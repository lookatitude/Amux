package snapshot

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/amux-run/amux/internal/persist"
)

// faultFS is the recording/fault-injecting FS seam the B8 fault-injection tests
// require: it logs every filesystem operation (interleaved with the semantic
// CommitOrder step markers) and, once armed, "crashes" — every subsequent
// operation returns an error without touching the disk, simulating a process
// that died at that step.
type faultFS struct {
	real    FS
	ops     []string
	failing bool
	inject  error
}

func newFaultFS() *faultFS {
	return &faultFS{real: OSFS(), inject: errors.New("injected crash")}
}

func (f *faultFS) op(name string) error {
	f.ops = append(f.ops, name)
	if f.failing {
		return f.inject
	}
	return nil
}

func (f *faultFS) MkdirAll(path string, perm fs.FileMode) error {
	if err := f.op("mkdirall"); err != nil {
		return err
	}
	return f.real.MkdirAll(path, perm)
}

func (f *faultFS) WriteFile(path string, data []byte, perm fs.FileMode) error {
	if err := f.op("writefile"); err != nil {
		return err
	}
	return f.real.WriteFile(path, data, perm)
}

func (f *faultFS) Fsync(path string) error {
	if err := f.op("fsync"); err != nil {
		return err
	}
	return f.real.Fsync(path)
}

func (f *faultFS) Rename(oldpath, newpath string) error {
	if err := f.op("rename"); err != nil {
		return err
	}
	return f.real.Rename(oldpath, newpath)
}

func (f *faultFS) RemoveAll(path string) error {
	if err := f.op("removeall"); err != nil {
		return err
	}
	return f.real.RemoveAll(path)
}

func (f *faultFS) ReadFile(path string) ([]byte, error) {
	if err := f.op("readfile"); err != nil {
		return nil, err
	}
	return f.real.ReadFile(path)
}

func (f *faultFS) ReadDir(path string) ([]fs.DirEntry, error) {
	if err := f.op("readdir"); err != nil {
		return nil, err
	}
	return f.real.ReadDir(path)
}

// testComponents builds a full three-component generation payload.
func testComponents(t *testing.T) map[persist.ComponentKind][]byte {
	t.Helper()
	g, err := EncodeGraph(testGraphDoc())
	if err != nil {
		t.Fatal(err)
	}
	sc, err := EncodeSidecar([]Chunk{{Seq: 1, Data: []byte("raw pty bytes")}})
	if err != nil {
		t.Fatal(err)
	}
	return map[persist.ComponentKind][]byte{
		persist.ComponentGraph:         g,
		persist.ComponentReplaySidecar: sc,
		persist.ComponentNotifyExport:  []byte("opaque-notify-export"),
	}
}

// TestCommitStepOrderMatchesCommitOrder asserts, step by step, that Commit
// executes EXACTLY the persist.CommitOrder sequence the ADR-0005 fault-injection
// contract names — and that the filesystem operations observed inside each step
// match that step's semantics.
func TestCommitStepOrderMatchesCommitOrder(t *testing.T) {
	ffs := newFaultFS()
	var steps []string
	w := &Writer{FS: ffs, Observe: func(s string) {
		steps = append(steps, s)
		ffs.ops = append(ffs.ops, "STEP "+s)
	}}
	if _, err := w.Commit(t.TempDir(), "s1", testComponents(t)); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if !slices.Equal(steps, persist.CommitOrder) {
		t.Fatalf("step order = %v, want persist.CommitOrder = %v", steps, persist.CommitOrder)
	}

	// Slice the interleaved op log into per-step segments.
	segs := map[string][]string{}
	current := ""
	for _, op := range ffs.ops {
		if s, ok := strings.CutPrefix(op, "STEP "); ok {
			current = s
			continue
		}
		if current != "" {
			segs[current] = append(segs[current], op)
		}
	}
	want := map[string][]string{
		"write_components_temp":                 {"mkdirall", "writefile", "writefile", "writefile"},
		"fsync_components":                      {"fsync", "fsync", "fsync"},
		"checksum_components":                   nil, // pure computation, no I/O
		"write_manifest_temp_and_fsync":         {"writefile", "fsync"},
		"atomic_rename_manifest":                {"rename"},
		"fsync_generation_dir":                  {"fsync", "fsync"}, // generation dir + session dir
		"retire_older_than_previous_known_good": {"readdir"},        // nothing to retire on first commit
	}
	for step, ops := range want {
		if !slices.Equal(segs[step], ops) {
			t.Errorf("step %q performed ops %v, want %v", step, segs[step], ops)
		}
	}
}

