package store

import (
	"errors"
	"fmt"
	"testing"
)

// approveP1 moves p1 to approved/1 so the tests below exercise a real
// follow-on transition (revoke epoch 2) instead of the first bump.
func approveP1(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.ApplyTrustTransition(TrustTransitionRow{
		Key: "p1", State: ProjectStateApproved, Epoch: 1,
		ValidationScheme: "test-v1", ValidationValue: "birth-p1",
		Audit: &AuditRow{Kind: "project_approved", ProjectKey: "p1", Epoch: 1, AtMS: 1_000},
	}); err != nil {
		t.Fatalf("approve transition: %v", err)
	}
}

// revocationRow is the canonical operator-revoke transition used across these
// tests: epoch 2, both active grants deactivated, one main record plus one
// grant_inactive record per deactivated grant.
func revocationRow() TrustTransitionRow {
	return TrustTransitionRow{
		Key: "p1", State: ProjectStateRevoked, Epoch: 2,
		ValidationScheme: "test-v1", ValidationValue: "birth-p1",
		DeactivateGrants: true,
		Audit:            &AuditRow{Kind: "project_revoked", ProjectKey: "p1", Epoch: 2, AtMS: 2_000},
		GrantAudit:       &AuditRow{Kind: "grant_inactive", ProjectKey: "p1", Epoch: 2, AtMS: 2_000},
	}
}

func seedRevocationFixture(t *testing.T, s *Store) {
	t.Helper()
	seedProject(t, s, "p1")
	seedProject(t, s, "p2")
	approveP1(t, s)
	for _, g := range []GrantRow{
		sampleGrant("g1", "p1", true),
		sampleGrant("g2", "p1", true),
		sampleGrant("g3", "p1", false), // pre-existing inactive history
		sampleGrant("g4", "p2", true),  // other project untouched
	} {
		if err := s.InsertGrant(g); err != nil {
			t.Fatalf("InsertGrant(%s): %v", g.ID, err)
		}
	}
}

