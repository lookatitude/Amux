//go:build darwin || linux

package platform

import (
	"path/filepath"
	"syscall"
)

// unixFSIdentity implements FilesystemIdentity via stat(2). It works on both
// Linux (the MVP target) and Darwin (the author host), so the project-identity
// contract is testable on the development machine even though the runtime ships
// on Linux. The dev/ino tuple is cast to uint64 to normalize the differing
// field widths across the two platforms.
type unixFSIdentity struct{}

// NewFilesystemIdentity returns the unix stat-backed implementation.
func NewFilesystemIdentity() FilesystemIdentity { return unixFSIdentity{} }

func (unixFSIdentity) Identify(path string) (string, FSIdentity, error) {
	// Resolve symlinks and relative components so the identity is canonical; a
	// symlinked or relative path can never masquerade as a different root.
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", FSIdentity{}, err
	}
	abs, err := filepath.Abs(real)
	if err != nil {
		return "", FSIdentity{}, err
	}
	var st syscall.Stat_t
	if err := syscall.Stat(abs, &st); err != nil {
		return "", FSIdentity{}, err
	}
	return abs, FSIdentity{Dev: uint64(st.Dev), Ino: uint64(st.Ino)}, nil
}
