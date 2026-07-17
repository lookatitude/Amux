package control

import (
	"os"
	"path/filepath"
	"testing"

	v1 "github.com/amux-run/amux/api/v1"
)

// baselineFacts is the allow baseline every trust-matrix row deviates from:
// project trusted at the current epoch, fresh active grant, scratch cwd,
// default bounds, empty env allowlist with nothing requested.
func baselineFacts() ActivationFacts {
	return ActivationFacts{
		ProjectRegistered:    true,
		TrustState:           StateApproved,
		RootIdentityMatch:    true,
		CurrentEpoch:         3,
		GrantFound:           true,
		GrantActive:          true,
		GrantBoundEpoch:      3,
		ExecDigestMatch:      true,
		ConfigDigestMatch:    true,
		ConfigEventSetMatch:  true,
		ConfigScopeMatch:     true,
		ConfigEnvMatch:       true,
		ConfigTimeoutMatch:   true,
		ConfigOutputCapMatch: true,
		TimeoutMS:            0, // default
		OutputCapBytes:       0, // default
		ExecIsRegularFile:    true,
		EventAllowed:         true,
		Scope:                ScopeFacts{Kind: ScopeNone, Resolved: true},
	}
}

// matrixFacts maps every frozen trust-matrix row ID to the ActivationFacts
// that materialize that row's single deviation. The replay test below fails if
// a golden row has no mapping, so a matrix change forces this table to keep up.
var matrixFacts = map[string]func(*ActivationFacts){
	"project.trusted-baseline":        func(f *ActivationFacts) {},
	"project.unregistered":            func(f *ActivationFacts) { f.ProjectRegistered = false },
	"project.registered-not-approved": func(f *ActivationFacts) { f.TrustState = StateNone },
	"project.denied":                  func(f *ActivationFacts) { f.TrustState = StateDenied },
	"project.revoked":                 func(f *ActivationFacts) { f.TrustState = StateRevoked },
	"project.replaced-root":           func(f *ActivationFacts) { f.RootIdentityMatch = false },
	"project.remounted-root":          func(f *ActivationFacts) { f.RootIdentityMatch = false },

	"grant.absent":                    func(f *ActivationFacts) { f.GrantFound = false },
	"grant.inactive-after-revocation": func(f *ActivationFacts) { f.GrantActive = false },
	"grant.epoch-stale":               func(f *ActivationFacts) { f.GrantBoundEpoch = f.CurrentEpoch - 1 },
	"grant.exec-digest-changed":       func(f *ActivationFacts) { f.ExecDigestMatch = false },
	"grant.config-digest-changed":     func(f *ActivationFacts) { f.ConfigDigestMatch = false },
	"grant.event-set-changed":         func(f *ActivationFacts) { f.ConfigEventSetMatch = false },
	"grant.cwd-scope-changed":         func(f *ActivationFacts) { f.ConfigScopeMatch = false },
	"grant.env-allowlist-changed":     func(f *ActivationFacts) { f.ConfigEnvMatch = false },
	"grant.timeout-changed":           func(f *ActivationFacts) { f.ConfigTimeoutMatch = false },
	"grant.output-cap-changed":        func(f *ActivationFacts) { f.ConfigOutputCapMatch = false },

	"scope.none-scratch": func(f *ActivationFacts) {},
	"scope.fixed-valid":  func(f *ActivationFacts) { f.Scope = ScopeFacts{Kind: ScopeFixed, Resolved: true} },
	"scope.fixed-identity-changed": func(f *ActivationFacts) {
		f.Scope = ScopeFacts{Kind: ScopeFixed, Reason: "granted directory identity changed"}
	},
	"scope.wsprimary-valid": func(f *ActivationFacts) {
		f.Scope = ScopeFacts{Kind: ScopeWorkspacePrimary, Resolved: true}
	},
	"scope.wsprimary-absent": func(f *ActivationFacts) {
		f.Scope = ScopeFacts{Kind: ScopeWorkspacePrimary, Reason: "no primary root configured"}
	},
	"scope.wsprimary-ambiguous": func(f *ActivationFacts) {
		f.Scope = ScopeFacts{Kind: ScopeWorkspacePrimary, Reason: "more than one candidate primary root"}
	},
	"scope.wsprimary-replaced": func(f *ActivationFacts) {
		f.Scope = ScopeFacts{Kind: ScopeWorkspacePrimary, Reason: "primary root identity changed"}
	},
	"scope.wsprimary-foreign-project": func(f *ActivationFacts) {
		f.Scope = ScopeFacts{Kind: ScopeWorkspacePrimary, Reason: "primary root belongs to another project"}
	},
	"scope.pane-same-project": func(f *ActivationFacts) {
		f.Scope = ScopeFacts{Kind: ScopePane, Resolved: true}
	},
	"scope.pane-cross-project": func(f *ActivationFacts) {
		f.Scope = ScopeFacts{Kind: ScopePane, Reason: "pane resolves to another project"}
	},
	"scope.pane-unregistered": func(f *ActivationFacts) {
		f.Scope = ScopeFacts{Kind: ScopePane, Reason: "pane has no registered project"}
	},
	"scope.event-not-granted": func(f *ActivationFacts) { f.EventAllowed = false },

	"bounds.timeout-default":        func(f *ActivationFacts) { f.TimeoutMS = 0 },
	"bounds.timeout-max":            func(f *ActivationFacts) { f.TimeoutMS = MaxTimeoutMS },
	"bounds.timeout-exceeds-max":    func(f *ActivationFacts) { f.TimeoutMS = MaxTimeoutMS + 1 },
	"bounds.output-within-cap":      func(f *ActivationFacts) { f.OutputCapBytes = 1024 },
	"bounds.output-cap-exceeds-max": func(f *ActivationFacts) { f.OutputCapBytes = MaxOutputCapBytes + 1 },
	"bounds.output-cap-enforced":    func(f *ActivationFacts) {},
	"bounds.exec-not-regular-file":  func(f *ActivationFacts) { f.ExecIsRegularFile = false },

	"env.empty-allowlist":           func(f *ActivationFacts) {},
	"env.allowlisted-keys-only":     func(f *ActivationFacts) {},
	"env.non-allowlisted-requested": func(f *ActivationFacts) { f.EnvOutsideAllowlist = true },

	"load.queue-exhausted": func(f *ActivationFacts) { f.QueueExhausted = true },

	"internal.invariant-violation": func(f *ActivationFacts) { f.InvariantViolated = true },
}

