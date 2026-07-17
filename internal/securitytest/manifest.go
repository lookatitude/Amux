package securitytest

import (
	"fmt"
	"os"

	v1 "github.com/amux-run/amux/api/v1"
)

// The readiness manifest (docs/security/readiness-manifest.json) is the
// machine-readable half of docs/security/security-readiness.md: the exhaustive
// list of integrated-candidate checks that gate release promotion, each with
// its blocking severity, owner, reproducible command, prerequisites, and
// evidence path. It is consumed by T4 (implements to it) and T6 (executes
// it); TestReadinessManifestIsWellFormed and TestGateConstantsMatchManifest
// keep it consistent with this package executable-now.

const ReadinessManifestSchema = "amux.security.readiness-manifest.v1"

// ReceiptSchemaName is the per-check result record T6 emits when executing
// the manifest (the security-review receipt schema required by the lane).
const ReceiptSchemaName = "amux.security.check-receipt.v1"

// ReadinessCheck is one required integrated-candidate check.
type ReadinessCheck struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	// Owner is the lane that executes the check: security | T3-devops |
	// T4-backend | T6-qa.
	Owner string `json:"owner"`
	// Severity is the triage class of a failure: high | medium | low.
	Severity string `json:"severity"`
	// Blocking: a failing (or unexecuted-without-recorded-deferral) blocking
	// check stops release promotion.
	Blocking bool `json:"blocking"`
	// Command is the reproducible invocation (or "manual: ..." for the
	// misuse-case review).
	Command       string   `json:"command"`
	Prerequisites []string `json:"prerequisites"`
	// EvidencePath is where the executing lane deposits the check's output.
	EvidencePath string `json:"evidence_path"`
	// Requirements anchors the check to the normative IDs it verifies.
	Requirements []string `json:"requirements"`
}

// ReceiptSchema describes the record each executed check must produce.
type ReceiptSchema struct {
	Name           string   `json:"name"`
	RequiredFields []string `json:"required_fields"`
	Outcomes       []string `json:"outcomes"`
}

// ReadinessManifest is the whole machine-readable gate document.
type ReadinessManifest struct {
	Schema        string           `json:"schema"`
	RunID         string           `json:"run_id"`
	FrozenAt      string           `json:"frozen_at"`
	Gates         GateConstants    `json:"gates"`
	ReceiptSchema ReceiptSchema    `json:"receipt_schema"`
	Checks        []ReadinessCheck `json:"checks"`
}

var validOwners = map[string]bool{"security": true, "T3-devops": true, "T4-backend": true, "T6-qa": true}
var validSeverities = map[string]bool{"high": true, "medium": true, "low": true}

// LoadReadinessManifest strictly decodes the manifest from path.
func LoadReadinessManifest(path string) (*ReadinessManifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m ReadinessManifest
	if err := v1.DecodeStrict(raw, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &m, nil
}

// Validate enforces manifest well-formedness: schema/receipt names, unique
// check IDs, enum fields, high==blocking, and non-empty command/evidence/
// requirement fields on every check.
func (m *ReadinessManifest) Validate() error {
	if m.Schema != ReadinessManifestSchema {
		return fmt.Errorf("schema %q, want %q", m.Schema, ReadinessManifestSchema)
	}
	if m.ReceiptSchema.Name != ReceiptSchemaName {
		return fmt.Errorf("receipt schema %q, want %q", m.ReceiptSchema.Name, ReceiptSchemaName)
	}
	if len(m.ReceiptSchema.RequiredFields) == 0 || len(m.ReceiptSchema.Outcomes) == 0 {
		return fmt.Errorf("receipt schema must declare required_fields and outcomes")
	}
	if len(m.Checks) == 0 {
		return fmt.Errorf("manifest has no checks")
	}
	seen := map[string]bool{}
	for _, c := range m.Checks {
		if c.ID == "" || seen[c.ID] {
			return fmt.Errorf("empty or duplicate check id %q", c.ID)
		}
		seen[c.ID] = true
		if !validOwners[c.Owner] {
			return fmt.Errorf("%s: unknown owner %q", c.ID, c.Owner)
		}
		if !validSeverities[c.Severity] {
			return fmt.Errorf("%s: unknown severity %q", c.ID, c.Severity)
		}
		if c.Severity == "high" && !c.Blocking {
			return fmt.Errorf("%s: high severity must be blocking", c.ID)
		}
		if c.Command == "" || c.EvidencePath == "" || c.Description == "" {
			return fmt.Errorf("%s: command, evidence_path, and description are required", c.ID)
		}
		if len(c.Requirements) == 0 {
			return fmt.Errorf("%s: at least one requirement anchor is required", c.ID)
		}
	}
	return nil
}
