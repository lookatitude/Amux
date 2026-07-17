package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/amux-run/amux/internal/persist"
	"github.com/google/uuid"
)

// ManifestSchemaVersion is the persist.Manifest schema this build writes and
// the newest it reads; a newer manifest schema is refused with ErrNewerSchema
// (ADR-0005 "refuse newer").
const ManifestSchemaVersion = 1

// ManifestName is the committed manifest file inside a generation directory.
// Its presence — created only by the atomic rename — is what makes a
// generation committed.
const ManifestName = "manifest.json"

// manifestTempName is the pre-commit manifest staging file (ADR-0005 step 4).
const manifestTempName = "manifest.json.tmp"

// genPrefix prefixes every generation directory: gen-<CheckpointID>. Because
// CheckpointIDs are UUIDv7, lexicographic order of directory names is
// chronological commit order.
const genPrefix = "gen-"

// ComponentFileName maps a component kind to its generation-relative file
// name. The graph is human-inspectable JSON; the other components are opaque
// or binary.
func ComponentFileName(kind persist.ComponentKind) string {
	if kind == persist.ComponentGraph {
		return "graph.json"
	}
	return string(kind) + ".bin"
}

// Writer commits checkpoint generations. The zero value is production-ready
// (real OS filesystem, real clock, real UUIDv7 source); every dependency is a
// seam so the B8 fault-injection tests can record the step order, freeze the
// clock, and crash the filesystem at any persist.CommitOrder step.
type Writer struct {
	// FS is the filesystem seam; nil means the real OS filesystem.
	FS FS
	// Observe, when non-nil, is called with each persist.CommitOrder step name
	// at the moment that step begins. The step-order test asserts the observed
	// sequence equals persist.CommitOrder exactly.
	Observe func(step string)
	// Now returns the commit wall-clock time in Unix milliseconds; nil means
	// time.Now.
	Now func() int64
	// NewCheckpointID mints the generation's CheckpointID; nil means a UUIDv7
	// (ADR-0005 "unique CheckpointID (UUIDv7)").
	NewCheckpointID func() (string, error)
}

func (w *Writer) fsys() FS {
	if w.FS != nil {
		return w.FS
	}
	return osFS{}
}

func (w *Writer) step(name string) {
	if w.Observe != nil {
		w.Observe(name)
	}
}

func (w *Writer) now() int64 {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now().UnixMilli()
}

func (w *Writer) newCheckpointID() (string, error) {
	if w.NewCheckpointID != nil {
		return w.NewCheckpointID()
	}
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("amux/snapshot: minting checkpoint id: %w", err)
	}
	return id.String(), nil
}

// Commit writes one checkpoint generation for session under root using the
// default Writer (real OS filesystem).
func Commit(root, session string, components map[persist.ComponentKind][]byte) (*persist.Manifest, error) {
	return (&Writer{}).Commit(root, session, components)
}

