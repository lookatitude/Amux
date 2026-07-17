//go:build !darwin && !linux

package platform

// On platforms without a stat-backed FSIdentity (Windows placeholder), filesystem
// identity is unsupported and fails closed. A real implementation is a
// supported-platform change requiring spec confirmation (ADR-0006).
type unsupportedFSIdentity struct{}

// NewFilesystemIdentity returns the fail-closed placeholder on unsupported OSes.
func NewFilesystemIdentity() FilesystemIdentity { return unsupportedFSIdentity{} }

func (unsupportedFSIdentity) Identify(string) (string, FSIdentity, error) {
	return "", FSIdentity{}, ErrUnsupportedPlatform
}
