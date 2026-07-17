package rpcapi

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	v1 "github.com/amux-run/amux/api/v1"
)

// update regenerates the golden vectors under testdata/. Run:
//
//	go test ./internal/rpcapi/ -run TestProjectionGoldenVectors -update
//
// The committed vectors freeze the minor-1 projection payload shapes
// (ADR-0003): changing any of them is a protocol change that must be visible
// in review.
var update = flag.Bool("update", false, "regenerate golden vectors in testdata/")

func intp(v int) *int { return &v }

// projectionGoldenCases are canonical examples of every minor-1 projection
// payload. Values are stable and human-meaningful so the files double as
// contract documentation for the TUI lane.
func projectionGoldenCases() []struct {
	name string
	v    any
} {
	grid := CellGrid{
		Rows: 2, Cols: 3,
		Cells: [][]SurfaceCell{
			{
				{Text: "h", Width: 1, Style: &CellStyle{FG: &CellColor{Mode: CellColorANSI, Index: 1}, Attrs: CellAttrBold}},
				{Text: "i", Width: 1},
				{Width: 1},
			},
			{
				{Text: "世", Width: 2, Style: &CellStyle{BG: &CellColor{Mode: CellColorRGB, R: 10, G: 20, B: 30}}},
				{Width: 0},
				{Width: 1},
			},
		},
		Cursor:       CellCursor{Row: 0, Col: 2, Visible: true},
		Pen:          &CellStyle{FG: &CellColor{Mode: CellColor256, Index: 208}},
		Title:        "amux",
		Autowrap:     true,
		ScrollTop:    0,
		ScrollBottom: 1,
	}
	return []struct {
		name string
		v    any
	}{
		{"surface_cells_params", SurfaceCellsParams{Session: "ses-000001", Surface: "sur-000001", IfChangedSince: 41}},
		{"surface_cells_result", SurfaceCellsResult{Surface: "sur-000001", UpToSeq: 42, Grid: &grid}},
		{"surface_cells_unchanged", SurfaceCellsResult{Surface: "sur-000001", UpToSeq: 41, Unchanged: true}},
		{"attach_snapshot_cells", AttachSnapshotCells{UpToSeq: 42, Grid: grid}},
		{"hook_inspect_result", HookInspectResult{
			Project: HookProjectTrust{Key: "9f86d081884c7d65", Root: "/repo", State: "approved", Epoch: 3},
			Grants: []HookGrantDetail{{
				ID: "grt-000001", HookID: "on-save", ExecPath: "/repo/.amux/hooks/on-save",
				ExecSHA256: "aa11bb22cc33dd44", ConfigSHA256: "ee55ff6677889900",
				Events: []string{"surface_exit"}, Scope: "workspace-primary",
				EnvKeys: []string{"PATH", "HOME"}, TimeoutMS: 2000, OutputCap: 1048576,
				BoundEpoch: 3, Active: true,
			}},
		}},
		{"pane_context_result", PaneContextResult{
			Pane: "pan-000001", Cwd: "/repo/src",
			GitRoot: "/repo", GitBranch: "main", GitDirty: true,
			ForegroundPID: 4242, ForegroundCmd: "nvim", ExitCode: intp(0),
			UpdatedMS: 1737000000000,
		}},
		{"workspace_tree_result", WorkspaceTreeResult{
			Workspace: "wsp-000001", Name: "main", Rev: 7,
			Focused:      "pan-000002",
			PaneOrder:    []string{"pan-000001", "pan-000002"},
			FocusHistory: []string{"pan-000001", "pan-000002"},
			Root: &TreeNode{Split: &TreeSplit{
				Orientation: OrientHorizontal, Ratio: 0.6,
				First: &TreeNode{Pane: &TreePane{
					ID: "pan-000001", Cwd: "/repo", Active: "sur-000001",
					Surfaces: []SurfaceInfo{{ID: "sur-000001", Title: "shell", Active: true}},
				}},
				Second: &TreeNode{Pane: &TreePane{
					ID: "pan-000002", Focused: true, Active: "sur-000002",
					Surfaces: []SurfaceInfo{{ID: "sur-000002", Active: true}},
				}},
			}},
		}},
	}
}

// TestProjectionGoldenVectors freezes the minor-1 wire shapes byte-for-byte
// AND proves every result round-trips through the strict decoder old clients
// use — so an accidental field rename or type change fails here, not in a
// review of the TUI lane.
func TestProjectionGoldenVectors(t *testing.T) {
	dir := "testdata"
	if *update {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range projectionGoldenCases() {
		t.Run(tc.name, func(t *testing.T) {
			got, err := json.MarshalIndent(tc.v, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, '\n')
			path := filepath.Join(dir, tc.name+".json")
			if *update {
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden %s: %v (run with -update to generate)", path, err)
			}
			if !bytes.Equal(bytes.TrimRight(want, "\n"), bytes.TrimRight(got, "\n")) {
				t.Errorf("golden mismatch for %s\n--- want ---\n%s\n--- got ---\n%s", tc.name, want, got)
			}
			// Strict round-trip: the exact bytes a daemon emits must decode
			// under DecodeStrict into the same value (client contract).
			fresh, err := json.Marshal(tc.v)
			if err != nil {
				t.Fatal(err)
			}
			target := newSameType(tc.v)
			if err := v1.DecodeStrict(fresh, target); err != nil {
				t.Fatalf("strict decode of canonical payload failed: %v", err)
			}
			re, err := json.Marshal(deref(target))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(fresh, re) {
				t.Errorf("round-trip drift:\n--- out ---\n%s\n--- back ---\n%s", fresh, re)
			}
		})
	}
}

// newSameType returns a pointer to a fresh zero value of v's dynamic type.
func newSameType(v any) any {
	switch v.(type) {
	case SurfaceCellsParams:
		return &SurfaceCellsParams{}
	case SurfaceCellsResult:
		return &SurfaceCellsResult{}
	case AttachSnapshotCells:
		return &AttachSnapshotCells{}
	case HookInspectResult:
		return &HookInspectResult{}
	case PaneContextResult:
		return &PaneContextResult{}
	case WorkspaceTreeResult:
		return &WorkspaceTreeResult{}
	default:
		panic("add the new projection type here")
	}
}

func deref(p any) any {
	switch v := p.(type) {
	case *SurfaceCellsParams:
		return *v
	case *SurfaceCellsResult:
		return *v
	case *AttachSnapshotCells:
		return *v
	case *HookInspectResult:
		return *v
	case *PaneContextResult:
		return *v
	case *WorkspaceTreeResult:
		return *v
	default:
		panic("add the new projection type here")
	}
}
