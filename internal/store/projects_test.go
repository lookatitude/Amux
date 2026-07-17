package store

import (
	"errors"
	"testing"
)

func TestUpsertProjectPreservesTrustState(t *testing.T) {
	s := openStore(t, t.TempDir())

	if err := s.UpsertProject("p1", "/repo/a", 10, 20, "test-v1", "birth-a"); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	p, err := s.GetProject("p1")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if p.Realpath != "/repo/a" || p.Dev != 10 || p.Ino != 20 {
		t.Fatalf("identity = %+v, want /repo/a 10 20", p)
	}
	if p.ValidationScheme != "test-v1" || p.ValidationValue != "birth-a" {
		t.Fatalf("validation discriminator not persisted: %+v", p)
	}
	if p.State != ProjectStateRegistered || p.Epoch != 0 {
		t.Fatalf("fresh project = %s/%d, want registered/0 (registration confers nothing)", p.State, p.Epoch)
	}

	if err := s.SetProjectState("p1", ProjectStateApproved, 3); err != nil {
		t.Fatalf("SetProjectState: %v", err)
	}
	// Re-registration refreshes filesystem identity but must not touch trust.
	if err := s.UpsertProject("p1", "/repo/moved", 11, 21, "test-v1", "birth-b"); err != nil {
		t.Fatalf("re-UpsertProject: %v", err)
	}
	p, err = s.GetProject("p1")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if p.Realpath != "/repo/moved" || p.Dev != 11 || p.Ino != 21 {
		t.Fatalf("identity not refreshed: %+v", p)
	}
	if p.State != ProjectStateApproved || p.Epoch != 3 {
		t.Fatalf("upsert mutated trust: %s/%d, want approved/3", p.State, p.Epoch)
	}
}

func TestUpdateProjectValidation(t *testing.T) {
	s := openStore(t, t.TempDir())
	seedProject(t, s, "p1")
	if err := s.SetProjectState("p1", ProjectStateApproved, 2); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateProjectValidation("p1", "test-v1", "birth-c"); err != nil {
		t.Fatalf("UpdateProjectValidation: %v", err)
	}
	p, err := s.GetProject("p1")
	if err != nil {
		t.Fatal(err)
	}
	if p.ValidationScheme != "test-v1" || p.ValidationValue != "birth-c" {
		t.Fatalf("validation not updated: %+v", p)
	}
	if p.State != ProjectStateApproved || p.Epoch != 2 {
		t.Fatalf("validation update mutated trust: %+v", p)
	}
	if err := s.UpdateProjectValidation("missing", "x", "y"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown project: err = %v, want ErrNotFound", err)
	}
}

func TestSetProjectStateRefusesNonMonotonicEpoch(t *testing.T) {
	s := openStore(t, t.TempDir())
	seedProject(t, s, "p1")

	if err := s.SetProjectState("p1", ProjectStateApproved, 1); err != nil {
		t.Fatalf("approve epoch 1: %v", err)
	}
	// Epochs never decrease (ADR-0005): equal and lower epochs are refused
	// with the typed error, and the stored state is untouched.
	for _, epoch := range []uint64{0, 1} {
		err := s.SetProjectState("p1", ProjectStateRevoked, epoch)
		if !errors.Is(err, ErrEpochNotMonotonic) {
			t.Fatalf("epoch %d: err = %v, want ErrEpochNotMonotonic", epoch, err)
		}
	}
	p, err := s.GetProject("p1")
	if err != nil || p.State != ProjectStateApproved || p.Epoch != 1 {
		t.Fatalf("refused write mutated state: %+v, %v", p, err)
	}

	if err := s.SetProjectState("p1", ProjectStateRevoked, 2); err != nil {
		t.Fatalf("revoke epoch 2: %v", err)
	}
	p, _ = s.GetProject("p1")
	if p.State != ProjectStateRevoked || p.Epoch != 2 {
		t.Fatalf("after revoke: %s/%d, want revoked/2", p.State, p.Epoch)
	}
}

func TestSetProjectStateTypedFailures(t *testing.T) {
	s := openStore(t, t.TempDir())
	seedProject(t, s, "p1")

	if err := s.SetProjectState("missing", ProjectStateApproved, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown project: err = %v, want ErrNotFound", err)
	}
	if err := s.SetProjectState("p1", "trusted-forever", 1); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("invalid state: err = %v, want ErrInvalidState", err)
	}
	if _, err := s.GetProject("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetProject missing: err = %v, want ErrNotFound", err)
	}
}

func TestListProjects(t *testing.T) {
	s := openStore(t, t.TempDir())
	seedProject(t, s, "b")
	seedProject(t, s, "a")

	list, err := s.ListProjects()
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(list) != 2 || list[0].Key != "a" || list[1].Key != "b" {
		t.Fatalf("ListProjects = %+v, want [a b]", list)
	}
}
