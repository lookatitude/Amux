package store

import (
	"errors"
	"testing"
)

func sampleGrant(id, project string, active bool) GrantRow {
	return GrantRow{
		ID:               id,
		ProjectKey:       project,
		HookID:           "on-save",
		ExecPath:         "/repo/hooks/fmt",
		ExecSHA256:       "aa11",
		ConfigSHA256:     "bb22",
		EventsJSON:       `["save","close"]`,
		ScopeKind:        "fixed",
		FixedPath:        "/repo",
		EnvAllowlistJSON: `["PATH"]`,
		TimeoutMS:        250,
		OutputCapBytes:   4096,
		BoundEpoch:       7,
		Active:           active,
	}
}

func TestGrantRoundtrip(t *testing.T) {
	s := openStore(t, t.TempDir())
	seedProject(t, s, "p1")

	want := sampleGrant("g1", "p1", true)
	if err := s.InsertGrant(want); err != nil {
		t.Fatalf("InsertGrant: %v", err)
	}
	got, err := s.GetGrant("g1")
	if err != nil {
		t.Fatalf("GetGrant: %v", err)
	}
	if got != want {
		t.Fatalf("GetGrant = %+v, want %+v", got, want)
	}
	if _, err := s.GetGrant("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing grant: err = %v, want ErrNotFound", err)
	}
}

func TestInsertGrantRequiresRegisteredProject(t *testing.T) {
	s := openStore(t, t.TempDir())
	// Foreign keys are ON: a grant can never exist without its trust row.
	if err := s.InsertGrant(sampleGrant("g1", "ghost", true)); err == nil {
		t.Fatal("InsertGrant for unregistered project succeeded, want FK failure")
	}
}

func TestDeactivateGrantsRetainsHistoryForever(t *testing.T) {
	s := openStore(t, t.TempDir())
	seedProject(t, s, "p1")
	seedProject(t, s, "p2")

	for _, g := range []GrantRow{
		sampleGrant("g1", "p1", true),
		sampleGrant("g2", "p1", true),
		sampleGrant("g3", "p1", false), // already inactive history
		sampleGrant("g4", "p2", true),  // other project untouched
	} {
		if err := s.InsertGrant(g); err != nil {
			t.Fatalf("InsertGrant(%s): %v", g.ID, err)
		}
	}

	n, err := s.DeactivateGrantsForProject("p1")
	if err != nil {
		t.Fatalf("DeactivateGrantsForProject: %v", err)
	}
	if n != 2 {
		t.Fatalf("deactivated %d grants, want 2", n)
	}

	active, err := s.ListGrants("p1", false)
	if err != nil {
		t.Fatalf("ListGrants(active): %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("active grants after revocation = %+v, want none", active)
	}

	// Rows are NEVER deleted (ADR-0005: history retained forever).
	all, err := s.ListGrants("p1", true)
	if err != nil {
		t.Fatalf("ListGrants(all): %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("retained %d grant rows, want 3", len(all))
	}
	for _, g := range all {
		if g.Active {
			t.Fatalf("grant %s still active after revocation", g.ID)
		}
	}

	other, err := s.ListGrants("p2", false)
	if err != nil || len(other) != 1 || !other[0].Active {
		t.Fatalf("other project's grants affected: %+v, %v", other, err)
	}
}
