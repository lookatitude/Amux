//go:build !linux

package platform

// This file supplies fail-closed placeholders for the capabilities that only
// Linux implements: descendant containment, descriptor-bound launch, and
// SO_PEERCRED peer validation (ADR-0006). On the macOS author host these let the
// whole module build and be exercised for its platform-neutral logic, while any
// attempt to actually use a Linux-only capability returns ErrUnsupportedPlatform
// rather than silently degrading. A real non-Linux implementation is a
// supported-platform change requiring spec confirmation.

type unsupportedContainment struct{}

// NewLinuxContainment is unavailable off Linux; it returns a fail-closed
// placeholder so callers compile everywhere but cannot obtain containment.
func NewLinuxContainment(string) Containment { return unsupportedContainment{} }

func (unsupportedContainment) Prepare(ContainmentSpec) (ContainmentHandle, error) {
	return nil, ErrUnsupportedPlatform
}

type unsupportedLaunch struct{}

// NewLinuxDescriptorLaunch is unavailable off Linux; fail-closed placeholder.
func NewLinuxDescriptorLaunch() DescriptorLaunch { return unsupportedLaunch{} }

func (unsupportedLaunch) OpenBound(int, string) (int, FSIdentity, error) {
	return -1, FSIdentity{}, ErrUnsupportedPlatform
}
func (unsupportedLaunch) LaunchBound(int, []string, []string, LaunchSpec) (int, error) {
	return -1, ErrUnsupportedPlatform
}

type unsupportedPeerCred struct{}

// NewLinuxPeerCredentials is unavailable off Linux; fail-closed placeholder.
func NewLinuxPeerCredentials() PeerCredentials { return unsupportedPeerCred{} }

func (unsupportedPeerCred) PeerUID(uintptr) (uint32, error) { return 0, ErrUnsupportedPlatform }

type unsupportedProcessInspector struct{}

// NewLinuxProcessInspector is unavailable off Linux; fail-closed placeholder
// (pane-context foreground fields stay undetermined rather than fabricated).
func NewLinuxProcessInspector() ProcessInspector { return unsupportedProcessInspector{} }

func (unsupportedProcessInspector) ForegroundPID(uintptr) (int, error) {
	return 0, ErrUnsupportedPlatform
}
func (unsupportedProcessInspector) Alive(int) (bool, error) { return false, ErrUnsupportedPlatform }
