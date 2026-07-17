//go:build darwin || linux

package platform

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The replacement-validation discriminator (G-lane F2) must satisfy three
// properties on every supported development/runtime filesystem:
//
//  1. Stability: ordinary child create/write/remove inside the root never
//     changes it (trust must not be invalidated by normal project work).
//  2. Replacement sensitivity: rm -rf root && mkdir root yields a DIFFERENT
//     discriminator even when the filesystem reuses (st_dev, st_ino) — the
//     overlayfs failure mode the frozen (realpath, dev, ino) key cannot see.
//  3. Fail-closed capability: when no reliable kernel identity surface exists
//     the resolver returns ErrValidationUnsupported instead of a guessable
//     value.

func mkValRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestValidationIDIsWellFormed(t *testing.T) {
	root := mkValRoot(t)
	v, err := NewReplacementValidator().ValidationID(root)
	if err != nil {
		t.Fatalf("ValidationID on a fresh directory: %v", err)
	}
	if v.Scheme == "" || v.Value == "" {
		t.Fatalf("discriminator must carry an explicit scheme and value, got %+v", v)
	}
	if v.IsZero() {
		t.Fatal("well-formed discriminator reported IsZero")
	}
}

func TestValidationIDStableAcrossContentChanges(t *testing.T) {
	root := mkValRoot(t)
	val := NewReplacementValidator()
	before, err := val.ValidationID(root)
	if err != nil {
		t.Fatal(err)
	}

	// Ordinary project work: create, write, rewrite, and remove children.
	child := filepath.Join(root, "file.txt")
	if err := os.WriteFile(child, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(child, []byte("v2 rewritten"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "sub", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(child); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, "sub")); err != nil {
		t.Fatal(err)
	}

	after, err := val.ValidationID(root)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("ordinary content changes altered the discriminator: %+v -> %+v", before, after)
	}
}

func TestValidationIDChangesOnRootReplacement(t *testing.T) {
	root := mkValRoot(t)
	val := NewReplacementValidator()
	before, err := val.ValidationID(root)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	// Birth-time granularity is filesystem-dependent (ns on ext4/APFS); a tiny
	// sleep keeps the recreate out of any coarse timestamp bucket.
	time.Sleep(50 * time.Millisecond)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}

	after, err := val.ValidationID(root)
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatalf("replaced root kept the same discriminator %+v; replacement must be detectable even under inode reuse", before)
	}
}