// TestCrashAtEachCommitStep injects a crash at every CommitOrder step k. For
// every k at or before the atomic manifest rename (the commit point), the prior
// committed generation must still be what OpenLatest returns and the partial
// temp generation must be ignored; once the rename has happened (k after the
// commit point), the new generation wins even though Commit reported an error
// (ADR-0005 "a crash before step 5 leaves the prior committed generation
// authoritative").
func TestCrashAtEachCommitStep(t *testing.T) {
	renameIdx := slices.Index(persist.CommitOrder, "atomic_rename_manifest")
	if renameIdx < 0 {
		t.Fatal("persist.CommitOrder has no atomic_rename_manifest step")
	}
	for k, step := range persist.CommitOrder {
		t.Run(step, func(t *testing.T) {
			root := t.TempDir()
			m1, err := Commit(root, "s1", testComponents(t))
			if err != nil {
				t.Fatalf("baseline Commit: %v", err)
			}

			ffs := newFaultFS()
			w := &Writer{FS: ffs, Observe: func(s string) {
				if s == step {
					ffs.failing = true
				}
			}}
			m2, err2 := w.Commit(root, "s1", testComponents(t))
			if err2 == nil {
				t.Fatalf("Commit with crash at %q returned no error", step)
			}
			if m2 != nil {
				t.Fatalf("Commit with crash at %q returned a manifest", step)
			}

			loaded, report, err3 := OpenLatest(root, "s1")
			if err3 != nil {
				t.Fatalf("OpenLatest after crash at %q: %v", step, err3)
			}
			if k <= renameIdx {
				// Crash at or before the rename op: the rename never completed,
				// so the prior generation is authoritative.
				if loaded.Manifest.CheckpointID != m1.CheckpointID {
					t.Fatalf("crash at %q: loaded %s, want prior %s", step, loaded.Manifest.CheckpointID, m1.CheckpointID)
				}
				if k >= 1 {
					// The partial generation dir exists from step 1 onward and
					// must be reported as rejected, not silently absent.
					if len(report.Rejected) == 0 {
						t.Fatalf("crash at %q: partial generation not reported as rejected", step)
					}
					for _, r := range report.Rejected {
						if r.Reason == "" {
							t.Errorf("rejected generation %s has empty reason", r.Dir)
						}
					}
				}
			} else {
				// Crash after the rename: THE COMMIT POINT passed, the new
				// generation is durable and wins.
				if loaded.Manifest.CheckpointID == m1.CheckpointID {
					t.Fatalf("crash at %q (after commit point): still loading prior generation", step)
				}
				if loaded.Manifest.PrevCheckpointID != m1.CheckpointID {
					t.Errorf("crash at %q: PrevCheckpointID = %q, want %q", step, loaded.Manifest.PrevCheckpointID, m1.CheckpointID)
				}
			}
		})
	}
}

// TestRetentionKeepsCurrentAndPrevious: after three commits only the current
// and previous-known-good generations remain on disk, linked by
// PrevCheckpointID, and older generations were deleted only after the commit
// point (ADR-0005 step 7).
func TestRetentionKeepsCurrentAndPrevious(t *testing.T) {
	root := t.TempDir()
	m1, err := Commit(root, "s1", testComponents(t))
	if err != nil {
		t.Fatal(err)
	}
	m2, err := Commit(root, "s1", testComponents(t))
	if err != nil {
		t.Fatal(err)
	}
	m3, err := Commit(root, "s1", testComponents(t))
	if err != nil {
		t.Fatal(err)
	}
	if m2.PrevCheckpointID != m1.CheckpointID || m3.PrevCheckpointID != m2.CheckpointID {
		t.Errorf("PrevCheckpointID chain broken: m2.prev=%q m3.prev=%q", m2.PrevCheckpointID, m3.PrevCheckpointID)
	}

	entries, err := os.ReadDir(filepath.Join(root, "s1"))
	if err != nil {
		t.Fatal(err)
	}
	var gens []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "gen-") {
			gens = append(gens, e.Name())
		}
	}
	if len(gens) != 2 {
		t.Fatalf("after 3 commits %d generations remain (%v), want exactly 2 (current + previous)", len(gens), gens)
	}
	if !slices.Contains(gens, "gen-"+m2.CheckpointID) || !slices.Contains(gens, "gen-"+m3.CheckpointID) {
		t.Fatalf("retained %v, want current %s and previous %s", gens, m3.CheckpointID, m2.CheckpointID)
	}

	loaded, _, err := OpenLatest(root, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Manifest.CheckpointID != m3.CheckpointID {
		t.Fatalf("OpenLatest = %s, want newest %s", loaded.Manifest.CheckpointID, m3.CheckpointID)
	}
}

// TestCommitRequiresGraphComponent: a generation without the graph component
// could never be loaded; Commit fails closed up front.
func TestCommitRequiresGraphComponent(t *testing.T) {
	comps := testComponents(t)
	delete(comps, persist.ComponentGraph)
	if _, err := Commit(t.TempDir(), "s1", comps); err == nil {
		t.Fatal("Commit accepted a generation without the graph component")
	}
}