type goldenMatrix struct {
	Schema string `json:"schema"`
	Gates  struct {
		AbsentTrustMS    int64 `json:"absent_trust_ms"`
		RevokeCancelMS   int64 `json:"revoke_cancel_ms"`
		KillEscalationMS int64 `json:"kill_escalation_ms"`
		DefaultTimeoutMS int64 `json:"default_timeout_ms"`
		MaxTimeoutMS     int64 `json:"max_timeout_ms"`
		OutputCapBytes   int64 `json:"output_cap_bytes"`
	} `json:"gates"`
	Rows []struct {
		ID          string `json:"id"`
		Family      string `json:"family"`
		Requirement string `json:"requirement"`
		Condition   string `json:"condition"`
		Expect      struct {
			Decision    string       `json:"decision"`
			Code        v1.ErrorCode `json:"code"`
			Enforcement string       `json:"enforcement"`
		} `json:"expect"`
	} `json:"rows"`
}

// TestDecideReplaysTrustMatrix replays every row of the generated T2 golden
// (testdata/security/trust-matrix.json) against the pure decision function.
// This is the T4 half of the "matrix replay" prerequisite the T2 receipt
// deferred: each pinned condition -> outcome cell is enforced by real backend
// decision code, not prose.
func TestDecideReplaysTrustMatrix(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "security", "trust-matrix.json"))
	if err != nil {
		t.Fatalf("reading trust-matrix golden: %v", err)
	}
	var m goldenMatrix
	if err := v1.DecodeStrict(raw, &m); err != nil {
		t.Fatalf("decoding trust-matrix golden: %v", err)
	}
	if m.Gates.DefaultTimeoutMS != DefaultTimeoutMS || m.Gates.MaxTimeoutMS != MaxTimeoutMS || m.Gates.OutputCapBytes != MaxOutputCapBytes {
		t.Fatalf("gate constants drifted from the golden: golden=%+v decide=(%d,%d,%d)",
			m.Gates, DefaultTimeoutMS, MaxTimeoutMS, MaxOutputCapBytes)
	}
	if len(m.Rows) == 0 {
		t.Fatal("golden matrix has no rows")
	}
	for _, row := range m.Rows {
		row := row
		t.Run(row.ID, func(t *testing.T) {
			mutate, ok := matrixFacts[row.ID]
			if !ok {
				t.Fatalf("no ActivationFacts mapping for matrix row %q — extend matrixFacts", row.ID)
			}
			f := baselineFacts()
			mutate(&f)
			d := Decide(f)
			switch row.Expect.Decision {
			case "allow":
				if !d.Allow {
					t.Fatalf("decision deny(%s: %s), want allow", d.Code, d.Reason)
				}
				if row.Expect.Enforcement != "" && !contains(d.Enforcements, row.Expect.Enforcement) {
					t.Fatalf("allow lacks required enforcement %q (got %v)", row.Expect.Enforcement, d.Enforcements)
				}
			case "deny":
				if d.Allow {
					t.Fatal("decision allow, want deny (fail closed)")
				}
				if d.Code != row.Expect.Code {
					t.Fatalf("deny code %s, want %s", d.Code, row.Expect.Code)
				}
			default:
				t.Fatalf("golden row with unknown decision %q", row.Expect.Decision)
			}
		})
	}
}

// TestDecideDenyBeatsEveryAllowFact pins fail-closed precedence: with every
// allow fact present, any single deny fact still denies.
func TestDecideDenyBeatsEveryAllowFact(t *testing.T) {
	f := baselineFacts()
	f.InvariantViolated = true
	f.QueueExhausted = true
	if d := Decide(f); d.Allow || d.Code != v1.ErrInternal {
		t.Fatalf("invariant violation must dominate everything, got %+v", d)
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
