package store

import "testing"

func TestAuditAppendOnlyMonotonicSeqAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)

	for i, want := range []uint64{1, 2, 3} {
		seq, err := s.AppendAudit(AuditRow{
			Kind: "project_approved", ProjectKey: "p1", Epoch: uint64(i + 1),
			AtMS: int64(100 + i),
		})
		if err != nil {
			t.Fatalf("AppendAudit #%d: %v", i, err)
		}
		if seq != want {
			t.Fatalf("AppendAudit #%d seq = %d, want %d", i, seq, want)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// The sequence continues after reopen: monotonic, never reused
	// (AUTOINCREMENT — the ADR-0005 audit ledger survives restart intact).
	s2 := openStore(t, dir)
	seq, err := s2.AppendAudit(AuditRow{
		Kind: "activation_denied", ProjectKey: "p1", Epoch: 3,
		Code: "E_HOOK_DENIED", AtMS: 200, DetailsJSON: `{"hook":"h"}`,
	})
	if err != nil {
		t.Fatalf("AppendAudit after reopen: %v", err)
	}
	if seq != 4 {
		t.Fatalf("seq after reopen = %d, want 4", seq)
	}

	n, err := s2.CountAudit()
	if err != nil || n != 4 {
		t.Fatalf("CountAudit = %d, %v; want 4", n, err)
	}
}

func TestListAuditOrderedFromSeq(t *testing.T) {
	s := openStore(t, t.TempDir())
	for i := 0; i < 5; i++ {
		if _, err := s.AppendAudit(AuditRow{Kind: "spawn", ProjectKey: "p", Epoch: 1, AtMS: int64(i)}); err != nil {
			t.Fatalf("AppendAudit: %v", err)
		}
	}

	page, err := s.ListAudit(2, 2)
	if err != nil {
		t.Fatalf("ListAudit(2,2): %v", err)
	}
	if len(page) != 2 || page[0].Seq != 2 || page[1].Seq != 3 {
		t.Fatalf("ListAudit(2,2) = %+v, want seqs [2 3]", page)
	}

	all, err := s.ListAudit(0, 0) // limit <= 0: everything
	if err != nil {
		t.Fatalf("ListAudit(0,0): %v", err)
	}
	if len(all) != 5 {
		t.Fatalf("ListAudit(0,0) returned %d rows, want 5", len(all))
	}
	for i, a := range all {
		if a.Seq != uint64(i+1) {
			t.Fatalf("row %d seq = %d, want %d (ascending order)", i, a.Seq, i+1)
		}
	}
}
