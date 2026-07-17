package securitytest

import (
	"bytes"
	"flag"
	"os"
	"testing"
)

var updateGolden = flag.Bool("update-golden", false,
	"rewrite testdata/security/trust-matrix.json from GenerateTrustMatrix (contract change; spec-gated)")

// requiredRows is the lane contract's coverage floor: absent/stale/revoked
// project and hook grants, replaced/remounted roots, cross-project panes, all
// cwd scopes, digest/config changes, timeout/output bounds, and environment
// allowlists. Removing any of these rows is a contract change.
var requiredRows = []string{
	"project.trusted-baseline",
	"project.unregistered",
	"project.registered-not-approved",
	"project.denied",
	"project.revoked",
	"project.replaced-root",
	"project.remounted-root",
	"grant.absent",
	"grant.inactive-after-revocation",
	"grant.epoch-stale",
	"grant.exec-digest-changed",
	"grant.config-digest-changed",
	"scope.none-scratch",
	"scope.fixed-valid",
	"scope.fixed-identity-changed",
	"scope.wsprimary-valid",
	"scope.wsprimary-absent",
	"scope.wsprimary-ambiguous",
	"scope.wsprimary-replaced",
	"scope.wsprimary-foreign-project",
	"scope.pane-same-project",
	"scope.pane-cross-project",
	"scope.pane-unregistered",
	"bounds.timeout-default",
	"bounds.timeout-exceeds-max",
	"bounds.output-cap-enforced",
	"env.empty-allowlist",
	"env.allowlisted-keys-only",
	"env.non-allowlisted-requested",
}

func TestTrustMatrixCoverage(t *testing.T) {
	m := GenerateTrustMatrix()
	if m.Schema != TrustMatrixSchema {
		t.Fatalf("schema %q, want %q", m.Schema, TrustMatrixSchema)
	}
	if m.Gates != Gates {
		t.Fatalf("matrix gates %+v drifted from frozen constants %+v", m.Gates, Gates)
	}
	seen := map[string]bool{}
	for _, r := range m.Rows {
		if r.ID == "" || seen[r.ID] {
			t.Fatalf("empty or duplicate row id %q", r.ID)
		}
		seen[r.ID] = true
		if r.Requirement == "" || r.Condition == "" {
			t.Fatalf("%s: requirement and condition are mandatory", r.ID)
		}
		switch r.Expect.Decision {
		case DecisionAllow:
			if r.Expect.Code != "" {
				t.Fatalf("%s: allow row carries an error code", r.ID)
			}
		case DecisionDeny:
			if !validCode(r.Expect.Code) {
				t.Fatalf("%s: code %q outside the frozen taxonomy", r.ID, r.Expect.Code)
			}
		default:
			t.Fatalf("%s: unknown decision %q", r.ID, r.Expect.Decision)
		}
	}
	for _, id := range requiredRows {
		if !seen[id] {
			t.Fatalf("required matrix row %q is missing (lane-contract coverage floor)", id)
		}
	}
}

func TestTrustMatrixGoldenIsCurrent(t *testing.T) {
	want := MarshalTrustMatrix(GenerateTrustMatrix())
	path := TrustMatrixGoldenPath()
	if *updateGolden {
		if err := os.WriteFile(path, want, 0o644); err != nil {
			t.Fatalf("updating golden: %v", err)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden (generate with -update-golden): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("testdata/security/trust-matrix.json is stale relative to GenerateTrustMatrix. " +
			"The matrix is a frozen contract: if this change is intended it must pass the " +
			"spec confirmation gate, then regenerate with " +
			"`go test ./internal/securitytest -run TestTrustMatrixGoldenIsCurrent -update-golden`.")
	}
}
