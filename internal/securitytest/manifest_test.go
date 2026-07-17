package securitytest

import "testing"

// requiredChecks is the floor promised by the prose docs: the deferred Linux
// integration checks (local-transport-hardening.md), the conformance and
// matrix replays, and the supply-chain/secrets scanners
// (security-readiness.md). Dropping one is a contract change.
var requiredChecks = []string{
	"security-contract-self-gates",
	"security-conformance",
	"trust-matrix-replay",
	"integration-second-uid",
	"integration-resource-exhaustion",
	"race-full-suite",
	"dep-integrity",
	"vuln-scan",
	"license-audit",
	"secrets-scan-history",
	"manual-misuse-review",
}

func loadManifest(t *testing.T) *ReadinessManifest {
	t.Helper()
	m, err := LoadReadinessManifest(ReadinessManifestPath())
	if err != nil {
		t.Fatalf("loading readiness manifest: %v", err)
	}
	return m
}

func TestReadinessManifestIsWellFormed(t *testing.T) {
	m := loadManifest(t)
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, c := range m.Checks {
		have[c.ID] = true
	}
	for _, id := range requiredChecks {
		if !have[id] {
			t.Errorf("required check %q missing from the readiness manifest", id)
		}
	}
}

// TestGateConstantsMatchManifest keeps prose (hook-authorization.md §7), the
// Go constants, and the machine manifest from drifting apart. Changing a gate
// is a spec-confirmation-gated contract change everywhere at once.
func TestGateConstantsMatchManifest(t *testing.T) {
	m := loadManifest(t)
	if m.Gates != Gates {
		t.Fatalf("manifest gates %+v != frozen constants %+v", m.Gates, Gates)
	}
}
