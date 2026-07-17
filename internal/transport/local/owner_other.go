//go:build !unix

package local

import (
	"os"

	"github.com/amux-run/amux/internal/platform"
)

// fileOwner fails closed off Unix: every ownership check refuses, so the
// transport can never bind or dial on a platform where the STR-1/STR-3/STR-4
// ownership discipline cannot be enforced (ADR-0006: placeholders fail closed
// with ErrUnsupportedPlatform, never degrade silently).
func fileOwner(string, os.FileInfo) (uint32, error) {
	return 0, platform.ErrUnsupportedPlatform
}
