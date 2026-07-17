package securitytest

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	v1 "github.com/amux-run/amux/api/v1"
)

// Fixture vectors are the declarative half of the adversarial fixtures: JSON
// documents under testdata/security/fixtures/ naming each fixture, the
// requirement it pins (HA-*, RED-*), and its machine-pinned expectations. The
// procedural half (what the harness does with each vector) lives in
// harness.go. Vectors are validated executable-now by
// TestFixtureVectorsAreWellFormed; they gate the contract itself even before
// a backend exists.

const FixtureSetSchema = "amux.security.fixtures.v1"

// TimingKind enumerates the deterministic ordering/latency fixtures.
const (
	TimingAbsentTrust  = "absent-trust"
	TimingRevokeCancel = "revoke-cancel"
	TimingRevokeFirst  = "revoke-first"
	TimingLaunchFirst  = "launch-first"
)

// RaceMutation enumerates the check-to-exec object-substitution attacks.
const (
	MutationSymlinkSwap        = "symlink-swap"
	MutationRenameSwap         = "rename-swap"
	MutationExecByteReplace    = "exec-byte-replace"
	MutationConfigByteReplace  = "config-byte-replace"
	MutationProjectRootReplace = "project-root-replace"
)

// RestoreClaim enumerates the forged-generation claims restore must reject.
const (
	ClaimEpochDecrease   = "epoch-decrease"
	ClaimGrantReactivate = "grant-reactivate"
	ClaimAuditErase      = "audit-erase"
	ClaimLaunchAuthority = "launch-authority"
)

// TimingFixture pins one HA-14/HA-15/HA-18/HA-22 ordering-and-latency case.
type TimingFixture struct {
	ID          string   `json:"id"`
	Requirement string   `json:"requirement"`
	Kind        string   `json:"kind"`
	GateMS      int64    `json:"gate_ms"`
	Expect      Expected `json:"expect"`
}

// RaceFixture pins one HA-10/HA-11/HA-13 substitution race: the approved
// object executes, or launch fails closed; the substituted object never runs.
type RaceFixture struct {
	ID          string `json:"id"`
	Requirement string `json:"requirement"`
	Mutation    string `json:"mutation"`
}

// RestoreFixture pins one HA-18..HA-21 forged-generation rejection.
type RestoreFixture struct {
	ID          string `json:"id"`
	Requirement string `json:"requirement"`
	Claim       string `json:"claim"`
}

// RedactionVector pins RED-1/RED-2/RED-5 for one egress context. Payload
// carries only SecretPlaceholder(label); the harness substitutes
// DeriveCandidateSecret(label) at run time so no golden file ever contains a
// raw candidate secret. TruncateAtBytes > 0 marks the RED-5 truncation case:
// the harness truncates the substituted payload at that byte offset (splitting
// the secret) before redaction, and no AMUXTEST_ remnant may survive.
type RedactionVector struct {
	ID              string `json:"id"`
	Requirement     string `json:"requirement"`
	Context         string `json:"context"`
	Label           string `json:"label"`
	Payload         string `json:"payload"`
	TruncateAtBytes int64  `json:"truncate_at_bytes,omitempty"`
}

type timingFile struct {
	Schema   string          `json:"schema"`
	Fixtures []TimingFixture `json:"fixtures"`
}
type raceFile struct {
	Schema   string        `json:"schema"`
	Fixtures []RaceFixture `json:"fixtures"`
}
type restoreFile struct {
	Schema   string           `json:"schema"`
	Fixtures []RestoreFixture `json:"fixtures"`
}
type redactionFile struct {
	Schema   string            `json:"schema"`
	Fixtures []RedactionVector `json:"fixtures"`
}

// FixtureSet is the loaded union of all fixture vector files.
type FixtureSet struct {
	Timing    []TimingFixture
	Races     []RaceFixture
	Restore   []RestoreFixture
	Redaction []RedactionVector
}

// sourceDir resolves this source file's directory so fixture paths work no
// matter which package (this one, or the T4 backend's conformance test)
// invokes the harness.
func sourceDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("securitytest: runtime.Caller failed resolving fixture paths")
	}
	return filepath.Dir(file)
}

// FixtureDir is the canonical vector directory (testdata/security/fixtures at
// the repo root).
func FixtureDir() string {
	return filepath.Join(sourceDir(), "..", "..", "testdata", "security", "fixtures")
}

// TrustMatrixGoldenPath is the generated trust-matrix golden document.
func TrustMatrixGoldenPath() string {
	return filepath.Join(sourceDir(), "..", "..", "testdata", "security", "trust-matrix.json")
}

// ReadinessManifestPath is the machine-readable readiness manifest.
func ReadinessManifestPath() string {
	return filepath.Join(sourceDir(), "..", "..", "docs", "security", "readiness-manifest.json")
}

func loadJSON(path, wantSchema string, gotSchema *string, into any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := v1.DecodeStrict(raw, into); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if *gotSchema != wantSchema {
		return fmt.Errorf("%s: schema %q, want %q", path, *gotSchema, wantSchema)
	}
	return nil
}

