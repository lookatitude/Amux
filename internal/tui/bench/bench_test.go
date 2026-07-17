package bench

import (
	"testing"

	"github.com/amux-run/amux/internal/tui/render"
)

const (
	benchCols = 200
	benchRows = 50
)

// BenchmarkRenderFullFrame measures the full-frame strategy: compose + emit the
// entire 8-pane scene every frame. Reports allocations and bytes written.
func BenchmarkRenderFullFrame(b *testing.B) {
	opts := render.Options{}
	b.ReportAllocs()
	var bytes int64
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, out := RenderFull(BuildScene(benchCols, benchRows, i), opts)
		bytes += int64(len(out))
	}
	b.StopTimer()
	b.ReportMetric(float64(bytes)/float64(b.N), "bytes/frame")
}

// BenchmarkRenderDamageAware measures the damage-aware strategy: only changed
// runs are emitted frame-to-frame. A steady scene (same salt) should emit near
// zero bytes; a churning scene emits proportional damage.
func BenchmarkRenderDamageAware(b *testing.B) {
	opts := render.Options{}
	b.ReportAllocs()
	var bytes int64
	prev, _ := RenderFull(BuildScene(benchCols, benchRows, 0), opts)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var out string
		prev, out = RenderDamage(prev, BuildScene(benchCols, benchRows, i%4), opts)
		bytes += int64(len(out))
	}
	b.StopTimer()
	b.ReportMetric(float64(bytes)/float64(b.N), "bytes/frame")
}

// BenchmarkFrameLatency measures a single interaction frame (compose only): the
// operation the p95 <75 ms gate governs (split/focus/resize triggers a recompose
// + render). This is the portable proxy for the Arch reference measurement.
func BenchmarkFrameLatency(b *testing.B) {
	opts := render.Options{}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sc := render.Render(benchRows, benchCols, BuildScene(benchCols, benchRows, i).Panes, opts)
		_ = sc
	}
}

// TestDamageIsSmallerOnSteadyScene is a correctness guard on the damage-aware
// strategy: an unchanged scene emits strictly fewer bytes than a full frame.
func TestDamageIsSmallerOnSteadyScene(t *testing.T) {
	opts := render.Options{}
	scene := BuildScene(120, 30, 7)
	full, fullOut := RenderFull(scene, opts)
	_, dmgOut := RenderDamage(full, scene, opts) // same scene → no damage
	if len(dmgOut) >= len(fullOut) {
		t.Fatalf("damage-aware (%d) should beat full-frame (%d) on a steady scene", len(dmgOut), len(fullOut))
	}
	if len(dmgOut) != 0 {
		t.Fatalf("no-change frame should emit 0 bytes, got %d", len(dmgOut))
	}
}

func TestSceneHasEightPanes(t *testing.T) {
	scene := BuildScene(120, 40, 0)
	if len(scene.Panes) != 8 {
		t.Fatalf("want 8 panes, got %d", len(scene.Panes))
	}
}
