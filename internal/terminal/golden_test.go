package terminal

import (
	"flag"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// update regenerates the golden files from the current engine output:
//
//	go test ./internal/terminal/ -run TestGoldenFixtures -update
var update = flag.Bool("update", false, "rewrite golden files under testdata/terminal")

// fixtureDir holds the raw *.in inputs and *.golden rendered grids (format
// documented on RenderSnapshot).
const fixtureDir = "../../testdata/terminal"

// fixtureSetup carries per-fixture engine geometry and an optional post-feed
// step (e.g. the resize-truncation fixture).
type fixtureSetup struct {
	rows, cols int
	post       func(t *testing.T, e *Engine)
}

func (f fixtureSetup) geometry() (int, int) {
	if f.rows == 0 {
		return 6, 20
	}
	return f.rows, f.cols
}

// fixtureSetups overrides the default 6x20 geometry / no post step.
var fixtureSetups = map[string]fixtureSetup{
	"resize_trunc": {rows: 6, cols: 20, post: func(t *testing.T, e *Engine) {
		if err := e.Resize(2, 8); err != nil {
			t.Fatal(err)
		}
	}},
}

func fixtureNames(t *testing.T) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(fixtureDir, "*.in"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatalf("no fixtures under %s", fixtureDir)
	}
	names := make([]string, 0, len(matches))
	for _, m := range matches {
		names = append(names, strings.TrimSuffix(filepath.Base(m), ".in"))
	}
	return names
}

func runFixture(t *testing.T, name string, feed func(e *Engine, raw []byte)) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(fixtureDir, name+".in"))
	if err != nil {
		t.Fatal(err)
	}
	setup := fixtureSetups[name]
	rows, cols := setup.geometry()
	e := mustEngine(t, rows, cols)
	feed(e, raw)
	if setup.post != nil {
		setup.post(t, e)
	}
	checkInvariants(t, e)
	return RenderSnapshot(e.CellSnapshot())
}

// TestGoldenFixtures renders every raw fixture and compares it against its
// committed golden grid (rebuild-from-raw determinism, ADR-0005).
func TestGoldenFixtures(t *testing.T) {
	for _, name := range fixtureNames(t) {
		t.Run(name, func(t *testing.T) {
			got := runFixture(t, name, func(e *Engine, raw []byte) { e.Feed(raw) })
			goldenPath := filepath.Join(fixtureDir, name+".golden")
			if *update {
				if err := os.WriteFile(goldenPath, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("missing golden (run with -update): %v", err)
			}
			if got != string(want) {
				t.Errorf("rendered grid differs from golden.\n--- got ---\n%s\n--- want ---\n%s", got, want)
			}
		})
	}
}

// TestDifferentialReplay proves chunking independence: feeding each fixture
// all-at-once, byte-by-byte, and in seeded random-sized chunks yields
// byte-identical rendered grids — the determinism snapshot restore relies on
// (ADR-0005: the grid is rebuilt from raw bytes).
func TestDifferentialReplay(t *testing.T) {
	for _, name := range fixtureNames(t) {
		t.Run(name, func(t *testing.T) {
			whole := runFixture(t, name, func(e *Engine, raw []byte) { e.Feed(raw) })
			bytewise := runFixture(t, name, func(e *Engine, raw []byte) {
				for i := range raw {
					e.Feed(raw[i : i+1])
				}
			})
			if whole != bytewise {
				t.Errorf("byte-by-byte differs from all-at-once.\n--- bytes ---\n%s\n--- whole ---\n%s", bytewise, whole)
			}
			for seed := int64(1); seed <= 5; seed++ {
				rng := rand.New(rand.NewSource(seed))
				chunked := runFixture(t, name, func(e *Engine, raw []byte) {
					for len(raw) > 0 {
						n := min(1+rng.Intn(7), len(raw))
						e.Feed(raw[:n])
						raw = raw[n:]
					}
				})
				if whole != chunked {
					t.Errorf("seed %d chunking differs.\n--- chunked ---\n%s\n--- whole ---\n%s", seed, chunked, whole)
				}
			}
		})
	}
}

// TestRenderSnapshotFormatStable pins the documented golden footer format so
// accidental renderer drift shows up as a test failure, not a golden churn.
func TestRenderSnapshotFormatStable(t *testing.T) {
	e := mustEngine(t, 2, 5)
	e.Feed([]byte("\x1b[1;31mhi"))
	got := RenderSnapshot(e.CellSnapshot())
	want := strings.Join([]string{
		"hi",
		"",
		"--",
		"cursor: row=0 col=2 visible=true wrapnext=false",
		"pen: fg=ansi(1) bg=default attrs=bold",
		"title: ",
		"modes: altscreen=false autowrap=true region=0..1 rows=2 cols=5",
		"bell=0 unsupported=0",
		"styles:",
		"row=0 cols=0..1 fg=ansi(1) bg=default attrs=bold",
		"",
	}, "\n")
	if got != want {
		t.Errorf("format drift.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
