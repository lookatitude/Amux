//go:build !(darwin || linux)

package pty

import "github.com/amux-run/amux/internal/platform"

// unsupportedPTY is the fail-closed placeholder for build targets with no PTY
// mechanism (ADR-0006: Linux is the product; darwin builds run tests only; any
// other target fails closed rather than degrading silently).
type unsupportedPTY struct{}

// New returns a fail-closed platform.PTY on unsupported targets.
func New() platform.PTY { return unsupportedPTY{} }

func (unsupportedPTY) Start(platform.PTYSpec) (platform.PTYHandle, error) {
	return nil, platform.ErrUnsupportedPlatform
}