// The primitive commits project state + epoch + discriminator, grant
// deactivation, the main audit record, and one grant_inactive record per
// deactivated grant as ONE durable unit, returning the exact deactivated
// grant IDs only after commit (G-lane F1).
func TestApplyTrustTransitionCommitsAtomically(t *testing.T) {
	s := openStore(t, t.TempDir())
	seedRevocationFixture(t, s)

	row := revocationRow()
	row.ValidationScheme, row.ValidationValue = "test-v1", "birth-replaced"
	ids, err := s.ApplyTrustTransition(row)
	if err != nil {
		t.Fatalf("ApplyTrustTransition: %v", err)
	}
	if len(ids) != 2 || ids[0] != "g1" || ids[1] != "g2" {
		t.Fatalf("deactivated IDs = %v, want [g1 g2]", ids)
	}

	p, err := s.GetProject("p1")
	if err != nil || p.State != ProjectStateRevoked || p.Epoch != 2 {
		t.Fatalf("project after transition = %+v, %v (want revoked/2)", p, err)
	}
	if p.ValidationScheme != "test-v1" || p.ValidationValue != "birth-replaced" {
		t.Fatalf("discriminator not committed with the transition: %+v", p)
	}
	active, err := s.ListGrants("p1", false)
	if err != nil || len(active) != 0 {
		t.Fatalf("active grants after revocation = %+v, %v", active, err)
	}
	all, _ := s.ListGrants("p1", true)
	if len(all) != 3 {
		t.Fatalf("grant history shrank: %d rows, want 3", len(all))
	}
	other, err := s.ListGrants("p2", false)
	if err != nil || len(other) != 1 || !other[0].Active {
		t.Fatalf("other project's grants affected: %+v, %v", other, err)
	}

	// Exact audit ordering and count: approve, then project_revoked, then one
	// grant_inactive per deactivated grant — nothing else, in sequence order.
	audit, err := s.ListAudit(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	wantKinds := []string{"project_approved", "project_revoked", "grant_inactive", "grant_inactive"}
	if len(audit) != len(wantKinds) {
		t.Fatalf("audit rows = %+v, want kinds %v", audit, wantKinds)
	}
	for i, r := range audit {
		if r.Kind != wantKinds[i] {
			t.Fatalf("audit[%d].Kind = %s, want %s (full trail %+v)", i, r.Kind, wantKinds[i], audit)
		}
		if i > 0 && r.Epoch != 2 {
			t.Fatalf("audit[%d].Epoch = %d, want 2", i, r.Epoch)
		}
	}
}

func TestApplyTrustTransitionTypedFailures(t *testing.T) {
	s := openStore(t, t.TempDir())
	seedProject(t, s, "p1")
	approveP1(t, s)

	if _, err := s.ApplyTrustTransition(TrustTransitionRow{
		Key: "missing", State: ProjectStateApproved, Epoch: 1,
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown project: err = %v, want ErrNotFound", err)
	}
	if _, err := s.ApplyTrustTransition(TrustTransitionRow{
		Key: "p1", State: "trusted-forever", Epoch: 2,
	}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("invalid state: err = %v, want ErrInvalidState", err)
	}
	for _, epoch := range []uint64{0, 1} {
		if _, err := s.ApplyTrustTransition(TrustTransitionRow{
			Key: "p1", State: ProjectStateRevoked, Epoch: epoch,
		}); !errors.Is(err, ErrEpochNotMonotonic) {
			t.Fatalf("epoch %d: err = %v, want ErrEpochNotMonotonic", epoch, err)
		}
	}
	p, err := s.GetProject("p1")
	if err != nil || p.State != ProjectStateApproved || p.Epoch != 1 {
		t.Fatalf("refused transition mutated state: %+v, %v", p, err)
	}
}

// Deterministic failure injection at EVERY transaction stage: whichever
// statement fails, the whole transition rolls back — old state and epoch,
// grants still active, discriminator untouched, zero partial audit — and a
// retry with the same epoch succeeds without ErrEpochNotMonotonic. The
// committed result then survives a store reopen (restart rehydration).
func TestApplyTrustTransitionRollsBackAtEveryStage(t *testing.T) {
	stages := []string{
		"project-update", "deactivate-grants", "audit-main", "audit-grant", "commit",
	}
	for _, stage := range stages {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir()
			s := openStore(t, dir)
			seedRevocationFixture(t, s)
			auditBefore, err := s.CountAudit()
			if err != nil {
				t.Fatal(err)
			}

			injected := errors.New("injected failure")
			fired := false
			s.SetTrustTransitionFailpoint(func(got string) error {
				if got == stage {
					fired = true
					return injected
				}
				return nil
			})
			if _, err := s.ApplyTrustTransition(revocationRow()); !errors.Is(err, injected) {
				t.Fatalf("stage %s: err = %v, want injected failure", stage, err)
			}
			if !fired {
				t.Fatalf("stage %s: failpoint never fired — stage label drifted", stage)
			}
			s.SetTrustTransitionFailpoint(nil)

			// Rollback: disk is byte-for-byte at the old approved transition.
			p, err := s.GetProject("p1")
			if err != nil || p.State != ProjectStateApproved || p.Epoch != 1 {
				t.Fatalf("stage %s: failed transition mutated project: %+v, %v", stage, p, err)
			}
			if p.ValidationScheme != "test-v1" || p.ValidationValue != "birth-p1" {
				t.Fatalf("stage %s: failed transition mutated discriminator: %+v", stage, p)
			}
			active, err := s.ListGrants("p1", false)
			if err != nil || len(active) != 2 {
				t.Fatalf("stage %s: grants after failed transition = %+v, %v (want 2 active)", stage, active, err)
			}
			if n, _ := s.CountAudit(); n != auditBefore {
				t.Fatalf("stage %s: partial audit persisted: %d rows, want %d", stage, n, auditBefore)
			}

			// Retry with the SAME epoch: the failed attempt must not have
			// consumed it (no ErrEpochNotMonotonic on retry).
			ids, err := s.ApplyTrustTransition(revocationRow())
			if err != nil {
				t.Fatalf("stage %s: retry failed: %v", stage, err)
			}
			if len(ids) != 2 {
				t.Fatalf("stage %s: retry deactivated %v, want [g1 g2]", stage, ids)
			}
			if n, _ := s.CountAudit(); n != auditBefore+3 {
				t.Fatalf("stage %s: audit after retry = %d rows, want %d", stage, n, auditBefore+3)
			}

			// Restart rehydration: reopen the database and observe exactly the
			// committed transition — never the rolled-back one twice.
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s2 := openStore(t, dir)
			p, err = s2.GetProject("p1")
			if err != nil || p.State != ProjectStateRevoked || p.Epoch != 2 {
				t.Fatalf("stage %s: reopened state = %+v, %v (want revoked/2)", stage, p, err)
			}
			active, _ = s2.ListGrants("p1", false)
			if len(active) != 0 {
				t.Fatalf("stage %s: reopened active grants = %+v, want none", stage, active)
			}
			if n, _ := s2.CountAudit(); n != auditBefore+3 {
				t.Fatalf("stage %s: reopened audit = %d rows, want %d", stage, n, auditBefore+3)
			}
		})
	}
}

