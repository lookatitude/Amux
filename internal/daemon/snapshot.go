package daemon

import (
	"context"
	"errors"
	"os"
	"time"

	v1 "github.com/amux-run/amux/api/v1"

	"github.com/amux-run/amux/internal/control"
	"github.com/amux-run/amux/internal/domain"
	"github.com/amux-run/amux/internal/persist"
	"github.com/amux-run/amux/internal/platform"
	"github.com/amux-run/amux/internal/pty"
	"github.com/amux-run/amux/internal/rpcapi"
	"github.com/amux-run/amux/internal/session"
	"github.com/amux-run/amux/internal/snapshot"
	"github.com/amux-run/amux/internal/store"
	"github.com/amux-run/amux/internal/terminal"
)

// SaveSnapshot implements flow 16: capture the session graph, event cursor, and
// each surface's runtime + raw-output sidecar into one atomic checkpoint
// generation (ADR-0005 commit ordering, previous-known-good retention).
func (e *Engine) SaveSnapshot(ctx context.Context, p rpcapi.SnapshotSaveParams) (rpcapi.SnapshotSaveResult, error) {
	rt, err := e.runtime(domain.SessionID(p.Session))
	if err != nil {
		return rpcapi.SnapshotSaveResult{}, err
	}
	graph, cursor, err := rt.graphActor().Snapshot(ctx)
	if err != nil {
		return rpcapi.SnapshotSaveResult{}, err
	}

	// Each surface's retained raw output becomes its own complete sidecar
	// stream; the streams are concatenated into the single replay-sidecar
	// component with per-surface offset/length recorded in the graph, because
	// surfaces' sequence spaces overlap (each starts at 1) and a flat shared
	// chunk list could not be partitioned back. The graph JSON never embeds raw
	// bytes (ADR-0005 / PRD F7).
	var sidecar []byte
	var surfaces []snapshot.SurfaceRuntime
	// liveSpawnIDs captures which process incarnations are live AT CAPTURE, so
	// a later in-daemon restore of THIS generation can prove same-identity
	// ownership (never persisted; daemon memory only).
	liveSpawnIDs := map[string]string{}
	rt.mu.Lock()
	for _, sid := range sortedSurfaceIDs(rt.surfaces) {
		sr := rt.surfaces[sid]
		if sr.live && sr.spawnID != "" {
			liveSpawnIDs[string(sid)] = sr.spawnID
		}
		snap := sr.ring.Snapshot()
		doc := snapshot.SurfaceRuntime{
			Surface:       string(sid),
			Argv:          sr.argv,
			Cwd:           sr.cwd,
			EnvAllowlist:  sr.env,
			RestartPolicy: sr.policy,
			ReplayNextSeq: snap.NextSeq,
		}
		if len(snap.Chunks) > 0 {
			chunks := make([]snapshot.Chunk, len(snap.Chunks))
			for i, c := range snap.Chunks {
				chunks[i] = snapshot.Chunk{Seq: c.Seq, Data: c.Data}
			}
			section, err := snapshot.EncodeSidecar(chunks)
			if err != nil {
				rt.mu.Unlock()
				return rpcapi.SnapshotSaveResult{}, &engineError{code: v1.ErrInternal, msg: err.Error()}
			}
			doc.SidecarPath = snapshot.ComponentFileName(persist.ComponentReplaySidecar)
			doc.SidecarOffset = int64(len(sidecar))
			doc.SidecarLength = int64(len(section))
			sidecar = append(sidecar, section...)
		}
		surfaces = append(surfaces, doc)
	}
	rt.mu.Unlock()

	doc := &snapshot.GraphDoc{
		Graph:        graph,
		Surfaces:     surfaces,
		EventCursor:  cursor,
		ReplayConfig: snapshot.ReplayConfig{PerSurfaceBytes: int64(e.deps.ReplayBytes)},
	}

	// Mint the checkpoint id up front so the notification export commits under
	// the SAME id the generation carries (store.ExportNotifications records it
	// as the committed checkpoint in one transaction, ADR-0005).
	checkpointID := control.NewID()
	components := map[persist.ComponentKind][]byte{}
	if e.deps.Store != nil {
		export, xerr := e.deps.Store.ExportNotifications(checkpointID)
		if xerr != nil {
			return rpcapi.SnapshotSaveResult{}, &engineError{code: v1.ErrInternal, msg: xerr.Error()}
		}
		components[persist.ComponentNotifyExport] = export
		doc.NotifyCheckpointID = checkpointID
	}
	graphBytes, err := snapshot.EncodeGraph(doc)
	if err != nil {
		return rpcapi.SnapshotSaveResult{}, &engineError{code: v1.ErrInternal, msg: err.Error()}
	}
	components[persist.ComponentGraph] = graphBytes
	if len(sidecar) > 0 {
		components[persist.ComponentReplaySidecar] = sidecar
	}

	w := &snapshot.Writer{NewCheckpointID: func() (string, error) { return checkpointID, nil }}
	manifest, err := w.Commit(e.deps.SnapshotDir, p.Session, components)
	if err != nil {
		return rpcapi.SnapshotSaveResult{}, &engineError{code: v1.ErrInternal, msg: err.Error()}
	}
	// Record the ownership attestation for the committed generation. A surface
	// that exits or restarts afterwards no longer matches it (live flag or
	// spawnID differs), so restore fails closed to stopped/restarted.
	e.mu.Lock()
	e.saved[domain.SessionID(p.Session)] = savedIdentity{checkpoint: manifest.CheckpointID, spawnIDs: liveSpawnIDs}
	e.mu.Unlock()
	return rpcapi.SnapshotSaveResult{CheckpointID: manifest.CheckpointID, Cursor: cursor}, nil
}

