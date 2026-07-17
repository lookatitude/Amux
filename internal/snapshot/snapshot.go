// Package snapshot implements the atomic checkpoint-generation store for a
// session (ADR-0005; T4 B8), strictly against the internal/persist contract
// types. Each generation lives at <root>/<session>/gen-<checkpointID>/ (the
// CheckpointID is a UUIDv7, so lexicographic directory order is chronological)
// and holds the component files plus manifest.json. The manifest's atomic
// rename is THE commit point: Commit follows persist.CommitOrder step by step,
// so a crash at any earlier step leaves the prior committed generation
// authoritative and the partial temp generation ignored. OpenLatest is the
// fail-closed reader: it loads the newest generation whose manifest parses and
// whose every component validates against its SHA-256 and size, skips damaged
// generations with a recorded diagnostic (the previous-known-good rule), and
// refuses — never partially loads — when a generation was written by a newer
// schema or when nothing validates.
//
// All I/O flows through the FS seam so the B8 fault-injection tests can crash
// the writer at every commit step and prove the ordering guarantees; production
// uses the real OS filesystem.
package snapshot

import (
	"errors"
	"io/fs"
	"os"
)

// ErrNewerSchema is the typed refusal for durable state written by a NEWER
// build. Amux refuses newer schemas outright — it never migrates backward and
// never silently falls back to older state a newer build superseded — and the
// refusal touches nothing on disk (ADR-0005 schema compatibility: refuse
// newer, migrate older forward one step).
var ErrNewerSchema = errors.New("amux/snapshot: state written by a newer schema; refusing to load")

// ErrNoValidGeneration is the typed refusal when no checkpoint generation
// validates end-to-end. The reader never fabricates or partially loads state;
// the accompanying LoadReport carries the per-generation diagnostics
// (ADR-0005 "reject partial/corrupt generations").
var ErrNoValidGeneration = errors.New("amux/snapshot: no valid checkpoint generation")

// ErrSidecarCorrupt is the typed refusal for a replay sidecar that is
// truncated, oversized, or otherwise malformed. Decoding fails closed: no
// partial chunk list is ever returned (ADR-0005 replay sidecar rules).
var ErrSidecarCorrupt = errors.New("amux/snapshot: replay sidecar corrupt")

// FS is the injectable filesystem seam every snapshot read and write goes
// through. It exists so the B8 fault-injection tests (ADR-0005 follow-ups; T6
// Q4) can record the exact operation order and crash the writer at any
// persist.CommitOrder step; production code uses OSFS. The surface is
// deliberately the minimal set of operations the commit ordering names.
type FS interface {
	// MkdirAll creates the generation directory tree.
	MkdirAll(path string, perm fs.FileMode) error
	// WriteFile writes a whole component or manifest file.
	WriteFile(path string, data []byte, perm fs.FileMode) error
	// Fsync flushes a file — or a directory, to make a rename inside it
	// durable — to stable storage (ADR-0005 steps 2, 4, and 6).
	Fsync(path string) error
	// Rename atomically renames the manifest into place (ADR-0005 step 5,
	// THE COMMIT POINT).
	Rename(oldpath, newpath string) error
	// RemoveAll retires a whole generation directory (ADR-0005 step 7).
	RemoveAll(path string) error
	// ReadFile reads a manifest or component for validation.
	ReadFile(path string) ([]byte, error)
	// ReadDir lists a session directory's generations.
	ReadDir(path string) ([]fs.DirEntry, error)
}

// OSFS returns the production FS backed by the real operating-system
// filesystem.
func OSFS() FS { return osFS{} }

type osFS struct{}

func (osFS) MkdirAll(path string, perm fs.FileMode) error { return os.MkdirAll(path, perm) }

func (osFS) WriteFile(path string, data []byte, perm fs.FileMode) error {
	return os.WriteFile(path, data, perm)
}

// Fsync opens the path (file or directory) and flushes it. Directory fsync is
// what makes the manifest rename durable on POSIX filesystems.
func (osFS) Fsync(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr := f.Sync()
	closeErr := f.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

func (osFS) Rename(oldpath, newpath string) error { return os.Rename(oldpath, newpath) }

func (osFS) RemoveAll(path string) error { return os.RemoveAll(path) }

func (osFS) ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }

func (osFS) ReadDir(path string) ([]fs.DirEntry, error) { return os.ReadDir(path) }
