package store

import (
	"errors"
	"fmt"
	"testing"
)

// dumpTrust serializes every projects/grants/audit row so tests can prove an
// import attempt left trust state bit-identical (ADR-0005: a snapshot restore
// can never restore, decrease, or reactivate security state).
func dumpTrust(t *testing.T, s *Store) string {
	t.Helper()
	var out string
	for _, q := range []string{
		`SELECT key, realpath, dev, ino, state, epoch FROM projects ORDER BY key`,
		`SELECT id, project_key, hook_id, exec_path, exec_sha256, config_sha256,
			events_json, scope_kind, fixed_path, env_allowlist_json, timeout_ms,
			output_cap_bytes, bound_epoch, active FROM grants ORDER BY id`,
		`SELECT seq, kind, project_key, epoch, code, at_ms, details_json FROM audit ORDER BY seq`,
	} {
		rows, err := s.db.Query(q)
		if err != nil {
			t.Fatalf("dump query: %v", err)
		}
		cols, err := rows.Columns()
		if err != nil {
			t.Fatalf("dump columns: %v", err)
		}
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatalf("dump scan: %v", err)
			}
			out += fmt.Sprintf("%#v\n", vals)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("dump rows: %v", err)
		}
		rows.Close()
	}
	return out
}

func seedTrustAndNotifications(t *testing.T, s *Store) {
	t.Helper()
	seedProject(t, s, "p1")
	if err := s.SetProjectState("p1", ProjectStateApproved, 5); err != nil {
		t.Fatalf("SetProjectState: %v", err)
	}
	if err := s.InsertGrant(sampleGrant("g1", "p1", true)); err != nil {
		t.Fatalf("InsertGrant: %v", err)
	}
	if _, err := s.AppendAudit(AuditRow{Kind: "grant_approved", ProjectKey: "p1", Epoch: 5, AtMS: 50}); err != nil {
		t.Fatalf("AppendAudit: %v", err)
	}
	seedNotifications(t, s)
	if err := s.MarkRead("n1", 500); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
}

func TestExportImportRoundtripRestoresReadState(t *testing.T) {
	s := openStore(t, t.TempDir())
	seedTrustAndNotifications(t, s)

	data, err := s.ExportNotifications("cp-1")
	if err != nil {
		t.Fatalf("ExportNotifications: %v", err)
	}
	// The committed checkpoint id is recorded in metadata.
	if v, err := s.GetMeta(MetaNotifyCheckpoint); err != nil || v != "cp-1" {
		t.Fatalf("recorded checkpoint = %q, %v; want cp-1", v, err)
	}

	// Diverge after the export: extra row, extra reads.
	if err := s.InsertNotification(NotificationRow{ID: "n5", Session: "s1", CreatedMS: 900}); err != nil {
		t.Fatalf("InsertNotification: %v", err)
	}
	if _, err := s.MarkAllRead("s1", 901); err != nil {
		t.Fatalf("MarkAllRead: %v", err)
	}

	if err := s.ImportNotifications("cp-1", data); err != nil {
		t.Fatalf("ImportNotifications: %v", err)
	}

	// Exactly the exported state: n1 read at 500, n2/n3 unread, n5 gone.
	list, err := s.ListNotifications("s1", false, 0)
	if err != nil {
		t.Fatalf("ListNotifications: %v", err)
	}
	if len(list) != 3 || list[0].ID != "n3" || list[2].ID != "n1" {
		t.Fatalf("restored list = %+v, want [n3 n2 n1]", list)
	}
	if list[2].ReadAtMS != 500 || list[0].ReadAtMS != 0 {
		t.Fatalf("restored read state wrong: %+v", list)
	}
	if n, _ := s.CountUnread("s2"); n != 1 {
		t.Fatalf("CountUnread(s2) = %d, want 1", n)
	}
}

