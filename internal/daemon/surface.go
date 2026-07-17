package daemon

import (
	"context"
	"errors"
	"strings"

	v1 "github.com/amux-run/amux/api/v1"

	"github.com/amux-run/amux/internal/control"
	"github.com/amux-run/amux/internal/domain"
	"github.com/amux-run/amux/internal/persist"
	"github.com/amux-run/amux/internal/platform"
	"github.com/amux-run/amux/internal/rpcapi"
	"github.com/amux-run/amux/internal/terminal"
)

// SpawnSurface implements flow 11: append a surface to a pane (a domain
// mutation) and, if argv is given, launch a real PTY whose output flows into
// the surface's bounded replay ring and VT engine. A surface with no argv is a
// metadata-only surface (the TUI may fill it later); the flows that need a
// process pass argv.
func (e *Engine) SpawnSurface(ctx context.Context, p rpcapi.SurfaceSpawnParams) (rpcapi.SurfaceSpawnResult, error) {
	if err := validateEnvKeys(p.Env); err != nil {
		return rpcapi.SurfaceSpawnResult{}, err
	}
	policy, err := restartPolicy(p.RestartPolicy)
	if err != nil {
		return rpcapi.SurfaceSpawnResult{}, err
	}
	// The PTY layer requires an explicit working directory (fail closed, never
	// inherit the daemon's cwd): default a process spawn to the pane's
	// recorded cwd and reject when neither is present — BEFORE the graph
	// mutation commits, so a refused spawn leaves no orphan surface.
	if len(p.Argv) > 0 && p.Cwd == "" {
		if st, serr := e.state(ctx, domain.SessionID(p.Session)); serr == nil {
			if w, ok := st.Workspace(domain.WorkspaceID(p.Workspace)); ok {
				if pane, ok := w.Pane(domain.PaneID(p.Pane)); ok {
					p.Cwd = pane.Cwd
				}
			}
		}
		if p.Cwd == "" {
			return rpcapi.SurfaceSpawnResult{}, &engineError{code: v1.ErrInvalidArgument, msg: "surface.spawn requires cwd (neither the request nor the pane carries a working directory)"}
		}
	}
	res, err := e.submit(ctx, domain.SessionID(p.Session), domain.SpawnSurface{
		Workspace: domain.WorkspaceID(p.Workspace),
		Pane:      domain.PaneID(p.Pane),
		Title:     p.Title,
	})
	if err != nil {
		return rpcapi.SurfaceSpawnResult{}, err
	}
	ev := res.Events[0].Payload.(domain.SurfaceSpawned)

	rt, err := e.runtime(domain.SessionID(p.Session))
	if err != nil {
		return rpcapi.SurfaceSpawnResult{}, err
	}
	ring, err := terminal.NewRing(e.deps.ReplayBytes)
	if err != nil {
		return rpcapi.SurfaceSpawnResult{}, &engineError{code: v1.ErrInternal, msg: err.Error()}
	}
	rows, cols := geom(p.Rows, p.Cols)
	eng, err := terminal.NewEngine(int(rows), int(cols))
	if err != nil {
		return rpcapi.SurfaceSpawnResult{}, &engineError{code: v1.ErrInvalidArgument, msg: err.Error()}
	}
	sr := &surfaceRuntime{
		id:     ev.Surface,
		ring:   ring,
		engine: eng,
		argv:   p.Argv,
		cwd:    p.Cwd,
		env:    p.Env,
		policy: policy,
	}
	if err := attachSurfaceFor(rt, sr); err != nil {
		return rpcapi.SurfaceSpawnResult{}, err
	}
	rt.mu.Lock()
	rt.surfaces[ev.Surface] = sr
	rt.mu.Unlock()

	if len(p.Argv) > 0 {
		spec := platform.PTYSpec{
			Argv: p.Argv,
			Dir:  p.Cwd,
			Env:  p.Env,
			Size: platform.PTYSize{Rows: rows, Cols: cols},
		}
		if err := rt.sup.Spawn(string(ev.Surface), spec); err != nil {
			return rpcapi.SurfaceSpawnResult{}, &engineError{code: v1.ErrInternal, msg: err.Error()}
		}
		rt.mu.Lock()
		sr.live = true
		sr.class = persist.ClassLive
		sr.spawnID = control.NewID()
		rt.mu.Unlock()
	}
	return rpcapi.SurfaceSpawnResult{Surface: string(ev.Surface), Rev: res.Rev}, nil
}

