package snapshot

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amux-run/amux/internal/persist"
)

// dirDigest hashes every file under dir so tests can assert a refusal touched
// nothing on disk.
func dirDigest(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		rel, _ := filepath.Rel(dir, path)
		out[rel] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// TestOpenLatestRoundtrip: the happy path returns the decoded GraphDoc plus
// the sidecar chunks and opaque notify export, with a report naming the loaded
// generation.
func TestOpenLatestRoundtrip(t *testing.T) {
	root := t.TempDir()
	comps := testComponents(t)
	m, err := Commit(root, "s1", comps)
	if err != nil {
		t.Fatal(err)
	}
	loaded, report, err := OpenLatest(root, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Manifest.CheckpointID != m.CheckpointID {
		t.Fatalf("loaded %s, want %s", loaded.Manifest.CheckpointID, m.CheckpointID)
	}
	if report.LoadedCheckpointID != m.CheckpointID || len(report.Rejected) != 0 {
		t.Errorf("report = %+v, want loaded %s with no rejections", report, m.CheckpointID)
	}
	if loaded.Graph == nil || loaded.Graph.EventCursor != 42 {
		t.Errorf("graph doc not decoded: %+v", loaded.Graph)
	}
	chunks, err := loaded.ReplayChunks()
	if err != nil || len(chunks) != 1 || !bytes.Equal(chunks[0].Data, []byte("raw pty bytes")) {
		t.Errorf("ReplayChunks = %v, %v", chunks, err)
	}
	notify, ok := loaded.NotifyExport()
	if !ok || !bytes.Equal(notify, []byte("opaque-notify-export")) {
		t.Errorf("NotifyExport = %q, %v", notify, ok)
	}
}

// TestOpenLatestCorruptComponentFallsBack: flipping one byte of a committed
// component makes its generation fail checksum validation; the generation is
// SKIPPED with a diagnostic and the previous known-good generation loads
// (ADR-0005 "reject partial/corrupt generations; keep prior known-good").
func TestOpenLatestCorruptComponentFallsBack(t *testing.T) {
	root := t.TempDir()
	m1, err := Commit(root, "s1", testComponents(t))
	if err != nil {
		t.Fatal(err)
	}
	m2, err := Commit(root, "s1", testComponents(t))
	if err != nil {
		t.Fatal(err)
	}

	// Corrupt one byte of the newest generation's graph component.
	gpath := filepath.Join(root, "s1", "gen-"+m2.CheckpointID, ComponentFileName(persist.ComponentGraph))
	b, err := os.ReadFile(gpath)
	if err != nil {
		t.Fatal(err)
	}
	b[len(b)/2] ^= 0xFF
	if err := os.WriteFile(gpath, b, 0o600); err != nil {
		t.Fatal(err)
	}

	loaded, report, err := OpenLatest(root, "s1")
	if err != nil {
		t.Fatalf("OpenLatest: %v", err)
	}
	if loaded.Manifest.CheckpointID != m1.CheckpointID {
		t.Fatalf("loaded %s, want previous known-good %s", loaded.Manifest.CheckpointID, m1.CheckpointID)
	}
	if len(report.Rejected) != 1 {
		t.Fatalf("report.Rejected = %+v, want exactly the corrupt generation", report.Rejected)
	}
	r := report.Rejected[0]
	if r.Dir != "gen-"+m2.CheckpointID || !strings.Contains(r.Reason, "sha256") {
		t.Errorf("rejection diagnostic %+v does not name the corrupt generation and checksum cause", r)
	}
}

// TestOpenLatestSizeMismatchFallsBack: a truncated component (size differs from
// the manifest) is rejected the same way.
func TestOpenLatestSizeMismatchFallsBack(t *testing.T) {
	root := t.TempDir()
	m1, err := Commit(root, "s1", testComponents(t))
	if err != nil {
		t.Fatal(err)
	}
	m2, err := Commit(root, "s1", testComponents(t))
	if err != nil {
		t.Fatal(err)
	}
	spath := filepath.Join(root, "s1", "gen-"+m2.CheckpointID, ComponentFileName(persist.ComponentReplaySidecar))
	b, err := os.ReadFile(spath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(spath, b[:len(b)-1], 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, report, err := OpenLatest(root, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Manifest.CheckpointID != m1.CheckpointID {
		t.Fatalf("loaded %s, want previous known-good %s", loaded.Manifest.CheckpointID, m1.CheckpointID)
	}
	if len(report.Rejected) != 1 || !strings.Contains(report.Rejected[0].Reason, "size") {
		t.Errorf("report = %+v, want a size-mismatch rejection", report.Rejected)
	}
}

// TestOpenLatestNewerGraphSchemaRefusesUntouched: a newest generation whose
// graph component carries a NEWER schema version yields the typed
// ErrNewerSchema refusal — Amux never migrates backward and never silently
// falls back to older state a newer build superseded — and the refusal touches
// nothing on disk (ADR-0005 "refuse newer").
func TestOpenLatestNewerGraphSchemaRefusesUntouched(t *testing.T) {
	root := t.TempDir()
	if _, err := Commit(root, "s1", testComponents(t)); err != nil {
		t.Fatal(err)
	}
	comps := testComponents(t)
	comps[persist.ComponentGraph] = []byte(`{"schema_version":2}`)
	if _, err := Commit(root, "s1", comps); err != nil {
		t.Fatal(err)
	}

	before := dirDigest(t, filepath.Join(root, "s1"))
	loaded, _, err := OpenLatest(root, "s1")
	if !errors.Is(err, ErrNewerSchema) {
		t.Fatalf("err = %v, want ErrNewerSchema", err)
	}
	if loaded != nil {
		t.Fatal("refusal returned a partial load")
	}
	after := dirDigest(t, filepath.Join(root, "s1"))
	if !mapsEqual(before, after) {
		t.Fatalf("refusal modified disk state:\nbefore=%v\nafter=%v", before, after)
	}
}

// TestOpenLatestNewerManifestSchemaRefuses: the manifest itself is also a
// durable schema; a newer manifest schema is refused, not skipped.
func TestOpenLatestNewerManifestSchemaRefuses(t *testing.T) {
	root := t.TempDir()
	if _, err := Commit(root, "s1", testComponents(t)); err != nil {
		t.Fatal(err)
	}
	// "z" sorts after every UUIDv7 hex digit, so this crafted generation is
	// scanned first.
	gdir := filepath.Join(root, "s1", "gen-zzzz")
	if err := os.MkdirAll(gdir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gdir, ManifestName), []byte(`{"schema_version":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := OpenLatest(root, "s1"); !errors.Is(err, ErrNewerSchema) {
		t.Fatalf("err = %v, want ErrNewerSchema", err)
	}
}

// TestOpenLatestV0MigrationFailureFallsBack: a generation whose graph document
// is legacy schema 0 but fails the 0->1 migration is rejected with a diagnostic
// and the previous known-good generation loads — migration failure preserves
// and reports previous-known-good, committing no partial load (ADR-0005).
func TestOpenLatestV0MigrationFailureFallsBack(t *testing.T) {
	root := t.TempDir()
	m1, err := Commit(root, "s1", testComponents(t))
	if err != nil {
		t.Fatal(err)
	}
	comps := testComponents(t)
	comps[persist.ComponentGraph] = []byte(`{"schema_version":0,"graph":{"session":"s1"},"surfaces":[{"surface":"srf-1","env_allowlist":["SECRET=x"]}],"event_cursor":0}`)
	if _, err := Commit(root, "s1", comps); err != nil {
		t.Fatal(err)
	}

	loaded, report, err := OpenLatest(root, "s1")
	if err != nil {
		t.Fatalf("OpenLatest: %v", err)
	}
	if loaded.Manifest.CheckpointID != m1.CheckpointID {
		t.Fatalf("loaded %s, want previous known-good %s", loaded.Manifest.CheckpointID, m1.CheckpointID)
	}
	if len(report.Rejected) != 1 || !strings.Contains(report.Rejected[0].Reason, "0->1") {
		t.Errorf("report = %+v, want a migration-failure rejection", report.Rejected)
	}
}

// TestOpenLatestV0GenerationMigratesInMemory: a valid legacy schema-0 graph
// loads by migrating forward one step in memory; the on-disk bytes stay v0
// (the migration seam rewrites nothing until the next Commit).
func TestOpenLatestV0GenerationMigratesInMemory(t *testing.T) {
	root := t.TempDir()
	comps := testComponents(t)
	v0 := []byte(`{"schema_version":0,"graph":{"session":"s1","rev":3},"surfaces":[],"event_cursor":9,"replay_per_surface_bytes":2048}`)
	comps[persist.ComponentGraph] = v0
	if _, err := Commit(root, "s1", comps); err != nil {
		t.Fatal(err)
	}
	before := dirDigest(t, filepath.Join(root, "s1"))
	loaded, _, err := OpenLatest(root, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Graph.SchemaVersion != GraphSchemaVersion || loaded.Graph.ReplayConfig.PerSurfaceBytes != 2048 {
		t.Fatalf("v0 not migrated in memory: %+v", loaded.Graph)
	}
	after := dirDigest(t, filepath.Join(root, "s1"))
	if !mapsEqual(before, after) {
		t.Fatal("in-memory migration modified disk state")
	}
}

// TestOpenLatestNoValidGeneration: when nothing validates the reader returns
// the typed ErrNoValidGeneration refusal (never a partial load) and the report
// carries the per-generation diagnostics.
func TestOpenLatestNoValidGeneration(t *testing.T) {
	// Missing session dir entirely.
	if _, _, err := OpenLatest(t.TempDir(), "nope"); !errors.Is(err, ErrNoValidGeneration) {
		t.Fatalf("missing session: err = %v, want ErrNoValidGeneration", err)
	}

	// A session whose only generation is corrupt.
	root := t.TempDir()
	m, err := Commit(root, "s1", testComponents(t))
	if err != nil {
		t.Fatal(err)
	}
	mpath := filepath.Join(root, "s1", "gen-"+m.CheckpointID, ManifestName)
	if err := os.WriteFile(mpath, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, report, err := OpenLatest(root, "s1")
	if !errors.Is(err, ErrNoValidGeneration) {
		t.Fatalf("err = %v, want ErrNoValidGeneration", err)
	}
	if loaded != nil {
		t.Fatal("refusal returned a partial load")
	}
	if len(report.Rejected) != 1 || report.Rejected[0].Reason == "" {
		t.Errorf("report = %+v, want one diagnostic rejection", report.Rejected)
	}
}
