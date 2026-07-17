//go:build darwin || linux

package platform

import (
	"os"
	"path/filepath"
	"testing"
)

// TestProjectKeyStableAndDistinct proves the trust-identity contract on the
// author host: the key is stable for the same root and distinct for a different
// root (different inode). This is the macOS-feasible half of the spec trust
// boundary; the replaced/remounted-root invalidation is a Linux fixture (T2/T6).
func TestProjectKeyStableAndDistinct(t *testing.T) {
	fsid := NewFilesystemIdentity()
	dir := t.TempDir()

	k1, real1, err := ComputeProjectKey(fsid, dir)
	if err != nil {
		t.Fatal(err)
	}
	if k1 == "" || real1 == "" {
		t.Fatal("empty key/realpath")
	}
	// Stable across calls.
	k1b, _, err := ComputeProjectKey(fsid, dir)
	if err != nil {
		t.Fatal(err)
	}
	if k1 != k1b {
		t.Fatal("project key not stable for the same root")
	}

	// A symlink to the same directory must resolve to the SAME key (realpath
	// canonicalization), proving a symlinked path cannot masquerade as a
	// different project.
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	kLink, _, err := ComputeProjectKey(fsid, link)
	if err != nil {
		t.Fatal(err)
	}
	if kLink != k1 {
		t.Fatal("symlink to the same root produced a different key; canonicalization failed")
	}

	// A different directory (different inode) must produce a different key.
	other := t.TempDir()
	k2, _, err := ComputeProjectKey(fsid, other)
	if err != nil {
		t.Fatal(err)
	}
	if k2 == k1 {
		t.Fatal("distinct roots produced the same project key")
	}
}

func TestIdentifyRejectsMissingPath(t *testing.T) {
	fsid := NewFilesystemIdentity()
	if _, _, err := fsid.Identify(filepath.Join(t.TempDir(), "does-not-exist")); err == nil {
		t.Fatal("Identify must error on a missing path (fail closed)")
	}
}