// StopSurface implements flow 19: stop a surface's process. The confirmation
// token is mandatory for this destructive op (spec confirmation matrix);
// missing confirmation fails closed. Detach is NOT stop — this stops the
// process (ADR-0004).
func (e *Engine) StopSurface(ctx context.Context, p rpcapi.SurfaceStopParams) (rpcapi.SurfaceStopResult, error) {
	if !p.Confirm {
		return rpcapi.SurfaceStopResult{}, &engineError{code: v1.ErrInvalidArgument, msg: "stopping a surface requires confirmation"}
	}
	rt, err := e.runtime(domain.SessionID(p.Session))
	if err != nil {
		return rpcapi.SurfaceStopResult{}, err
	}
	rt.mu.Lock()
	sr := rt.surfaces[domain.SurfaceID(p.Surface)]
	rt.mu.Unlock()
	if sr == nil {
		return rpcapi.SurfaceStopResult{}, &engineError{code: v1.ErrNotFound, msg: "surface not found"}
	}
	if err := rt.sup.Stop(string(p.Surface)); err != nil {
		// Not live is not an error to the caller: report the current class.
		_ = err
	}
	rt.mu.Lock()
	class, reason := sr.class, sr.reason
	if class == "" {
		class = persist.ClassStopped
		reason = "manual stop"
	}
	rt.mu.Unlock()
	return rpcapi.SurfaceStopResult{Class: string(class), ExitReason: reason}, nil
}

// RestartSurface implements flow 18: relaunch a stopped surface's process. A
// fresh process is a NEW process — the classification is "restarted", never
// "live" reconstructed from memory (spec success criterion 5).
func (e *Engine) RestartSurface(ctx context.Context, p rpcapi.SurfaceRestartParams) (rpcapi.SurfaceRestartResult, error) {
	rt, err := e.runtime(domain.SessionID(p.Session))
	if err != nil {
		return rpcapi.SurfaceRestartResult{}, err
	}
	rt.mu.Lock()
	sr := rt.surfaces[domain.SurfaceID(p.Surface)]
	rt.mu.Unlock()
	if sr == nil {
		return rpcapi.SurfaceRestartResult{}, &engineError{code: v1.ErrNotFound, msg: "surface not found"}
	}
	if sr.live {
		return rpcapi.SurfaceRestartResult{}, &engineError{code: v1.ErrConflict, msg: "surface is already live"}
	}
	if len(sr.argv) == 0 {
		return rpcapi.SurfaceRestartResult{}, &engineError{code: v1.ErrInvalidArgument, msg: "surface has no recorded argv to restart"}
	}
	spec := platform.PTYSpec{Argv: sr.argv, Dir: sr.cwd, Env: sr.env, Size: platform.PTYSize{Rows: 24, Cols: 80}}
	if err := rt.sup.Spawn(string(p.Surface), spec); err != nil {
		return rpcapi.SurfaceRestartResult{}, &engineError{code: v1.ErrInternal, msg: err.Error()}
	}
	rt.mu.Lock()
	sr.live = true
	sr.class = persist.ClassRestarted
	sr.reason = "relaunched by restart"
	// A restart is a NEW process: mint a new spawn identity so no earlier
	// checkpoint's live attestation can match it (ADR-0005: same PTY identity).
	sr.spawnID = control.NewID()
	rt.mu.Unlock()
	return rpcapi.SurfaceRestartResult{Class: string(persist.ClassRestarted), ExitReason: "relaunched by restart"}, nil
}

// Bounds for one replay.read page. The unary result travels inside the
// response frame HEADER, so the raw decoded payload cap and the chunk-count
// cap together must keep the encoded JSON (base64 payload + per-chunk
// framing) safely under v1.MaxHeaderBytes: 512 KiB of raw bytes encode to
// ~683 KiB of base64, and 4096 chunks cost at most ~64 B of JSON framing each
// (~256 KiB) — ~940 KiB worst case against the 1 MiB header cap. The bounds
// page the READ; the ring's 16 MiB retention floor is untouched.
const (
	replayReadMaxBytes  = 512 << 10
	replayReadMaxChunks = 4096
)

