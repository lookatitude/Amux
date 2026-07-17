package client_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	v1 "github.com/amux-run/amux/api/v1"

	"github.com/amux-run/amux/internal/client"
	"github.com/amux-run/amux/internal/protocol"
	"github.com/amux-run/amux/internal/rpcapi"
)

// projectionFixture reads one committed golden vector from the rpcapi
// contract testdata — the SAME frozen bytes the daemon emits — so this test
// (and any TUI test built the same way) is a deterministic fake daemon that
// cannot drift from the wire contract without the golden failing first.
func projectionFixture(t *testing.T, name string) json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "rpcapi", "testdata", name+".json"))
	if err != nil {
		t.Fatalf("missing rpcapi golden fixture %q: %v", name, err)
	}
	return raw
}

// TestProjectionMethodsAgainstFixtureBackend proves the typed projection
// wrappers strict-decode the frozen golden vectors over a real connection:
// the deterministic fake/client fixture path T5 builds on.
func TestProjectionMethodsAgainstFixtureBackend(t *testing.T) {
	b := startBackend(t, nil, "boot-fixture", 0)
	serveFixture := func(method, fixture string) {
		b.srv.HandleFunc(method, func(ctx context.Context, req protocol.Request) (json.RawMessage, *v1.ErrorBody) {
			return projectionFixture(t, fixture), nil
		})
	}
	serveFixture(rpcapi.MethodSurfaceCells, "surface_cells_result")
	serveFixture(rpcapi.MethodHookInspect, "hook_inspect_result")
	serveFixture(rpcapi.MethodPaneContext, "pane_context_result")
	serveFixture(rpcapi.MethodWorkspaceTree, "workspace_tree_result")
	cli := dialClient(t, b)
	ctx := context.Background()

	cells, err := cli.SurfaceCells(ctx, rpcapi.SurfaceCellsParams{Session: "ses-000001", Surface: "sur-000001"})
	if err != nil {
		t.Fatal(err)
	}
	if cells.Grid == nil || cells.Grid.Rows != 2 || cells.Grid.Cols != 3 || cells.UpToSeq != 42 {
		t.Fatalf("surface.cells fixture decode: %+v", cells)
	}
	if cells.Grid.Cells[1][0].Text != "世" || cells.Grid.Cells[1][0].Width != 2 || cells.Grid.Cells[1][1].Width != 0 {
		t.Fatalf("wide-cell pair lost in decode: %+v", cells.Grid.Cells[1])
	}

	hooks, err := cli.HookInspect(ctx, rpcapi.HookInspectParams{Project: "/repo"})
	if err != nil {
		t.Fatal(err)
	}
	if hooks.Project.State != "approved" || len(hooks.Grants) != 1 || hooks.Grants[0].ExecSHA256 == "" ||
		hooks.Grants[0].TimeoutMS != 2000 || hooks.Grants[0].OutputCap != 1048576 {
		t.Fatalf("hook.inspect fixture decode: %+v", hooks)
	}

	pc, err := cli.PaneContext(ctx, rpcapi.PaneContextParams{Session: "s", Workspace: "w", Pane: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if pc.GitBranch != "main" || !pc.GitDirty || pc.ForegroundPID != 4242 || pc.ExitCode == nil {
		t.Fatalf("pane.context fixture decode: %+v", pc)
	}

	tree, err := cli.WorkspaceTree(ctx, rpcapi.WorkspaceTreeParams{Session: "s", Workspace: "w"})
	if err != nil {
		t.Fatal(err)
	}
	if tree.Root == nil || tree.Root.Split == nil || tree.Root.Split.Ratio != 0.6 ||
		tree.Root.Split.First.Pane.ID != "pan-000001" {
		t.Fatalf("workspace.tree fixture decode: %+v", tree)
	}

	// An older daemon has no projection methods: the client must surface the
	// server's typed error, never fake a projection.
	_, err = cli.PaneContext(ctx, rpcapi.PaneContextParams{Session: "s", Workspace: "w", Pane: "p2"})
	if err != nil {
		t.Fatalf("fixture backend should still answer: %v", err)
	}
	var missing rpcapi.SurfaceCellsResult
	err = cli.Call(ctx, "surface.cells.v99", rpcapi.SurfaceCellsParams{Session: "s", Surface: "x"}, &missing)
	if err == nil || client.CodeOf(err) == "" {
		t.Fatalf("unknown method must be a typed error, got %v", err)
	}
}
