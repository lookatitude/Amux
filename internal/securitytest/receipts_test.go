package securitytest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// secondUIDCheck returns the frozen integration-second-uid manifest entry —
// the check whose receipt went stale in G-lane F2 review round 2.
func secondUIDCheck(t *testing.T) ReadinessCheck {
	t.Helper()
	for _, c := range loadManifest(t).Checks {
		if c.ID == "integration-second-uid" {
			return c
		}
	}
	t.Fatal("integration-second-uid check missing from the manifest")
	return ReadinessCheck{}
}

// writeReceiptFixture materializes a receipt (and optional evidence
// transcript) under artifactsRoot at the check's frozen relative paths.
func writeReceiptFixture(t *testing.T, artifactsRoot string, c ReadinessCheck, receiptJSON, evidence string) {
	t.Helper()
	rp := filepath.Join(artifactsRoot, ReceiptPathFor(c.EvidencePath))
	if err := os.MkdirAll(filepath.Dir(rp), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rp, []byte(receiptJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	if evidence != "" {
		if err := os.WriteFile(filepath.Join(artifactsRoot, filepath.FromSlash(c.EvidencePath)), []byte(evidence), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func receiptJSON(command, outcome, evidencePath, notes string) string {
	return `{
 "schema": "amux.security.check-receipt.v1",
 "check_id": "integration-second-uid",
 "command": ` + quote(command) + `,
 "exit_code": 0,
 "outcome": "` + outcome + `",
 "started_at": "2026-07-17T00:00:00Z",
 "host_os": "linux",
 "host_arch": "arm64",
 "tool_version": "go1.26.5",
 "evidence_path": "` + evidencePath + `",
 "notes": ` + quote(notes) + `
}`
}

func quote(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

const realHarnessEvidence = `=== RUN   TestSecondUIDForeignChainComponent
--- PASS: TestSecondUIDForeignChainComponent (0.00s)
=== RUN   TestSecondUIDForeignStaleSocket
--- PASS: TestSecondUIDForeignStaleSocket (0.00s)
=== RUN   TestSecondUIDForeignLiveSocketDial
--- PASS: TestSecondUIDForeignLiveSocketDial (0.00s)
=== RUN   TestSecondUIDExpectedOwnerMismatch
--- PASS: TestSecondUIDExpectedOwnerMismatch (0.01s)
PASS
ok  	github.com/amux-run/amux/internal/transport/local	0.012s
`

// The F2 remediation rule: a recorded pass receipt whose notes or evidence
// name a test that matches the check's -run pattern but does NOT bind to a
// real test function in the current tree (nonexistent or retired) can never
// validate. This is exactly the shape of the stale second-UID receipt that
// review round 2 blocked.
func TestValidateReceiptEvidenceRejectsRetiredTestNames(t *testing.T) {
	c := secondUIDCheck(t)

	// Stale notes: the retired stub is named in the receipt.
	root := t.TempDir()
	writeReceiptFixture(t, root, c,
		receiptJSON(c.Command, "pass", c.EvidencePath,
			"Executed with the real harness. The pre-existing TestSecondUIDVariantsDeferred stub always skipped."),
		realHarnessEvidence)
	err := ValidateReceiptEvidence(repoRoot(), root, c)
	if err == nil || !strings.Contains(err.Error(), "TestSecondUIDVariantsDeferred") {
		t.Fatalf("stale notes naming the retired stub validated: %v", err)
	}

	// Stale evidence: the transcript itself records the retired stub running.
	root = t.TempDir()
	writeReceiptFixture(t, root, c,
		receiptJSON(c.Command, "pass", c.EvidencePath, "4 top-level RUN, 4 PASS, 0 SKIP."),
		"=== RUN   TestSecondUIDVariantsDeferred\n--- SKIP: TestSecondUIDVariantsDeferred (0.00s)\n"+realHarnessEvidence)
	err = ValidateReceiptEvidence(repoRoot(), root, c)
	if err == nil || !strings.Contains(err.Error(), "TestSecondUIDVariantsDeferred") {
		t.Fatalf("stale evidence recording the retired stub validated: %v", err)
	}
}

func TestValidateReceiptEvidenceRules(t *testing.T) {
	c := secondUIDCheck(t)

	// Command drift: the receipt command must be byte-identical to the manifest.
	root := t.TempDir()
	writeReceiptFixture(t, root, c,
		receiptJSON("go test -count=1 -v -tags integration -run 'SecondUID' ./internal/transport/local/",
			"pass", c.EvidencePath, "4 top-level RUN, 4 PASS, 0 SKIP."),
		realHarnessEvidence)
	if err := ValidateReceiptEvidence(repoRoot(), root, c); err == nil || !strings.Contains(err.Error(), "byte-identical") {
		t.Fatalf("command drift validated: %v", err)
	}

	// Undeclared top-level skip of a bound test in a PASS receipt: refused
	// (§6: an undeclared skip is never pass).
	root = t.TempDir()
	writeReceiptFixture(t, root, c,
		receiptJSON(c.Command, "pass", c.EvidencePath, "counts recorded"),
		"=== RUN   TestSecondUIDForeignChainComponent\n--- SKIP: TestSecondUIDForeignChainComponent (0.00s)\n"+realHarnessEvidence)
	if err := ValidateReceiptEvidence(repoRoot(), root, c); err == nil || !strings.Contains(err.Error(), "skip") {
		t.Fatalf("undeclared skip validated: %v", err)
	}

	// Vacuous pass: -v evidence with zero substantive top-level PASS of a
	// bound test is refused.
	root = t.TempDir()
	writeReceiptFixture(t, root, c,
		receiptJSON(c.Command, "pass", c.EvidencePath, "counts recorded"),
		"=== RUN   TestSecondUIDForeignChainComponent\nPASS\nok  \tgithub.com/amux-run/amux/internal/transport/local\t0.01s\n")
	if err := ValidateReceiptEvidence(repoRoot(), root, c); err == nil {
		t.Fatal("vacuous evidence (no substantive top-level PASS) validated")
	}

	// Honest deferral: a deferred_prerequisite receipt is not gated on
	// substantive execution (it records WHY it could not run).
	root = t.TempDir()
	writeReceiptFixture(t, root, c,
		receiptJSON(c.Command, "deferred_prerequisite", c.EvidencePath,
			"Linux host with a second UID unavailable; exact deferred command recorded."), "")
	if err := ValidateReceiptEvidence(repoRoot(), root, c); err != nil {
		t.Fatalf("honest deferral refused: %v", err)
	}

	// Missing receipt: nothing recorded, nothing to validate here (a missing
	// receipt equals a failed check at promotion time, §6).
	if err := ValidateReceiptEvidence(repoRoot(), t.TempDir(), c); err != nil {
		t.Fatalf("missing receipt refused: %v", err)
	}

	// Healthy current receipt validates clean.
	root = t.TempDir()
	writeReceiptFixture(t, root, c,
		receiptJSON(c.Command, "pass", c.EvidencePath,
			"Real harness: 4 top-level RUN (TestSecondUIDForeignChainComponent et al), 4 PASS, 0 SKIP."),
		realHarnessEvidence)
	if err := ValidateReceiptEvidence(repoRoot(), root, c); err != nil {
		t.Fatalf("healthy receipt refused: %v", err)
	}
}

// TestRecordedReceiptsBindToCurrentTests is the repo-level self-gate (G-lane
// F2): every receipt actually recorded under the frozen evidence paths must
// satisfy ValidateReceiptEvidence against the CURRENT tree, so a receipt that
// froze evidence of a since-retired test fails the security-contract-self-gates
// check instead of silently passing promotion.
func TestRecordedReceiptsBindToCurrentTests(t *testing.T) {
	m := loadManifest(t)
	validated := 0
	for _, c := range m.Checks {
		if err := ValidateReceiptEvidence(repoRoot(), repoRoot(), c); err != nil {
			t.Errorf("check %s: recorded receipt fails current-tree validation: %v", c.ID, err)
			continue
		}
		if _, ok := ParseRunBinding(c.Command); ok {
			if _, err := os.Stat(filepath.Join(repoRoot(), ReceiptPathFor(c.EvidencePath))); err == nil {
				validated++
				t.Logf("check %s: recorded receipt validates against the current tree", c.ID)
			}
		}
	}
	t.Logf("%d recorded -run receipts validated", validated)
}
