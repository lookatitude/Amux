package securitytest

// receipts.go extends the F5 gate-binding discipline from the manifest's
// commands to the RECORDED evidence (G-lane F2): a check receipt that froze
// output of a test which has since been retired — or never existed — must not
// keep passing. ValidateReceiptEvidence re-validates each recorded pass
// receipt for a `-run` check against the CURRENT source tree: the receipt
// command must be byte-identical to the manifest command, every pattern-
// matching test name the receipt or its evidence transcript mentions must
// bind to a real test function under the check's tags on the Linux target,
// the transcript must show substantive top-level execution, and an undeclared
// top-level skip of a bound test can never ride under `outcome: pass`.
// TestRecordedReceiptsBindToCurrentTests wires this into the blocking
// security-contract-self-gates check.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	v1 "github.com/amux-run/amux/api/v1"
)

// CheckReceipt is one amux.security.check-receipt.v1 record
// (security-readiness.md §6).
type CheckReceipt struct {
	Schema       string `json:"schema"`
	CheckID      string `json:"check_id"`
	Command      string `json:"command"`
	ExitCode     int    `json:"exit_code"`
	Outcome      string `json:"outcome"`
	StartedAt    string `json:"started_at"`
	HostOS       string `json:"host_os"`
	HostArch     string `json:"host_arch"`
	ToolVersion  string `json:"tool_version"`
	EvidencePath string `json:"evidence_path"`
	Notes        string `json:"notes"`
}

// ReceiptPathFor is the frozen receipt-location convention: the check's
// evidence file with its extension replaced by ".receipt.json".
func ReceiptPathFor(evidencePath string) string {
	p := filepath.FromSlash(evidencePath)
	return strings.TrimSuffix(p, filepath.Ext(p)) + ".receipt.json"
}

// testNameRe finds Go test identifiers in prose or transcripts. The rune
// after "Test" is non-lowercase, mirroring `go test` discovery, so ordinary
// words like "Tests" never match.
var testNameRe = regexp.MustCompile(`\bTest[A-Z0-9_][A-Za-z0-9_]*`)

// ValidateReceiptEvidence validates the recorded receipt (and its evidence
// transcript) for check c against the current source tree under moduleRoot.
// Receipt and evidence paths resolve against artifactsRoot (the module root
// in production; a fixture directory in tests). A missing receipt is NOT an
// error here — a missing receipt equals a failed check at promotion time
// (§6); this gate's job is narrower: whatever evidence IS recorded can never
// pass on a nonexistent or retired test. Non-pass outcomes (honest failures
// and deferrals) and commands without a -run binding are exempt.
func ValidateReceiptEvidence(moduleRoot, artifactsRoot string, c ReadinessCheck) error {
	b, ok := ParseRunBinding(c.Command)
	if !ok {
		return nil
	}
	receiptPath := filepath.Join(artifactsRoot, ReceiptPathFor(c.EvidencePath))
	raw, err := os.ReadFile(receiptPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading receipt: %w", err)
	}
	var r CheckReceipt
	if err := v1.DecodeStrict(raw, &r); err != nil {
		return fmt.Errorf("%s: %w", receiptPath, err)
	}
	if r.Schema != ReceiptSchemaName {
		return fmt.Errorf("receipt schema %q, want %q", r.Schema, ReceiptSchemaName)
	}
	if r.CheckID != c.ID {
		return fmt.Errorf("receipt check_id %q under %s's evidence path", r.CheckID, c.ID)
	}
	if r.Outcome != "pass" {
		return nil
	}
	if r.Command != c.Command {
		return fmt.Errorf("pass receipt command %q is not byte-identical to the manifest command %q", r.Command, c.Command)
	}
	if r.EvidencePath != c.EvidencePath {
		return fmt.Errorf("pass receipt evidence_path %q drifted from the frozen path %q", r.EvidencePath, c.EvidencePath)
	}

	bound, err := EnumerateBoundTests(moduleRoot, b, "linux")
	if err != nil {
		return err
	}
	boundSet := map[string]bool{}
	for _, name := range bound {
		boundSet[name] = true
	}
	topPattern, err := regexp.Compile(strings.SplitN(b.Pattern, "/", 2)[0])
	if err != nil {
		return fmt.Errorf("-run pattern %q: %w", b.Pattern, err)
	}
	// Any pattern-matching test name the receipt itself mentions must exist
	// in the current tree under the check's binding.
	if err := namesMustBind(r.Notes, "receipt notes", topPattern, boundSet); err != nil {
		return err
	}

	evidencePath := filepath.Join(artifactsRoot, filepath.FromSlash(c.EvidencePath))
	evidence, err := os.ReadFile(evidencePath)
	if os.IsNotExist(err) {
		return fmt.Errorf("pass receipt has no evidence transcript at %s (§6: pass must be generated from -v output)", c.EvidencePath)
	}
	if err != nil {
		return fmt.Errorf("reading evidence: %w", err)
	}
	text := string(evidence)
	if err := namesMustBind(text, "evidence transcript", topPattern, boundSet); err != nil {
		return err
	}

	// Substantive execution: at least one top-level PASS of a bound test, and
	// no undeclared top-level skip of a pattern-matching test under a `pass`
	// outcome (an undeclared skip is never pass, §6).
	passes := 0
	for _, line := range strings.Split(text, "\n") {
		if name, ok := strings.CutPrefix(line, "--- PASS: "); ok {
			if boundSet[strings.Fields(name)[0]] {
				passes++
			}
		}
		if name, ok := strings.CutPrefix(line, "--- SKIP: "); ok {
			skipped := strings.Fields(name)[0]
			// A skip is declared only when the receipt notes name the skipped
			// test explicitly (and namesMustBind above already proved that
			// name still binds — a retired stub can never be "declared").
			if topPattern.MatchString(skipped) && !strings.Contains(r.Notes, skipped) {
				return fmt.Errorf("pass receipt evidence records an undeclared top-level skip of %s (skips are pass-compatible only when declared in the receipt notes; a prerequisite skip is deferred_prerequisite, never pass)", skipped)
			}
		}
	}
	if passes == 0 {
		return fmt.Errorf("pass receipt evidence shows zero substantive top-level PASS lines for -run %q — a vacuous pass", b.Pattern)
	}
	return nil
}

// namesMustBind rejects any pattern-matching test identifier in text that
// does not bind to a real test function in the current tree — the exact shape
// of stale evidence naming a retired or nonexistent test.
func namesMustBind(text, where string, topPattern *regexp.Regexp, boundSet map[string]bool) error {
	for _, name := range testNameRe.FindAllString(text, -1) {
		if topPattern.MatchString(name) && !boundSet[name] {
			return fmt.Errorf("%s names %s, which matches the -run pattern but binds to no test in the current tree (retired or nonexistent)", where, name)
		}
	}
	return nil
}