// A transition without grant deactivation (approve/deny) appends exactly its
// main record; an unaudited transition (deny path) appends nothing.
func TestApplyTrustTransitionAuditShapes(t *testing.T) {
	s := openStore(t, t.TempDir())
	seedProject(t, s, "p1")
	if err := s.InsertGrant(sampleGrant("g1", "p1", true)); err != nil {
		t.Fatal(err)
	}

	// Audited approve: no deactivation, one main record, no grant records.
	approveP1(t, s)
	if ids, err := s.ApplyTrustTransition(TrustTransitionRow{
		Key: "p1", State: ProjectStateApproved, Epoch: 2,
		ValidationScheme: "test-v1", ValidationValue: "birth-p1",
		Audit: &AuditRow{Kind: "project_approved", ProjectKey: "p1", Epoch: 2, AtMS: 2_000},
	}); err != nil || len(ids) != 0 {
		t.Fatalf("approve transition: ids=%v err=%v", ids, err)
	}
	if g, err := s.GetGrant("g1"); err != nil || !g.Active {
		t.Fatalf("approve transition touched grants: %+v, %v", g, err)
	}

	// Unaudited deny: state moves, nothing lands in audit.
	before, _ := s.CountAudit()
	if _, err := s.ApplyTrustTransition(TrustTransitionRow{
		Key: "p1", State: ProjectStateDenied, Epoch: 3,
		ValidationScheme: "test-v1", ValidationValue: "birth-p1",
	}); err != nil {
		t.Fatal(err)
	}
	if after, _ := s.CountAudit(); after != before {
		t.Fatalf("unaudited transition appended audit: %d -> %d", before, after)
	}
	p, _ := s.GetProject("p1")
	if p.State != ProjectStateDenied || p.Epoch != 3 {
		t.Fatalf("deny transition = %+v, want denied/3", p)
	}
}

// The failpoint is test scaffolding: with none installed the stages are
// invisible; sanity-pin the stage labels so injection tests cannot silently
// stop covering a stage after a refactor.
func TestApplyTrustTransitionStageLabels(t *testing.T) {
	s := openStore(t, t.TempDir())
	seedRevocationFixture(t, s)
	var seen []string
	s.SetTrustTransitionFailpoint(func(stage string) error {
		seen = append(seen, stage)
		return nil
	})
	if _, err := s.ApplyTrustTransition(revocationRow()); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("%v", []string{
		"project-update", "deactivate-grants", "audit-main", "audit-grant", "audit-grant", "commit",
	})
	if got := fmt.Sprintf("%v", seen); got != want {
		t.Fatalf("stages fired = %v, want %v", got, want)
	}
}