func TestImportRefusesCheckpointMismatch(t *testing.T) {
	s := openStore(t, t.TempDir())
	seedNotifications(t, s)

	// No export ever committed: nothing may be imported.
	fresh := []byte(`{"version":1,"checkpoint_id":"cp-x","notifications":[]}`)
	if err := s.ImportNotifications("cp-x", fresh); !errors.Is(err, ErrCheckpointMismatch) {
		t.Fatalf("import with no committed checkpoint: err = %v, want ErrCheckpointMismatch", err)
	}

	data, err := s.ExportNotifications("cp-1")
	if err != nil {
		t.Fatalf("ExportNotifications: %v", err)
	}

	// Requested id disagrees with the document's embedded id.
	if err := s.ImportNotifications("cp-2", data); !errors.Is(err, ErrCheckpointMismatch) {
		t.Fatalf("requested/document mismatch: err = %v, want ErrCheckpointMismatch", err)
	}

	// Document and request agree with each other but not with the committed
	// checkpoint (a stale or foreign snapshot).
	forged := []byte(`{"version":1,"checkpoint_id":"cp-2","notifications":[]}`)
	if err := s.ImportNotifications("cp-2", forged); !errors.Is(err, ErrCheckpointMismatch) {
		t.Fatalf("stale snapshot: err = %v, want ErrCheckpointMismatch", err)
	}

	// Refused imports leave notifications untouched.
	if n, err := s.CountUnread("s1"); err != nil || n != 3 {
		t.Fatalf("notifications mutated by refused import: %d, %v", n, err)
	}
}

func TestImportRejectsForgedTrustFieldsAndLeavesTrustBitIdentical(t *testing.T) {
	s := openStore(t, t.TempDir())
	seedTrustAndNotifications(t, s)
	if _, err := s.ExportNotifications("cp-1"); err != nil {
		t.Fatalf("ExportNotifications: %v", err)
	}

	before := dumpTrust(t, s)

	// A crafted export carrying trust claims: top-level epoch/grant/audit
	// fields and a per-notification epoch. Strict decoding rejects every
	// unknown field before any write (ADR-0005: a snapshot can never
	// restore, decrease, or reactivate security state).
	forgeries := [][]byte{
		[]byte(`{"version":1,"checkpoint_id":"cp-1","notifications":[],
			"projects":[{"key":"p1","state":"approved","epoch":1}]}`),
		[]byte(`{"version":1,"checkpoint_id":"cp-1","notifications":[],
			"grants":[{"id":"g1","active":true}]}`),
		[]byte(`{"version":1,"checkpoint_id":"cp-1","notifications":[],"audit":[]}`),
		[]byte(`{"version":1,"checkpoint_id":"cp-1","notifications":[
			{"id":"n9","session":"s1","workspace":"","pane":"","kind":"","title":"","body":"","created_ms":1,"read_at_ms":0,"epoch":999}]}`),
		[]byte(`{"version":2,"checkpoint_id":"cp-1","notifications":[]}`),
		[]byte(`not json`),
	}
	for i, payload := range forgeries {
		if err := s.ImportNotifications("cp-1", payload); !errors.Is(err, ErrImportInvalid) {
			t.Fatalf("forgery %d: err = %v, want ErrImportInvalid", i, err)
		}
	}

	after := dumpTrust(t, s)
	if before != after {
		t.Fatalf("trust state changed across import attempts:\nbefore:\n%s\nafter:\n%s", before, after)
	}

	// A grant deactivated before a valid import stays inactive: the import
	// path has no reach into grants.
	if _, err := s.DeactivateGrantsForProject("p1"); err != nil {
		t.Fatalf("DeactivateGrantsForProject: %v", err)
	}
	data, err := s.ExportNotifications("cp-2")
	if err != nil {
		t.Fatalf("re-export: %v", err)
	}
	if err := s.ImportNotifications("cp-2", data); err != nil {
		t.Fatalf("valid import: %v", err)
	}
	g, err := s.GetGrant("g1")
	if err != nil || g.Active {
		t.Fatalf("import reactivated grant: %+v, %v", g, err)
	}
}
