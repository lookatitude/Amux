//go:build !darwin && !linux

package platform

import "fmt"

// On platforms without a birth-time identity surface (Windows placeholder),
// replacement validation is unsupported and fails closed: trust reuse must be
// denied rather than accepting an ambiguous identity (ADR-0006 posture; a
// real implementation is a supported-platform change requiring spec
// confirmation).
type unsupportedValidator struct{}

// NewReplacementValidator returns the fail-closed placeholder.
func NewReplacementValidator() ReplacementValidator { return unsupportedValidator{} }

func (unsupportedValidator) ValidationID(string) (ValidationID, error) {
	return ValidationID{}, fmt.Errorf("%w: %v", ErrValidationUnsupported, ErrUnsupportedPlatform)
}
