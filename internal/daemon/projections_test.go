package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	v1 "github.com/amux-run/amux/api/v1"

	"github.com/amux-run/amux/internal/client"
	panectx "github.com/amux-run/amux/internal/context"
	"github.com/amux-run/amux/internal/control"
	"github.com/amux-run/amux/internal/rpcapi"
	"github.com/amux-run/amux/internal/transport/local"
)

// spawnLiveSurface creates session -> workspace -> spawned surface and waits
// until raw output is committed to the ring (so projections have content).
func spawnLiveSurface(t *testing.T, h *daemonHarness, firstPaneCwd string) (sid string, ws rpcapi.WorkspaceCreateResult, surface string) {
	t.Helper()
	created := call[rpcapi.SessionCreateResult](t, h, rpcapi.MethodSessionCreate, nil)
	sid = created.Session.ID
	ws = call[rpcapi.WorkspaceCreateResult](t, h, rpcapi.MethodWorkspaceCreate, rpcapi.WorkspaceCreateParams{Session: sid, Name: "main", FirstPaneCwd: firstPaneCwd})
	spawned := call[rpcapi.SurfaceSpawnResult](t, h, rpcapi.MethodSurfaceSpawn, rpcapi.SurfaceSpawnParams{
		Session: sid, Workspace: ws.Workspace, Pane: ws.FirstPane, Argv: []string{"/bin/cat"}, Cwd: t.TempDir(),
	})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r := call[rpcapi.ReplayReadResult](t, h, rpcapi.MethodReplayRead, rpcapi.ReplayReadParams{Session: sid, Surface: spawned.Surface, FromSeq: 1})
		if len(r.Chunks) > 0 {
			return sid, ws, spawned.Surface
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("surface produced no output")
	return
}

// gridRowText joins one wire grid row into its visible text (spacers skipped,
// blanks as spaces) — the same rule the golden renderer uses.
func gridRowText(row []rpcapi.SurfaceCell) string {
	var b strings.Builder
	for _, c := range row {
		switch {
		case c.Width == 0:
		case c.Text == "":
			b.WriteByte(' ')
		default:
			b.WriteString(c.Text)
		}
	}
	return strings.TrimRight(b.String(), " ")
}

// TestServerSurfaceCellsProjection proves surface.cells is a bounded, live,
// read-only projection of the daemon's derived grid: the payload reflects the
// exact raw bytes the ring committed, the geometry invariant holds on every
// row, the if_changed_since delta gate answers cheaply, the call mutates no
// graph state, and the projection survives a reconnect.
func TestServerSurfaceCellsProjection(t *testing.T) {
	h := startDaemon(t)
	sid, ws, surface := spawnLiveSurface(t, h, "/repo")

	res, err := h.cli.SurfaceCells(context.Background(), rpcapi.SurfaceCellsParams{Session: sid, Surface: surface})
	if err != nil {
		t.Fatal(err)
	}
	if res.Surface != surface || res.UpToSeq == 0 || res.Unchanged || res.Grid == nil {
		t.Fatalf("surface.cells = %+v", res)
	}
	g := res.Grid
	// Bounded, exact geometry: Rows slices of exactly Cols cells.
	if g.Rows == 0 || g.Cols == 0 || len(g.Cells) != g.Rows {
		t.Fatalf("grid geometry: rows=%d cols=%d len=%d", g.Rows, g.Cols, len(g.Cells))
	}
	for r, row := range g.Cells {
		if len(row) != g.Cols {
			t.Fatalf("row %d has %d cells, want %d", r, len(row), g.Cols)
		}
	}
	// Live backend authority: the grid content IS the raw output the daemon
	// committed ("wire-payload\r\n" leaves the text on row 0).
	if got := gridRowText(g.Cells[0]); got != "wire-payload" {
		t.Fatalf("grid row 0 = %q, want %q", got, "wire-payload")
	}
	// UpToSeq is the ring's committed cursor.
	replay := call[rpcapi.ReplayReadResult](t, h, rpcapi.MethodReplayRead, rpcapi.ReplayReadParams{Session: sid, Surface: surface, FromSeq: 1})
	if res.UpToSeq != replay.LatestSeq {
		t.Fatalf("UpToSeq %d != ring latest %d", res.UpToSeq, replay.LatestSeq)
	}

	// Delta gate: nothing new -> Unchanged with no grid; stale cursor -> grid.
	unchanged, err := h.cli.SurfaceCells(context.Background(), rpcapi.SurfaceCellsParams{Session: sid, Surface: surface, IfChangedSince: res.UpToSeq})
	if err != nil {
		t.Fatal(err)
	}
	if !unchanged.Unchanged || unchanged.Grid != nil || unchanged.UpToSeq != res.UpToSeq {
		t.Fatalf("delta gate broken: %+v", unchanged)
	}
	changed, err := h.cli.SurfaceCells(context.Background(), rpcapi.SurfaceCellsParams{Session: sid, Surface: surface, IfChangedSince: res.UpToSeq - 1})
	if err != nil {
		t.Fatal(err)
	}
	if changed.Unchanged || changed.Grid == nil {
		t.Fatalf("stale cursor must return the grid: %+v", changed)
	}

	// Read-only: the projection commits no graph mutation.
	before := call[rpcapi.WorkspaceListResult](t, h, rpcapi.MethodWorkspaceList, rpcapi.WorkspaceListParams{Session: sid}).Workspaces[0].Rev
	for i := 0; i < 3; i++ {
		if _, err := h.cli.SurfaceCells(context.Background(), rpcapi.SurfaceCellsParams{Session: sid, Surface: surface}); err != nil {
			t.Fatal(err)
		}
	}
	after := call[rpcapi.WorkspaceListResult](t, h, rpcapi.MethodWorkspaceList, rpcapi.WorkspaceListParams{Session: sid}).Workspaces[0].Rev
	if before != after {
		t.Fatalf("surface.cells mutated the graph: rev %d -> %d", before, after)
	}

	// Reconnect: a fresh client sees the same projection (no per-connection
	// state, same boot).
	cli2, err := client.Dial(context.Background(), local.New(), h.spec, "cells-reconnect")
	if err != nil {
		t.Fatal(err)
	}
	defer cli2.Close()
	res2, err := cli2.SurfaceCells(context.Background(), rpcapi.SurfaceCellsParams{Session: sid, Surface: surface})
	if err != nil {
		t.Fatal(err)
	}
	if res2.Grid == nil || gridRowText(res2.Grid.Cells[0]) != "wire-payload" {
		t.Fatalf("reconnected projection diverged: %+v", res2)
	}

	// Unknown surface fails typed, not silent.
	_, err = h.cli.SurfaceCells(context.Background(), rpcapi.SurfaceCellsParams{Session: sid, Surface: "sur-missing"})
	if client.CodeOf(err) != v1.ErrNotFound {
		t.Fatalf("missing surface must be not_found, got %v", err)
	}
	_ = ws
}

// TestServerAttachStreamCellsOptIn proves the additive attach integration:
// with cells=true the attach_snapshot payload carries the exact snapshot-at-N
// grid; without it the minor-0 payload is byte-compatible (no "cells" key).
func TestServerAttachStreamCellsOptIn(t *testing.T) {
	h := startDaemon(t)
	ctx := context.Background()
	sid, _, surface := spawnLiveSurface(t, h, "")

	// Opt-in: cells present and consistent with the snapshot cursor.
	cliA, err := client.Dial(ctx, local.New(), h.spec, "attach-cells")
	if err != nil {
		t.Fatal(err)
	}
	defer cliA.Close()
	sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	st, err := cliA.Attach(sctx, rpcapi.AttachParams{Session: sid, Surface: surface, FromSeq: 1, Cells: true})
	if err != nil {
		t.Fatal(err)
	}
	ev, _, err := st.Recv()
	if err != nil || ev.Event != "attach_snapshot" {
		t.Fatalf("first frame: %+v err=%v", ev, err)
	}
	var withCells struct {
		UpToSeq uint64                      `json:"up_to_seq"`
		Cells   *rpcapi.AttachSnapshotCells `json:"cells"`
	}
	if err := json.Unmarshal(ev.Payload, &withCells); err != nil {
		t.Fatal(err)
	}
	if withCells.Cells == nil {
		t.Fatalf("cells requested but absent: %s", ev.Payload)
	}
	if withCells.Cells.UpToSeq != withCells.UpToSeq {
		t.Fatalf("cells.up_to_seq %d != snapshot up_to_seq %d (must be the SAME linearized capture)",
			withCells.Cells.UpToSeq, withCells.UpToSeq)
	}
	if got := gridRowText(withCells.Cells.Grid.Cells[0]); got != "wire-payload" {
		t.Fatalf("attach cells row 0 = %q", got)
	}

	// No opt-in: payload has no "cells" key (old clients see minor-0 shape).
	cliB, err := client.Dial(ctx, local.New(), h.spec, "attach-plain")
	if err != nil {
		t.Fatal(err)
	}
	defer cliB.Close()
	sctx2, cancel2 := context.WithTimeout(ctx, 5*time.Second)
	defer cancel2()
	st2, err := cliB.Attach(sctx2, rpcapi.AttachParams{Session: sid, Surface: surface, FromSeq: 1})
	if err != nil {
		t.Fatal(err)
	}
	ev2, _, err := st2.Recv()
	if err != nil {
		t.Fatal(err)
	}
	var plain map[string]json.RawMessage
	if err := json.Unmarshal(ev2.Payload, &plain); err != nil {
		t.Fatal(err)
	}
	if _, present := plain["cells"]; present {
		t.Fatalf("cells must be strictly opt-in; payload = %s", ev2.Payload)
	}
}

// TestServerHookInspectProjection proves hook.inspect exposes the FULL frozen
// trust presentation (identity, digests, events, scope, env KEYS, timeout,
// cap, epoch/status) directly from the durable store, read-only: no epoch
// bump, no grant change, no audit growth, and env values never on the wire.
func TestServerHookInspectProjection(t *testing.T) {
	h := startDaemon(t)
	ctx := context.Background()
	project := t.TempDir()

	approved := call[rpcapi.EpochResult](t, h, rpcapi.MethodHookApprove, rpcapi.HookApproveParams{Project: project, Confirm: true})
	key, err := h.control.RegisterProject(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	grantID, err := h.control.ApproveGrant(ctx, "", key, control.GrantInput{
		HookID: "on-save", ExecPath: project + "/.amux/hooks/on-save",
		ExecSHA256: "aa11bb22", ConfigSHA256: "cc33dd44",
		AllowedEvents: []string{"surface_exit", "pane_focus"},
		Scope:         control.ScopeWorkspacePrimary,
		EnvAllowlist:  []string{"PATH", "HOME"},
		TimeoutMS:     2500, OutputCap: 65536,
	})
	if err != nil {
		t.Fatal(err)
	}

	auditBefore, err := h.control.Audit(ctx)
	if err != nil {
		t.Fatal(err)
	}

	insp, err := h.cli.HookInspect(ctx, rpcapi.HookInspectParams{Project: project})
	if err != nil {
		t.Fatal(err)
	}
	if insp.Project.Key != string(key) || insp.Project.Root == "" {
		t.Fatalf("project identity missing: %+v", insp.Project)
	}
	if insp.Project.State != "approved" || insp.Project.Epoch != approved.Epoch {
		t.Fatalf("trust status wrong: %+v (want approved@%d)", insp.Project, approved.Epoch)
	}
	if len(insp.Grants) != 1 {
		t.Fatalf("grants = %+v", insp.Grants)
	}
	g := insp.Grants[0]
	if g.ID != grantID || g.HookID != "on-save" ||
		g.ExecPath != project+"/.amux/hooks/on-save" || g.ExecSHA256 != "aa11bb22" ||
		g.ConfigSHA256 != "cc33dd44" || g.Scope != string(control.ScopeWorkspacePrimary) ||
		g.TimeoutMS != 2500 || g.OutputCap != 65536 ||
		g.BoundEpoch != approved.Epoch || !g.Active {
		t.Fatalf("grant detail lost fields: %+v", g)
	}
	if len(g.Events) != 2 || g.Events[0] != "surface_exit" {
		t.Fatalf("events = %v", g.Events)
	}
	if len(g.EnvKeys) != 2 || g.EnvKeys[0] != "PATH" || g.EnvKeys[1] != "HOME" {
		t.Fatalf("env key allowlist = %v", g.EnvKeys)
	}
	// Secrets stay off the wire: the payload carries key NAMES only — no '='
	// anywhere in the allowlist (values are rejected at grant time, ADR-0005).
	for _, k := range g.EnvKeys {
		if strings.Contains(k, "=") {
			t.Fatalf("env allowlist leaked a value: %q", k)
		}
	}

	// Read-only proof: same epoch, same grants, no new audit rows.
	epoch, err := h.control.Epoch(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if epoch != approved.Epoch {
		t.Fatalf("inspect changed the epoch: %d -> %d", approved.Epoch, epoch)
	}
	auditAfter, err := h.control.Audit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(auditAfter) != len(auditBefore) {
		t.Fatalf("inspect grew the audit log: %d -> %d", len(auditBefore), len(auditAfter))
	}

	// Revocation is visible and fails closed in presentation terms: state
	// flips, epoch bumps, the grant survives as INACTIVE history bound to the
	// now-stale epoch.
	revoked := call[rpcapi.EpochResult](t, h, rpcapi.MethodHookRevoke, rpcapi.HookRevokeParams{Project: project, Confirm: true})
	insp2, err := h.cli.HookInspect(ctx, rpcapi.HookInspectParams{Project: project})
	if err != nil {
		t.Fatal(err)
	}
	if insp2.Project.State != "revoked" || insp2.Project.Epoch != revoked.Epoch {
		t.Fatalf("post-revoke status: %+v", insp2.Project)
	}
	if len(insp2.Grants) != 1 || insp2.Grants[0].Active || insp2.Grants[0].BoundEpoch >= revoked.Epoch {
		t.Fatalf("post-revoke grant must be retained, inactive, stale-bound: %+v", insp2.Grants)
	}

	// A never-decided project reports the empty (fail-closed) state.
	fresh, err := h.cli.HookInspect(ctx, rpcapi.HookInspectParams{Project: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Project.State != "" || len(fresh.Grants) != 0 {
		t.Fatalf("undecided project must present fail-closed defaults: %+v", fresh)
	}
}

// TestServerWorkspaceTreeProjection proves workspace.tree projects the
// authoritative split tree — stable IDs, orientation, nesting, ratios, focus,
// active surface, deterministic orders — read-only, and that it tracks live
// mutations (resize) rather than a cached copy.
func TestServerWorkspaceTreeProjection(t *testing.T) {
	h := startDaemon(t)
	ctx := context.Background()
	created := call[rpcapi.SessionCreateResult](t, h, rpcapi.MethodSessionCreate, nil)
	sid := created.Session.ID
	ws := call[rpcapi.WorkspaceCreateResult](t, h, rpcapi.MethodWorkspaceCreate, rpcapi.WorkspaceCreateParams{Session: sid, Name: "main", FirstPaneCwd: "/repo"})
	sp1 := call[rpcapi.PaneSplitResult](t, h, rpcapi.MethodPaneSplit, rpcapi.PaneSplitParams{
		Session: sid, Workspace: ws.Workspace, Target: ws.FirstPane, Orientation: rpcapi.OrientHorizontal, Ratio: 0.6,
	})
	sp2 := call[rpcapi.PaneSplitResult](t, h, rpcapi.MethodPaneSplit, rpcapi.PaneSplitParams{
		Session: sid, Workspace: ws.Workspace, Target: sp1.NewPane, Orientation: rpcapi.OrientVertical, Ratio: 0.3,
	})
	call[rpcapi.RevResult](t, h, rpcapi.MethodPaneFocus, rpcapi.PaneFocusParams{Session: sid, Workspace: ws.Workspace, Pane: sp2.NewPane})

	tree, err := h.cli.WorkspaceTree(ctx, rpcapi.WorkspaceTreeParams{Session: sid, Workspace: ws.Workspace})
	if err != nil {
		t.Fatal(err)
	}
	if tree.Workspace != ws.Workspace || tree.Name != "main" || tree.Focused != sp2.NewPane {
		t.Fatalf("tree header = %+v", tree)
	}
	root := tree.Root
	if root == nil || root.Split == nil || root.Split.Orientation != rpcapi.OrientHorizontal || root.Split.Ratio != sp1.Ratio {
		t.Fatalf("root split = %+v", root)
	}
	if root.Split.First == nil || root.Split.First.Pane == nil || root.Split.First.Pane.ID != ws.FirstPane {
		t.Fatalf("first leaf = %+v", root.Split.First)
	}
	inner := root.Split.Second
	if inner == nil || inner.Split == nil || inner.Split.Orientation != rpcapi.OrientVertical || inner.Split.Ratio != sp2.Ratio {
		t.Fatalf("inner split = %+v", inner)
	}
	focusedLeaf := inner.Split.Second
	if focusedLeaf == nil || focusedLeaf.Pane == nil || focusedLeaf.Pane.ID != sp2.NewPane || !focusedLeaf.Pane.Focused {
		t.Fatalf("focused leaf = %+v", focusedLeaf)
	}
	// Leaf order == PaneOrder, and every leaf carries its active surface.
	wantOrder := []string{ws.FirstPane, sp1.NewPane, sp2.NewPane}
	if len(tree.PaneOrder) != 3 {
		t.Fatalf("pane order = %v", tree.PaneOrder)
	}
	for i, id := range wantOrder {
		if tree.PaneOrder[i] != id {
			t.Fatalf("pane order = %v, want %v", tree.PaneOrder, wantOrder)
		}
	}
	var leaves []*rpcapi.TreePane
	var walk func(n *rpcapi.TreeNode)
	walk = func(n *rpcapi.TreeNode) {
		if n == nil {
			return
		}
		if n.Pane != nil {
			leaves = append(leaves, n.Pane)
			return
		}
		walk(n.Split.First)
		walk(n.Split.Second)
	}
	walk(root)
	if len(leaves) != 3 {
		t.Fatalf("tree has %d leaves", len(leaves))
	}
	for i, leaf := range leaves {
		if leaf.ID != wantOrder[i] {
			t.Fatalf("leaf order %v diverges from pane order %v", leaves, wantOrder)
		}
		if leaf.Active == "" || len(leaf.Surfaces) == 0 {
			t.Fatalf("leaf %s missing surfaces/active: %+v", leaf.ID, leaf)
		}
	}
	if got := tree.FocusHistory[len(tree.FocusHistory)-1]; got != sp2.NewPane {
		t.Fatalf("focus history tail = %q, want %q", got, sp2.NewPane)
	}

	// Live authority: a committed resize is visible on the next projection,
	// with the rev advancing — no snapshot caching, no second layout model.
	resized := call[rpcapi.RevResult](t, h, rpcapi.MethodPaneResize, rpcapi.PaneResizeParams{Session: sid, Workspace: ws.Workspace, Pane: ws.FirstPane, Ratio: 0.25})
	tree2, err := h.cli.WorkspaceTree(ctx, rpcapi.WorkspaceTreeParams{Session: sid, Workspace: ws.Workspace})
	if err != nil {
		t.Fatal(err)
	}
	if tree2.Root.Split.Ratio != 0.25 || tree2.Rev < tree.Rev || tree2.Rev != resized.Rev {
		t.Fatalf("resize not reflected: ratio=%v rev=%d (want 0.25 rev=%d)", tree2.Root.Split.Ratio, tree2.Rev, resized.Rev)
	}

	// Read-only: repeated projections do not advance the workspace rev.
	tree3, err := h.cli.WorkspaceTree(ctx, rpcapi.WorkspaceTreeParams{Session: sid, Workspace: ws.Workspace})
	if err != nil {
		t.Fatal(err)
	}
	if tree3.Rev != tree2.Rev {
		t.Fatalf("workspace.tree mutated the graph: rev %d -> %d", tree2.Rev, tree3.Rev)
	}

	// Unknown workspace fails typed.
	_, err = h.cli.WorkspaceTree(ctx, rpcapi.WorkspaceTreeParams{Session: sid, Workspace: "wsp-missing"})
	if client.CodeOf(err) != v1.ErrNotFound {
		t.Fatalf("missing workspace must be not_found, got %v", err)
	}
}

// TestServerPaneContextProjection proves pane.context serves DAEMON-owned B10
// context through the injected collectors — graph cwd, bounded git facts,
// foreground process while live, the recorded exit code after stop — and that
// absent collectors mean honest zero values, never fabrication.
func TestServerPaneContextProjection(t *testing.T) {
	gitCalls := 0
	h := startDaemon(t, func(d *Deps) {
		d.GitContext = func(_ context.Context, cwd string) (panectx.GitInfo, error) {
			gitCalls++
			return panectx.GitInfo{Present: true, Root: "/repo", Branch: "main", Dirty: true}, nil
		}
		d.Foreground = func(fd uintptr) (int, string, error) {
			return 4242, "nvim", nil
		}
	})
	ctx := context.Background()
	sid, ws, surface := spawnLiveSurface(t, h, "/repo/src")

	pc, err := h.cli.PaneContext(ctx, rpcapi.PaneContextParams{Session: sid, Workspace: ws.Workspace, Pane: ws.FirstPane})
	if err != nil {
		t.Fatal(err)
	}
	if pc.Pane != ws.FirstPane || pc.Cwd != "/repo/src" {
		t.Fatalf("pane identity/cwd = %+v", pc)
	}
	if pc.GitRoot != "/repo" || pc.GitBranch != "main" || !pc.GitDirty {
		t.Fatalf("git context not projected: %+v", pc)
	}
	if pc.ForegroundPID != 4242 || pc.ForegroundCmd != "nvim" {
		t.Fatalf("foreground context not projected: %+v", pc)
	}
	if pc.ExitCode != nil {
		t.Fatalf("live surface must not report an exit code: %+v", pc)
	}
	if pc.UpdatedMS == 0 {
		t.Fatal("UpdatedMS missing")
	}
	if gitCalls == 0 {
		t.Fatal("daemon-side collector was not consulted (context must be daemon-owned)")
	}

	// After a confirmed stop the recorded exit code appears and the
	// foreground fields stop being reported (the process is gone).
	call[rpcapi.SurfaceStopResult](t, h, rpcapi.MethodSurfaceStop, rpcapi.SurfaceStopParams{
		Session: sid, Workspace: ws.Workspace, Pane: ws.FirstPane, Surface: surface, Confirm: true,
	})
	deadline := time.Now().Add(2 * time.Second)
	var stopped rpcapi.PaneContextResult
	for time.Now().Before(deadline) {
		stopped, err = h.cli.PaneContext(ctx, rpcapi.PaneContextParams{Session: sid, Workspace: ws.Workspace, Pane: ws.FirstPane})
		if err != nil {
			t.Fatal(err)
		}
		if stopped.ExitCode != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if stopped.ExitCode == nil || *stopped.ExitCode != 0 {
		t.Fatalf("recorded exit code not projected: %+v", stopped)
	}
	if stopped.ForegroundPID != 0 || stopped.ForegroundCmd != "" {
		t.Fatalf("stopped surface must not report a foreground process: %+v", stopped)
	}

	// Fail-closed harness: no collectors wired -> git/foreground stay zero,
	// cwd (graph authority) still present. Nothing is fabricated.
	bare := startDaemon(t)
	bsid, bws, _ := spawnLiveSurface(t, bare, "/elsewhere")
	bpc, err := bare.cli.PaneContext(ctx, rpcapi.PaneContextParams{Session: bsid, Workspace: bws.Workspace, Pane: bws.FirstPane})
	if err != nil {
		t.Fatal(err)
	}
	if bpc.Cwd != "/elsewhere" {
		t.Fatalf("cwd lost: %+v", bpc)
	}
	if bpc.GitRoot != "" || bpc.GitBranch != "" || bpc.GitDirty || bpc.ForegroundPID != 0 || bpc.ForegroundCmd != "" {
		t.Fatalf("absent collectors must project honest absence: %+v", bpc)
	}

	// Unknown pane fails typed.
	_, err = h.cli.PaneContext(ctx, rpcapi.PaneContextParams{Session: sid, Workspace: ws.Workspace, Pane: "pan-missing"})
	if client.CodeOf(err) != v1.ErrNotFound {
		t.Fatalf("missing pane must be not_found, got %v", err)
	}
}