// ReplayRead implements flow 14: bounded raw replay from a cursor. The page
// bound semantics (max_bytes zero/positive/negative, whole-chunk truth,
// next_seq continuation) are contract-documented on rpcapi.ReplayReadParams/
// Result. A cursor older than the retained window returns a replay_gap error
// whose structured details (rpcapi.ReplayGapDetails) carry the oldest
// retained and latest sequences (ADR-0004 explicit gap boundary; automation
// never parses the message).
//
// The page and latest_seq come from ONE ring snapshot (Ring.ReplayPageBytes),
// and the empty-page next_seq derives from that snapshot's latest — never
// from a second LatestSeq() sample. A chunk appended right after an
// empty-page snapshot therefore always sits at or past the advertised
// next_seq and surfaces on the next page; sampling latest separately would
// let the response advertise a cursor one past that unseen chunk (a silent
// skip, G-lane F5).
func (e *Engine) ReplayRead(ctx context.Context, p rpcapi.ReplayReadParams) (rpcapi.ReplayReadResult, error) {
	if p.MaxBytes < 0 {
		return rpcapi.ReplayReadResult{}, &engineError{code: v1.ErrInvalidArgument, msg: "max_bytes must be >= 0"}
	}
	rt, err := e.runtime(domain.SessionID(p.Session))
	if err != nil {
		return rpcapi.ReplayReadResult{}, err
	}
	rt.mu.Lock()
	sr := rt.surfaces[domain.SurfaceID(p.Surface)]
	rt.mu.Unlock()
	if sr == nil {
		return rpcapi.ReplayReadResult{}, &engineError{code: v1.ErrNotFound, msg: "surface not found"}
	}
	from := p.FromSeq
	if from == 0 {
		from = 1
	}
	// The effective page bound: the caller's ask, clamped to the server cap so
	// the encoded response always fits one frame header. The decoded payload
	// therefore never exceeds the caller's max_bytes.
	bound := replayReadMaxBytes
	if p.MaxBytes > 0 && p.MaxBytes < int64(bound) {
		bound = int(p.MaxBytes)
	}
	page, err := sr.ring.ReplayPageBytes(from, replayReadMaxChunks, bound)
	if err != nil {
		var gap *terminal.ReplayGapError
		if isReplayGap(err, &gap) {
			return rpcapi.ReplayReadResult{}, &engineError{
				code: v1.ErrReplayGap,
				msg:  err.Error(),
				details: errorDetails(rpcapi.ReplayGapDetails{
					FromSeq:        gap.FromSeq,
					OldestRetained: gap.OldestRetained,
					LatestSeq:      gap.LatestSeq,
				}),
			}
		}
		var tooSmall *terminal.BoundTooSmallError
		if errors.As(err, &tooSmall) {
			return rpcapi.ReplayReadResult{}, &engineError{
				code: v1.ErrInvalidArgument,
				msg:  err.Error(),
				details: errorDetails(rpcapi.ReplayBoundDetails{
					MaxBytes:       int64(tooSmall.MaxBytes),
					NextChunkBytes: int64(tooSmall.NextChunkBytes),
				}),
			}
		}
		return rpcapi.ReplayReadResult{}, &engineError{code: v1.ErrInvalidArgument, msg: err.Error()}
	}
	out := rpcapi.ReplayReadResult{LatestSeq: page.LatestSeq}
	// NextSeq is sequence truth for continuation: on a bounded partial page it
	// is the first sequence NOT returned; on an empty page the caller is
	// already current as of the snapshot's latest.
	if len(page.Chunks) > 0 {
		out.NextSeq = page.Chunks[len(page.Chunks)-1].Seq + 1
	} else {
		out.NextSeq = page.LatestSeq + 1
	}
	for _, c := range page.Chunks {
		out.Chunks = append(out.Chunks, rpcapi.ReplayChunk{Seq: c.Seq, DataB64: b64(c.Data)})
	}
	return out, nil
}

func geom(rows, cols uint16) (uint16, uint16) {
	if rows == 0 {
		rows = 24
	}
	if cols == 0 {
		cols = 80
	}
	return rows, cols
}

func restartPolicy(s string) (persist.RestartPolicy, error) {
	switch s {
	case "", string(persist.RestartManual):
		return persist.RestartManual, nil
	case string(persist.RestartAutomatic):
		return persist.RestartAutomatic, nil
	default:
		return "", &engineError{code: v1.ErrInvalidArgument, msg: "restart_policy must be manual or automatic"}
	}
}

// validateEnvKeys enforces the non-secret allowlist rule: entries are KEYS,
// never key=value (ADR-0005 non-secret env allowlist; no secret persistence).
func validateEnvKeys(env []string) error {
	for _, k := range env {
		if k == "" || strings.ContainsRune(k, '=') {
			return &engineError{code: v1.ErrInvalidArgument, msg: "env allowlist entries are keys, not key=value pairs"}
		}
	}
	return nil
}