// LoadFixtures reads and strictly decodes every vector file in dir.
func LoadFixtures(dir string) (*FixtureSet, error) {
	var (
		tf timingFile
		rf raceFile
		sf restoreFile
		df redactionFile
	)
	if err := loadJSON(filepath.Join(dir, "timing.json"), FixtureSetSchema, &tf.Schema, &tf); err != nil {
		return nil, err
	}
	if err := loadJSON(filepath.Join(dir, "races.json"), FixtureSetSchema, &rf.Schema, &rf); err != nil {
		return nil, err
	}
	if err := loadJSON(filepath.Join(dir, "restore.json"), FixtureSetSchema, &sf.Schema, &sf); err != nil {
		return nil, err
	}
	if err := loadJSON(filepath.Join(dir, "redaction.json"), FixtureSetSchema, &df.Schema, &df); err != nil {
		return nil, err
	}
	return &FixtureSet{Timing: tf.Fixtures, Races: rf.Fixtures, Restore: sf.Fixtures, Redaction: df.Fixtures}, nil
}

func validCode(c v1.ErrorCode) bool {
	for _, k := range v1.AllErrorCodes {
		if c == k {
			return true
		}
	}
	return false
}

// requiredTimingGate maps each timing kind to the frozen gate constant its
// vector must carry — vectors cannot drift from securitytest.Gates.
var requiredTimingGate = map[string]int64{
	TimingAbsentTrust:  Gates.AbsentTrustMS,
	TimingRevokeCancel: Gates.RevokeCancelMS,
	TimingRevokeFirst:  Gates.RevokeCancelMS,
	TimingLaunchFirst:  Gates.KillEscalationMS,
}

var requiredMutations = []string{
	MutationSymlinkSwap, MutationRenameSwap, MutationExecByteReplace,
	MutationConfigByteReplace, MutationProjectRootReplace,
}

var requiredClaims = []string{
	ClaimEpochDecrease, ClaimGrantReactivate, ClaimAuditErase, ClaimLaunchAuthority,
}

// ValidateFixtures enforces the executable-now well-formedness contract:
// unique IDs; known kinds/mutations/claims with full required coverage; deny
// codes inside the frozen taxonomy; timing gates equal to the frozen
// constants; redaction coverage of every RED-1 context; and placeholder-only
// payloads (no raw candidate secret, no AMUXTEST_ remnant, in any vector).
func ValidateFixtures(fs *FixtureSet) error {
	seen := map[string]bool{}
	uniq := func(id string) error {
		if id == "" {
			return fmt.Errorf("fixture with empty id")
		}
		if seen[id] {
			return fmt.Errorf("duplicate fixture id %q", id)
		}
		seen[id] = true
		return nil
	}

	for _, f := range fs.Timing {
		if err := uniq(f.ID); err != nil {
			return err
		}
		want, ok := requiredTimingGate[f.Kind]
		if !ok {
			return fmt.Errorf("%s: unknown timing kind %q", f.ID, f.Kind)
		}
		if f.GateMS != want {
			return fmt.Errorf("%s: gate_ms %d drifted from frozen constant %d", f.ID, f.GateMS, want)
		}
		if f.Expect.Decision == DecisionDeny && !validCode(f.Expect.Code) {
			return fmt.Errorf("%s: code %q outside the frozen taxonomy", f.ID, f.Expect.Code)
		}
		if f.Requirement == "" {
			return fmt.Errorf("%s: missing requirement anchor", f.ID)
		}
	}

	mut := map[string]bool{}
	for _, f := range fs.Races {
		if err := uniq(f.ID); err != nil {
			return err
		}
		mut[f.Mutation] = true
	}
	for _, m := range requiredMutations {
		if !mut[m] {
			return fmt.Errorf("races: required mutation %q has no fixture", m)
		}
	}

	claims := map[string]bool{}
	for _, f := range fs.Restore {
		if err := uniq(f.ID); err != nil {
			return err
		}
		claims[f.Claim] = true
	}
	for _, c := range requiredClaims {
		if !claims[c] {
			return fmt.Errorf("restore: required claim %q has no fixture", c)
		}
	}

	ctx := map[string]bool{}
	for _, f := range fs.Redaction {
		if err := uniq(f.ID); err != nil {
			return err
		}
		if f.Label == "" {
			return fmt.Errorf("%s: missing secret label", f.ID)
		}
		if !strings.Contains(f.Payload, SecretPlaceholder(f.Label)) {
			return fmt.Errorf("%s: payload does not carry %s", f.ID, SecretPlaceholder(f.Label))
		}
		if strings.Contains(f.Payload, CandidateSecretPrefix) {
			return fmt.Errorf("%s: payload contains a raw candidate secret (must use placeholders)", f.ID)
		}
		ctx[f.Context] = true
	}
	for _, c := range RedactionContexts {
		if !ctx[c] {
			return fmt.Errorf("redaction: RED-1 context %q has no fixture coverage", c)
		}
	}
	return nil
}