// RestoreSnapshot implements flow 17: load the latest valid generation,
// rehydrate the session graph, and classify every surface exactly one of
// live|restarted|stopped (spec success criterion 5).
//
// The restore runs in one of two modes (ADR-0005 restore classification):
//
//   - In-daemon reconcile: this daemon still hosts the session's runtime. A
//     surface classifies live ONLY when the loaded generation is the exact
//     checkpoint this incarnation saved AND the surface's identical spawn
//     identity is still live under this daemon's supervisor — that surface is
//     adopted untouched (its process is never stopped or relaunched). Every
//     other surface is rebuilt from the generation, and any live process the
//     checkpoint does not vouch for is stopped so the reported class is true.
//   - Fresh restore: the daemon owns no runtime for the session (fresh daemon
//     or never-hosted session), so no ownership evidence exists and the
//     classifier structurally excludes live.
//
// `restarted` is completed behavior, not intent (ADR-0005: a NEW process
// launched under an explicit automatic policy): an automatic-policy surface is
// reported restarted only after this restore actually spawned its replacement
// PTY — with the restored argv/cwd/env and the restore geometry — into the
// restored runtime. A failed launch demotes the surface fail-closed to stopped
// with the exact failure reason, retaining no live owner.
//
// Attachments and leases are never restored (ADR-0005 ephemeral rule); trust
// and security state are structurally out of restore's reach. Restore refuses
// corrupt generations and falls back to previous-known-good inside
// snapshot.OpenLatest.
func (e *Engine) RestoreSnapshot(ctx context.Context, p rpcapi.SnapshotRestoreParams) (rpcapi.SnapshotRestoreResult, error) {
	loaded, _, err := snapshot.OpenLatest(e.deps.SnapshotDir, p.Session)
	if err != nil {
		return rpcapi.SnapshotRestoreResult{}, &engineError{code: v1.ErrNotFound, msg: err.Error()}
	}
	doc := loaded.Graph
	if doc == nil || doc.Graph == nil {
		return rpcapi.SnapshotRestoreResult{}, &engineError{code: v1.ErrInternal, msg: "restored generation has no graph"}
	}
	sid := doc.Graph.Session

	// In-daemon vs fresh, plus the ownership attestation for the loaded
	// generation. sameGen is the trust gate: an older/previous-known-good
	// generation, or one saved by another incarnation, never matches, so its
	// surfaces can never classify live (fail closed).
	e.mu.Lock()
	prior := e.sessions[sid]
	rec, hasRec := e.saved[sid]
	e.mu.Unlock()
	inDaemon := prior != nil
	sameGen := inDaemon && hasRec && rec.checkpoint == loaded.Manifest.CheckpointID

	// Rebuild the session actor from the graph DTO (domain.Rehydrate fail-closes
	// on invariant violations) and resume the event cursor.
	act := session.New(session.Config{ID: sid, IDs: domain.NewUUIDv7Source(), Clock: e.deps.Clock})
	act.Start()
	if err := act.Restore(ctx, doc.Graph, doc.EventCursor); err != nil {
		act.Stop()
		return rpcapi.SnapshotRestoreResult{}, &engineError{code: v1.ErrInvalidArgument, msg: err.Error()}
	}

	// The runtime that will own the restored surfaces: the EXISTING one for an
	// in-daemon reconcile (its supervisor owns any still-live PTYs), a fresh
	// one otherwise. Until the commit step below, nothing in a prior runtime
	// is mutated, so any failure leaves the daemon exactly as it was.
	rt := prior
	if !inDaemon {
		rt = &sessionRuntime{actor: act, surfaces: map[domain.SurfaceID]*surfaceRuntime{}}
		sup, serr := ptySupervisor(e, rt)
		if serr != nil {
			act.Stop()
			return rpcapi.SnapshotRestoreResult{}, serr
		}
		rt.sup = sup
	}
	fail := func(ferr error) (rpcapi.SnapshotRestoreResult, error) {
		act.Stop()
		if !inDaemon {
			_ = rt.sup.StopAll()
		}
		return rpcapi.SnapshotRestoreResult{}, ferr
	}

	// Per-surface classification evidence: filesystem probes plus — only under
	// sameGen — proof that the checkpoint's live spawn identity is still owned
	// by this daemon's supervisor right now.
	inputs := map[string]persist.SurfaceRestoreInput{}
	for _, srDoc := range doc.Surfaces {
		in := persist.SurfaceRestoreInput{RestartPolicy: srDoc.RestartPolicy}
		if len(srDoc.Argv) > 0 {
			if _, err := os.Stat(srDoc.Argv[0]); err == nil {
				in.ExecutablePresent = true
			}
		}
		if srDoc.Cwd == "" {
			in.CwdPresent = true
		} else if fi, err := os.Stat(srDoc.Cwd); err == nil && fi.IsDir() {
			in.CwdPresent = true
		}
		if sameGen {
			if want := rec.spawnIDs[srDoc.Surface]; want != "" && rt.sup.Alive(srDoc.Surface) {
				rt.mu.Lock()
				cur := rt.surfaces[domain.SurfaceID(srDoc.Surface)]
				in.SamePTYIdentityOwned = cur != nil && cur.live && cur.spawnID == want
				rt.mu.Unlock()
			}
		}
		inputs[srDoc.Surface] = in
	}
	classes := snapshot.ClassifySurfaces(persist.RestoreContext{FreshDaemon: !inDaemon}, doc, func(sr snapshot.SurfaceRuntime) persist.SurfaceRestoreInput {
		return inputs[sr.Surface]
	})
	classOf := func(surface string) (persist.SurfaceClass, string) {
		for _, cl := range classes {
			if cl.Surface == surface {
				return cl.Class, cl.Reason
			}
		}
		return persist.ClassStopped, "surface missing from classification"
	}

	// Materialize every surface's replacement runtime BEFORE mutating any
	// state, rebuilding ring and VT grid from its OWN sidecar section —
	// sequence allocation resumes at the recorded next sequence, never reusing
	// numbers (ADR-0004). An adopted-live surface discards its replacement;
	// building it anyway keeps failure atomic (a corrupt sidecar aborts the
	// whole restore with the prior runtime untouched).
	rebuilt := map[string]*surfaceRuntime{}
	for _, srDoc := range doc.Surfaces {
		chunks, cerr := loaded.SurfaceReplayChunks(srDoc)
		if cerr != nil {
			return fail(&engineError{code: v1.ErrInternal, msg: cerr.Error()})
		}
		ring, rerr := terminal.NewRing(e.deps.ReplayBytes)
		if rerr != nil {
			return fail(&engineError{code: v1.ErrInternal, msg: rerr.Error()})
		}
		next := srDoc.ReplayNextSeq
		if next == 0 {
			next = 1
			for _, c := range chunks {
				if c.Seq >= next {
					next = c.Seq + 1
				}
			}
		}
		if len(chunks) > 0 || next > 1 {
			ringChunks := make([]terminal.Chunk, len(chunks))
			for i, c := range chunks {
				ringChunks[i] = terminal.Chunk{Seq: c.Seq, Data: c.Data}
			}
			if err := ring.RestoreFromSnapshot(ringChunks, next); err != nil {
				return fail(&engineError{code: v1.ErrInternal, msg: err.Error()})
			}
		}
		eng, eerr := terminal.NewEngine(24, 80)
		if eerr != nil {
			return fail(&engineError{code: v1.ErrInternal, msg: eerr.Error()})
		}
		for _, c := range chunks {
			eng.Feed(c.Data)
		}
		sr := &surfaceRuntime{
			id:     domain.SurfaceID(srDoc.Surface),
			ring:   ring,
			engine: eng,
			argv:   srDoc.Argv,
			cwd:    srDoc.Cwd,
			env:    srDoc.EnvAllowlist,
			policy: srDoc.RestartPolicy,
		}
		if aerr := attachSurfaceFor(rt, sr); aerr != nil {
			return fail(aerr)
		}
		rebuilt[srDoc.Surface] = sr
	}

	// Re-import the committed notification export — the ONLY thing an explicit
	// restore may import; trust state is structurally out of its reach
	// (ADR-0005). A generation older than the last committed export is a typed
	// checkpoint mismatch: notifications stay SQLite-current (deterministic
	// skip, never a partial import); any other import failure fails the
	// restore before the runtime commit below.
	if e.deps.Store != nil && doc.NotifyCheckpointID != "" {
		if export, ok := loaded.NotifyExport(); ok {
			ierr := e.deps.Store.ImportNotifications(doc.NotifyCheckpointID, export)
			if ierr != nil && !errors.Is(ierr, store.ErrCheckpointMismatch) {
				return fail(&engineError{code: v1.ErrInternal, msg: ierr.Error()})
			}
		}
	}

	// Commit. Adoption is re-verified under the runtime lock so a process that
	// exited between classification and here demotes fail-closed; a live
	// process the checkpoint does not vouch for (or one absent from the
	// restored graph) is stopped so the reported classification is true. A
	// surface classified restarted is only PENDING here: the class becomes
	// true — and is reported — only after the relaunch step below actually
	// spawned the replacement process.
	out := rpcapi.SnapshotRestoreResult{Session: string(sid), Cursor: doc.EventCursor}
	var stopIDs []string
	var oldAct *session.Actor
	type pendingRelaunch struct {
		sr     *surfaceRuntime
		outIdx int
		reason string // the classifier's restarted reason, applied on success
	}
	var relaunches []pendingRelaunch
	rt.mu.Lock()
	inDoc := map[domain.SurfaceID]bool{}
	for _, srDoc := range doc.Surfaces {
		id := domain.SurfaceID(srDoc.Surface)
		inDoc[id] = true
		class, reason := classOf(srDoc.Surface)
		cur := rt.surfaces[id]
		if class == persist.ClassLive {
			if sameGen && cur != nil && cur.live && cur.spawnID == rec.spawnIDs[srDoc.Surface] {
				// Adopt the still-owned surface untouched: same process, same
				// ring (a superset of the checkpoint's), same attach surface.
				cur.class, cur.reason = persist.ClassLive, reason
				out.Surfaces = append(out.Surfaces, rpcapi.RestoredSurface{
					Surface: srDoc.Surface, Class: string(persist.ClassLive), Reason: reason,
				})
				continue
			}
			in := inputs[srDoc.Surface]
			in.SamePTYIdentityOwned = false
			class, reason = persist.Classify(persist.RestoreContext{FreshDaemon: !inDaemon}, in)
		}
		if cur != nil && cur.live {
			stopIDs = append(stopIDs, srDoc.Surface)
		}
		ns := rebuilt[srDoc.Surface]
		ns.class, ns.reason = class, reason
		if class == persist.ClassRestarted {
			// Fail closed until the replacement process exists: never report
			// restarted (nor retain a live owner) on classification alone.
			ns.class, ns.reason = persist.ClassStopped, "automatic restart policy: relaunch pending"
			relaunches = append(relaunches, pendingRelaunch{sr: ns, outIdx: len(out.Surfaces), reason: reason})
		}
		rt.surfaces[id] = ns
		out.Surfaces = append(out.Surfaces, rpcapi.RestoredSurface{
			Surface: srDoc.Surface, Class: string(ns.class), Reason: ns.reason,
		})
	}
	for id, cur := range rt.surfaces {
		if !inDoc[id] {
			if cur.live {
				stopIDs = append(stopIDs, string(id))
			}
			delete(rt.surfaces, id)
		}
	}
	if rt.actor != act {
		oldAct = rt.actor
		rt.actor = act
	}
	rt.mu.Unlock()
	for _, id := range stopIDs {
		_ = rt.sup.Stop(id)
	}
	if oldAct != nil {
		oldAct.Stop()
	}

	// Relaunch every automatic-policy surface: `restarted` is a claim about
	// completed behavior, so the replacement PTY is spawned NOW — with the
	// restored argv/cwd/env and the restore geometry (24x80, the size the
	// rebuilt VT grid derives at) — into the already-installed runtime, so its
	// output lands in the restored ring after the checkpoint history and exit
	// tracking flows through the same supervisor callbacks. A failed launch
	// demotes the surface to stopped with the exact reason: never restarted,
	// never a false live owner (the unvouched predecessor was stopped above).
	for _, rl := range relaunches {
		id := string(rl.sr.id)
		spec := platform.PTYSpec{
			Argv: rl.sr.argv, Dir: rl.sr.cwd, Env: rl.sr.env,
			Size: platform.PTYSize{Rows: 24, Cols: 80},
		}
		serr := spawnReplacement(rt, id, spec)
		rt.mu.Lock()
		if serr == nil {
			rl.sr.live = true
			rl.sr.class = persist.ClassRestarted
			rl.sr.reason = rl.reason
			// The replacement is a NEW process: mint a fresh spawn identity so
			// only checkpoints saved from here on can vouch for it.
			rl.sr.spawnID = control.NewID()
		} else if !rl.sr.live {
			rl.sr.class = persist.ClassStopped
			rl.sr.reason = "automatic restart policy but relaunch failed: " + serr.Error()
		}
		out.Surfaces[rl.outIdx] = rpcapi.RestoredSurface{
			Surface: id, Class: string(rl.sr.class), Reason: rl.sr.reason,
		}
		rt.mu.Unlock()
	}

	// Register the restored session; a duplicate registration on an in-daemon
	// re-restore is tolerated (keep the runtime).
	if err := e.deps.Control.RegisterSession(ctx, control.SessionInfo{ID: string(sid), CreatedMS: e.deps.Clock.NowUnixMilli()}); err != nil {
		_ = err
	}
	if !inDaemon {
		e.mu.Lock()
		if old := e.sessions[sid]; old != nil && old != rt {
			// A concurrent create raced the fresh restore in: the restored
			// generation wins, replacing the newcomer (previous behavior).
			_ = old.sup.StopAll()
			old.graphActor().Stop()
		}
		e.sessions[sid] = rt
		e.mu.Unlock()
	}
	return out, nil
}

// relaunchRetryWindow bounds how long a restore relaunch waits for a just-
// stopped predecessor on the same surface id to retire in the supervisor
// before the launch is reported failed. Stop is asynchronous (SIGTERM, then
// SIGKILL escalation), so the window covers the supervisor's default grace
// period with margin.
const relaunchRetryWindow = 5 * time.Second

// spawnReplacement launches the replacement process for a restored
// automatic-policy surface. A conflict means the stopped predecessor has not
// retired yet, so it retries within the bounded window; any other error — or
// the window expiring — reports the launch as failed and the caller fails
// closed to stopped.
func spawnReplacement(rt *sessionRuntime, id string, spec platform.PTYSpec) error {
	deadline := time.Now().Add(relaunchRetryWindow)
	for {
		err := rt.sup.Spawn(id, spec)
		var conflict *pty.ConflictError
		if err == nil || !errors.As(err, &conflict) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
}
