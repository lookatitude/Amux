//go:build linux

package platform

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// linuxValidator derives the discriminator from statx(2) birth time. See
// validation.go for the surface-selection rationale (btime over mount id and
// inode generation) and the fail-closed contract.
type linuxValidator struct{}

// NewReplacementValidator returns the statx-btime-backed Linux validator.
func NewReplacementValidator() ReplacementValidator { return linuxValidator{} }

const linuxValidationScheme = "statx-btime-v1"

func (linuxValidator) ValidationID(realpath string) (ValidationID, error) {
	var stx unix.Statx_t
	// realpath is already canonical (FilesystemIdentity.Identify resolved
	// symlinks); AT_STATX_SYNC_AS_STAT keeps semantics identical to the stat()
	// the durable key was computed from.
	err := unix.Statx(unix.AT_FDCWD, realpath, unix.AT_STATX_SYNC_AS_STAT, unix.STATX_BTIME, &stx)
	if err != nil {
		if errors.Is(err, unix.ENOSYS) {
			// Pre-4.11 kernel: no statx at all. Fail closed, never guess.
			return ValidationID{}, fmt.Errorf("%w: kernel lacks statx(2)", ErrValidationUnsupported)
		}
		return ValidationID{}, fmt.Errorf("amux: statx %s: %w", realpath, err)
	}
	if stx.Mask&unix.STATX_BTIME == 0 {
		// The filesystem does not report birth time. Fail closed: an absent
		// surface must never silently degrade to "always matches".
		return ValidationID{}, fmt.Errorf("%w: filesystem does not report statx birth time", ErrValidationUnsupported)
	}
	return ValidationID{
		Scheme: linuxValidationScheme,
		Value:  birthTimeDigest(uint64(stx.Btime.Sec), uint32(stx.Btime.Nsec)),
	}, nil
}
