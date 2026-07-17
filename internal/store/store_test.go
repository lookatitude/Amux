package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// openStore opens a store at dir and closes it when the test ends.
func openStore(t *testing.T, dir string) *Store {
	t.Helper()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open(%q): %v", dir, err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// seedProject registers a project so grant rows satisfy the foreign key.
func seedProject(t *testing.T, s *Store, key string) {
	t.Helper()
	if err := s.UpsertProject(key, "/tmp/"+key, 1, 2, "test-v1", "birth-"+key); err != nil {
		t.Fatalf("UpsertProject(%q): %v", key, err)
	}
}

func TestOpenCreatesPrivateDirWithWAL(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	s := openStore(t, dir)

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat store dir: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("store dir mode = %o, want 700", got)
	}

	var mode string
	if err := s.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatalf("read journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
	var fk int
	if err := s.db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil {
		t.Fatalf("read foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Fatalf("foreign_keys = %d, want 1", fk)
	}
}

func TestMigrationsApplyInOrderAndReopenIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)

	assertVersions := func(s *Store) {
		t.Helper()
		rows, err := s.db.Query(`SELECT version FROM schema_migrations ORDER BY applied_unix_ms, version`)
		if err != nil {
			t.Fatalf("read schema_migrations: %v", err)
		}
		defer rows.Close()
		var got []int
		for rows.Next() {
			var v int
			if err := rows.Scan(&v); err != nil {
				t.Fatalf("scan version: %v", err)
			}
			got = append(got, v)
		}
		if len(got) != len(migrations) {
			t.Fatalf("recorded %d migrations, want %d", len(got), len(migrations))
		}
		for i, v := range got {
			// Ordered, contiguous, starting at 1 (ADR-0005: ordered,
			// forward-only migrations).
			if v != i+1 {
				t.Fatalf("migration %d recorded as version %d, want %d", i, v, i+1)
			}
		}
	}
	assertVersions(s)

	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	s2 := openStore(t, dir) // idempotent reopen: applies nothing twice
	assertVersions(s2)
}

func TestOpenRefusesNewerSchemaFailClosed(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	// Simulate a database written by a future binary.
	if _, err := s.db.Exec(
		`INSERT INTO schema_migrations (version, applied_unix_ms) VALUES (?, 0)`,
		supportedSchemaVersion()+1); err != nil {
		t.Fatalf("bump recorded version: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	_, err := Open(dir)
	if !errors.Is(err, ErrNewerSchema) {
		t.Fatalf("Open on newer schema: err = %v, want ErrNewerSchema", err)
	}
}

// TestReopenAfterKillRetainsCommittedState simulates crash durability two
// ways: (a) the specified write-close-reopen-verify cycle, and (b) copying the
// live db + WAL sidecars mid-flight — without closing — into a fresh directory
// (a filesystem snapshot of a killed process) and verifying WAL recovery
// yields every committed row (ADR-0005: a partial write must never be loaded;
// committed state must survive).
func TestReopenAfterKillRetainsCommittedState(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)

	seedProject(t, s, "p1")
	if err := s.SetProjectState("p1", ProjectStateApproved, 1); err != nil {
		t.Fatalf("SetProjectState: %v", err)
	}
	if err := s.InsertGrant(GrantRow{ID: "g1", ProjectKey: "p1", HookID: "h",
		EventsJSON: "[]", ScopeKind: "none", EnvAllowlistJSON: "[]",
		BoundEpoch: 1, Active: true}); err != nil {
		t.Fatalf("InsertGrant: %v", err)
	}
	if _, err := s.AppendAudit(AuditRow{Kind: "project_approved", ProjectKey: "p1", Epoch: 1, AtMS: 10}); err != nil {
		t.Fatalf("AppendAudit: %v", err)
	}
	if err := s.InsertNotification(NotificationRow{ID: "n1", Session: "s1", CreatedMS: 10}); err != nil {
		t.Fatalf("InsertNotification: %v", err)
	}
	if err := s.SetMeta("boot_id", "b-1"); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}
	if err := s.SetCursor("cli", "s1", 42); err != nil {
		t.Fatalf("SetCursor: %v", err)
	}

	verify := func(s *Store) {
		t.Helper()
		p, err := s.GetProject("p1")
		if err != nil || p.State != ProjectStateApproved || p.Epoch != 1 {
			t.Fatalf("GetProject = %+v, %v; want approved epoch 1", p, err)
		}
		if _, err := s.GetGrant("g1"); err != nil {
			t.Fatalf("GetGrant: %v", err)
		}
		if n, err := s.CountAudit(); err != nil || n != 1 {
			t.Fatalf("CountAudit = %d, %v; want 1", n, err)
		}
		if n, err := s.CountUnread("s1"); err != nil || n != 1 {
			t.Fatalf("CountUnread = %d, %v; want 1", n, err)
		}
		if v, err := s.GetMeta("boot_id"); err != nil || v != "b-1" {
			t.Fatalf("GetMeta = %q, %v; want b-1", v, err)
		}
		if seq, err := s.GetCursor("cli", "s1"); err != nil || seq != 42 {
			t.Fatalf("GetCursor = %d, %v; want 42", seq, err)
		}
	}

	// (b) Kill simulation: snapshot the live files without closing.
	killDir := t.TempDir()
	for _, suffix := range []string{"", "-wal", "-shm"} {
		src := s.path + suffix
		data, err := os.ReadFile(src)
		if err != nil {
			if suffix != "" && os.IsNotExist(err) {
				continue // sidecar may not exist; fine
			}
			t.Fatalf("read %s: %v", src, err)
		}
		if err := os.WriteFile(filepath.Join(killDir, filepath.Base(src)), data, 0o600); err != nil {
			t.Fatalf("copy %s: %v", src, err)
		}
	}
	killed := openStore(t, killDir)
	verify(killed)

	// (a) Graceful write-close-reopen-verify.
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	verify(openStore(t, dir))
}
