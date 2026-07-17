package securitytest

import (
	"crypto/sha256"
	"encoding/hex"
)

// Candidate secrets used by the redaction fixtures are DERIVED, never stored:
// golden files carry only the placeholder "{{SECRET:<label>}}" and the
// expected marker "[REDACTED:<label>]". At run time the harness substitutes
// DeriveCandidateSecret(label); TestRedactionFixturesContainNoRawSecrets
// walks docs/security and testdata/security proving no derived value (nor the
// recognizable AMUXTEST_ prefix) ever landed in a durable artifact. This is
// how the fixtures cover real secret-shaped values without a golden file ever
// containing one.

// CandidateSecretPrefix marks every derived candidate secret so an
// accidentally persisted value is unmistakable to scanners; .gitleaks.toml
// carries a rule for it.
const CandidateSecretPrefix = "AMUXTEST_"

// DeriveCandidateSecret returns the deterministic candidate secret for a
// fixture label. 32 hex chars of entropy after the prefix keeps it
// credential-shaped for the RED-2 value heuristics.
func DeriveCandidateSecret(label string) string {
	sum := sha256.Sum256([]byte("amux-securitytest-candidate:" + label))
	return CandidateSecretPrefix + hex.EncodeToString(sum[:])[:32]
}

// SecretPlaceholder is the golden-file stand-in for the candidate secret.
func SecretPlaceholder(label string) string { return "{{SECRET:" + label + "}}" }

// RedactionMarker is the exact replacement the engine must emit (RED-1).
func RedactionMarker(label string) string { return "[REDACTED:" + label + "]" }
