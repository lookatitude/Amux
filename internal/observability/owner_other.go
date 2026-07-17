//go:build !darwin && !linux

package observability

import (
	"fmt"

	"github.com/amux-run/amux/internal/platform"
)

// validateOwnerOnlyDir fails closed on platforms without a stat-backed
// ownership check (Windows placeholder), so the pprof socket can never be
// created without the owner-only guarantee (ADR-0006 posture).
func validateOwnerOnlyDir(dir string) error {
	return fmt.Errorf("observability: cannot validate pprof socket dir %q ownership: %w", dir, platform.ErrUnsupportedPlatform)
}
