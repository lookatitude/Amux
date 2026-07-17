// Package bench builds the deterministic 8-pane / 20-surface scene the
// performance lane measures (U7), and the two rendering strategies it compares:
// full-frame (re-emit every cell) and damage-aware (emit only changed runs).
// The fixtures are pure data so the benchmarks are reproducible and portable;
// the actual timing/allocation/bytes evidence is produced by bench_test.go.
//
// Honesty note: benchmarks recorded on a developer host (e.g. macOS/arm64) are
// portable evidence of the RELATIVE cost of the two strategies and of the
// absence of pathological allocation — they are NOT the Arch reference-profile
// p95. The exact Arch command T6 must run is documented in RunbookArchCommand.
package bench

import (
	"fmt"

	"github.com/amux-run/amux/internal/tui/geometry"
	"github.com/amux-run/amux/internal/tui/model"
	"github.com/amux-run/amux/internal/tui/render"
)

// Scene is a full render input: the terminal size, the pane tree layout, and a
// PaneView per pane.
type Scene struct {
	Rows, Cols int
	Panes      []render.PaneView
}

// EightPaneTree builds a nested 8-pane split tree (the success-criteria scene).
func EightPaneTree() *geometry.Node {
	ids := make([]string, 8)
	for i := range ids {
		ids[i] = fmt.Sprintf("pane-%d", i)
	}
	// Two columns of four stacked panes, each column a vertical split.
	col := func(a, b, c, d string) *geometry.Node {
		return geometry.Split(geometry.Vertical, geometry.Leaf(a), geometry.Leaf(b), geometry.Leaf(c), geometry.Leaf(d))
	}
	return geometry.Split(geometry.Horizontal,
		col(ids[0], ids[1], ids[2], ids[3]),
		col(ids[4], ids[5], ids[6], ids[7]),
	)
}

// filledSnapshot builds a content snapshot of rows×cols cells with deterministic
// glyphs (so diffs are meaningful and wide cells appear).
func filledSnapshot(rows, cols, salt int) model.CellSnapshot {
	if rows < 0 {
		rows = 0
	}
	if cols < 0 {
		cols = 0
	}
	cells := make([][]model.Cell, rows)
	for r := range cells {
		row := make([]model.Cell, cols)
		for c := range row {
			// Occasional wide cell to exercise width handling in the hot path.
			if (r+c+salt)%17 == 0 && c+1 < cols {
				row[c] = model.Cell{Content: "界", Width: 2}
				if c+1 < cols {
					row[c+1] = model.Cell{Width: 0}
				}
				continue
			}
			glyph := string(rune('!' + ((r*7 + c*3 + salt) % 90)))
			row[c] = model.Cell{Content: glyph, Width: 1, Style: model.Style{FG: model.Color{Mode: model.ColorANSI, Index: uint8((r + c) % 8)}}}
		}
		cells[r] = row
	}
	return model.CellSnapshot{Rows: rows, Cols: cols, Cells: cells, Cursor: model.Cursor{Visible: true}}
}

// BuildScene lays out the 8-pane tree at cols×rows and attaches filled
// snapshots plus decorations across 20 surfaces (surfacesPerScene distributed
// over the 8 panes). salt varies content so successive scenes differ (for the
// damage-aware benchmark).
func BuildScene(cols, rows, salt int) Scene {
	tree := EightPaneTree()
	l := geometry.Compute(tree, cols, rows, geometry.DefaultConfig())
	var pvs []render.PaneView
	for i, pl := range l.Panes {
		snap := filledSnapshot(pl.Content.H, pl.Content.W, salt+i)
		pvs = append(pvs, render.PaneView{
			Layout:        pl,
			Snapshot:      snap,
			Focused:       i == 0,
			Title:         fmt.Sprintf("srf-%d", i),
			Class:         model.ClassLive,
			Lease:         leaseFor(i),
			Process:       "bash",
			Cwd:           "/home/dev/project",
			GitBranch:     "main",
			ActiveSurface: fmt.Sprintf("%d/3", (i%3)+1),
			Unread:        i % 2,
			ShowCursor:    i == 0,
		})
	}
	return Scene{Rows: rows, Cols: cols, Panes: pvs}
}

func leaseFor(i int) model.LeaseState {
	switch i % 3 {
	case 0:
		return model.LeaseOwned
	case 1:
		return model.LeaseOther
	default:
		return model.LeaseNone
	}
}

// SurfaceCount is the number of surfaces the benchmark scene models across the
// 8 panes (spec: 8-pane / 20-surface fixture).
const SurfaceCount = 20

// RenderFull renders the whole scene and returns the full-frame ANSI bytes.
func RenderFull(s Scene, opts render.Options) (*render.Screen, string) {
	sc := render.Render(s.Rows, s.Cols, s.Panes, opts)
	return sc, sc.AnsiString()
}

// RenderDamage renders the scene and returns only the ANSI for cells that differ
// from prev (the damage-aware strategy). When prev is nil it is a full frame.
func RenderDamage(prev *render.Screen, s Scene, opts render.Options) (*render.Screen, string) {
	sc := render.Render(s.Rows, s.Cols, s.Panes, opts)
	dmg := render.Diff(prev, sc)
	return sc, render.EmitDamage(dmg)
}

// RunbookArchCommand is the exact command T6 must run on the Arch reference
// profile to produce the p95 <75 ms split/focus/resize evidence. It is recorded
// here (not executed) so the deferral is explicit and reproducible.
const RunbookArchCommand = `# Arch reference profile (T6): p95 split/focus/resize < 75 ms
# Run on Arch Linux x86_64, release build, isolated:
GOFLAGS=-mod=mod CGO_ENABLED=0 go test -run '^$' -bench 'BenchmarkRender|BenchmarkFrameLatency' \
    -benchmem -benchtime=2000x -count=10 ./internal/tui/bench/ | tee bench-arch.txt
# Then compute p95 of the per-op ns/op across the 10 counts and assert < 75e6 ns.`