// Commit atomically persists one checkpoint generation, following EXACTLY the
// persist.CommitOrder sequence (ADR-0005):
//
//  1. write every component into the new generation directory,
//  2. fsync each component file,
//  3. compute each component's SHA-256 into the manifest's ComponentRefs,
//  4. write the manifest temp file and fsync it,
//  5. atomically rename the manifest into place — THE COMMIT POINT,
//  6. fsync the generation directory (and the session directory, so the new
//     directory entry itself is durable),
//  7. only now retire generations older than previous-known-good.
//
// A failure before step 5 leaves the prior committed generation authoritative;
// the partial directory has no manifest and is ignored (and later swept) —
// Commit deliberately does not clean it up, because a real crash could not
// either. A failure at step 6 or 7 returns an error even though the commit
// point has passed: the caller learns durability/retirement was not confirmed,
// while OpenLatest will already prefer the new generation.
//
// PrevCheckpointID links the retained previous-known-good generation: the
// newest existing generation that fully validates (manifest, checksums, and
// graph decode), determined before writing. Retention keeps exactly the new
// generation plus that previous one and removes every other gen-* directory,
// including orphaned partials from crashed commits.
func (w *Writer) Commit(root, session string, components map[persist.ComponentKind][]byte) (*persist.Manifest, error) {
	if root == "" || session == "" {
		return nil, fmt.Errorf("amux/snapshot: empty root or session")
	}
	if _, ok := components[persist.ComponentGraph]; !ok {
		return nil, fmt.Errorf("amux/snapshot: a generation requires the %q component", persist.ComponentGraph)
	}
	fsys := w.fsys()
	sessionDir := filepath.Join(root, session)

	// Identify the previous known-good generation BEFORE writing anything, so
	// the new manifest can link it and retention can retain it.
	prevID := previousKnownGood(fsys, sessionDir, session)

	id, err := w.newCheckpointID()
	if err != nil {
		return nil, err
	}
	genDir := filepath.Join(sessionDir, genPrefix+id)

	// Deterministic component order keeps the fault-injection step accounting
	// and the manifest byte-stable.
	kinds := make([]persist.ComponentKind, 0, len(components))
	for kind := range components {
		kinds = append(kinds, kind)
	}
	sort.Slice(kinds, func(i, j int) bool { return kinds[i] < kinds[j] })

	// Step 1: write every component into the (uncommitted) generation dir.
	w.step(persist.CommitOrder[0])
	if err := fsys.MkdirAll(genDir, fs.FileMode(0o700)); err != nil {
		return nil, fmt.Errorf("amux/snapshot: creating generation dir: %w", err)
	}
	for _, kind := range kinds {
		path := filepath.Join(genDir, ComponentFileName(kind))
		if err := fsys.WriteFile(path, components[kind], fs.FileMode(0o600)); err != nil {
			return nil, fmt.Errorf("amux/snapshot: writing component %s: %w", kind, err)
		}
	}

	// Step 2: fsync each component file.
	w.step(persist.CommitOrder[1])
	for _, kind := range kinds {
		if err := fsys.Fsync(filepath.Join(genDir, ComponentFileName(kind))); err != nil {
			return nil, fmt.Errorf("amux/snapshot: fsync component %s: %w", kind, err)
		}
	}

	// Step 3: compute and record each component's SHA-256.
	w.step(persist.CommitOrder[2])
	refs := make([]persist.ComponentRef, 0, len(kinds))
	for _, kind := range kinds {
		sum := sha256.Sum256(components[kind])
		refs = append(refs, persist.ComponentRef{
			Kind:      kind,
			Path:      ComponentFileName(kind),
			SHA256:    hex.EncodeToString(sum[:]),
			SizeBytes: int64(len(components[kind])),
		})
	}
	manifest := &persist.Manifest{
		SchemaVersion:    ManifestSchemaVersion,
		CheckpointID:     id,
		Session:          session,
		CreatedUnixMS:    w.now(),
		Components:       refs,
		PrevCheckpointID: prevID,
	}

	// Step 4: write the manifest temp file and fsync it.
	w.step(persist.CommitOrder[3])
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("amux/snapshot: encoding manifest: %w", err)
	}
	tempPath := filepath.Join(genDir, manifestTempName)
	if err := fsys.WriteFile(tempPath, manifestBytes, fs.FileMode(0o600)); err != nil {
		return nil, fmt.Errorf("amux/snapshot: writing manifest temp: %w", err)
	}
	if err := fsys.Fsync(tempPath); err != nil {
		return nil, fmt.Errorf("amux/snapshot: fsync manifest temp: %w", err)
	}

	// Step 5: atomic rename — THE COMMIT POINT.
	w.step(persist.CommitOrder[4])
	if err := fsys.Rename(tempPath, filepath.Join(genDir, ManifestName)); err != nil {
		return nil, fmt.Errorf("amux/snapshot: committing manifest rename: %w", err)
	}

	// Step 6: make the rename (and the new directory entry) durable.
	w.step(persist.CommitOrder[5])
	if err := fsys.Fsync(genDir); err != nil {
		return nil, fmt.Errorf("amux/snapshot: checkpoint %s renamed into place but generation-dir fsync failed (durability unconfirmed): %w", id, err)
	}
	if err := fsys.Fsync(sessionDir); err != nil {
		return nil, fmt.Errorf("amux/snapshot: checkpoint %s renamed into place but session-dir fsync failed (durability unconfirmed): %w", id, err)
	}

	// Step 7: only after the rename is durable, retire everything older than
	// previous-known-good (keep current + previous).
	w.step(persist.CommitOrder[6])
	entries, err := fsys.ReadDir(sessionDir)
	if err != nil {
		return nil, fmt.Errorf("amux/snapshot: checkpoint %s committed but retirement scan failed: %w", id, err)
	}
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || !strings.HasPrefix(name, genPrefix) {
			continue
		}
		if name == genPrefix+id || (prevID != "" && name == genPrefix+prevID) {
			continue
		}
		if err := fsys.RemoveAll(filepath.Join(sessionDir, name)); err != nil {
			return nil, fmt.Errorf("amux/snapshot: checkpoint %s committed but retiring %s failed: %w", id, name, err)
		}
	}
	return manifest, nil
}

// previousKnownGood returns the CheckpointID of the newest generation that
// fully validates — the ADR-0005 previous-known-good. Damaged, partial, or
// unreadable generations are simply not candidates; an empty result means this
// commit has no predecessor to link or retain.
func previousKnownGood(fsys FS, sessionDir, session string) string {
	entries, err := fsys.ReadDir(sessionDir)
	if err != nil {
		return "" // first commit for this session
	}
	for _, name := range genDirNamesNewestFirst(entries) {
		m, _, _, err := loadGeneration(fsys, filepath.Join(sessionDir, name), name, session)
		if err == nil {
			return m.CheckpointID
		}
	}
	return ""
}
