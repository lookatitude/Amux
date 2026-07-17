//go:build darwin

package platform

import (
	"fmt"
	"syscall"
)

// darwinValidator derives the discriminator from the native st_birthtimespec
// (APFS/HFS+ maintain it with the required "changes exactly on recreation"
// property). Darwin is the author/test host, not a supported runtime platform
// (ADR-0006); this implementation exists so the project-identity contract —
// including the replacement-validation semantics — is testable on the
// development machine. The conservative documented behavior: a filesystem
// that reports a zero birth time fails closed with ErrValidationUnsupported.
type darwinValidator struct{}

// NewReplacementValidator returns the birthtime-backed Darwin validator.
func NewReplacementValidator() ReplacementValidator { return darwinValidator{} }

const darwinValidationScheme = "darwin-birthtime-v1"

func (darwinValidator) ValidationID(realpath string) (ValidationID, error) {
	var st syscall.Stat_t
	if err := syscall.Stat(realpath, &st); err != nil {
		return ValidationID{}, fmt.Errorf("amux: stat %s: %w", realpath, err)
	}
	bt := st.Birthtimespec
	if bt.Sec == 0 && bt.Nsec == 0 {
		return ValidationID{}, fmt.Errorf("%w: filesystem does not report a birth time", ErrValidationUnsupported)
	}
	return ValidationID{
		Scheme: darwinValidationScheme,
		Value:  birthTimeDigest(uint64(bt.Sec), uint32(bt.Nsec)),
	}, nil
}
