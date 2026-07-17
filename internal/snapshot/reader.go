package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	apiv1 "github.com/amux-run/amux/api/v1"
	"github.com/amux-run/amux/internal/persist"
)

// RejectedGeneration is one diagnostic entry in a LoadReport: a generation the
// reader skipped, and exactly why (ADR-0005 requires the refusal to be
// exportable, never silent).
type RejectedGeneration struct {
	// Dir is the generation directory name (gen-<id>).
	Dir string
	// Reason is the validation failure that disqualified the generation.
	Reason string
}

// LoadReport records what OpenLatest did: which generation loaded (empty on
// refusal) and every generation it rejected, newest first, with reasons. It is
// the diagnostic half of the previous-known-good rule — a fallback is always
// visible, never silent.
type LoadReport struct {
	LoadedCheckpointID string
	LoadedDir          string
	Rejected           []RejectedGeneration
}

// Loaded is one fully validated, decoded checkpoint generation.
type Loaded struct {
	Manifest persist.Manifest
	// Graph is the decoded (and, for legacy versions, forward-migrated) graph
	// document. domain.Rehydrate fail-closes on graph invariants when the
	// caller rebuilds live state.
	Graph *GraphDoc
	// components holds every checksum-verified component's bytes.
	components map[persist.ComponentKind][]byte
}

// Component returns the verified raw bytes of one component. Callers must not
// mutate the returned slice.
func (l *Loaded) Component(kind persist.ComponentKind) ([]byte, bool) {
	b, ok := l.components[kind]
	return b, ok
}

// ReplayChunks decodes the replay sidecar, failing closed on any framing
// corruption. A generation without a sidecar yields no chunks and no error —
// the replay gap is explicit, not invented (ADR-0005 authority table).
func (l *Loaded) ReplayChunks() ([]Chunk, error) {
	b, ok := l.components[persist.ComponentReplaySidecar]
	if !ok {
		return nil, nil
	}
	return DecodeSidecar(b)
}

// SurfaceReplayChunks decodes one surface's own section of the replay sidecar
// (SidecarOffset/SidecarLength), failing closed when the recorded section falls
// outside the verified component or its framing is corrupt. A surface with no
// recorded section yields no chunks and no error — an absent capture is an
// explicit replay gap, not an invented one (ADR-0005 authority table).
func (l *Loaded) SurfaceReplayChunks(sr SurfaceRuntime) ([]Chunk, error) {
	if sr.SidecarLength == 0 {
		return nil, nil
	}
	b, ok := l.components[persist.ComponentReplaySidecar]
	if !ok {
		return nil, fmt.Errorf("amux/snapshot: surface %s records a sidecar section but the generation has no %q component: %w", sr.Surface, persist.ComponentReplaySidecar, ErrSidecarCorrupt)
	}
	end := sr.SidecarOffset + sr.SidecarLength
	if end < sr.SidecarOffset || end > int64(len(b)) {
		return nil, fmt.Errorf("amux/snapshot: surface %s sidecar section [%d, %d) exceeds the %d-byte component: %w", sr.Surface, sr.SidecarOffset, end, len(b), ErrSidecarCorrupt)
	}
	return DecodeSidecar(b[sr.SidecarOffset:end])
}

// NotifyExport returns the opaque notification-export bytes, produced and
// consumed by internal/store. This package only persists and checksums them:
// the export is the ONLY thing an explicit snapshot restore may import, and it
// can never touch security state (ADR-0005 SQLite precedence).
func (l *Loaded) NotifyExport() ([]byte, bool) {
	b, ok := l.components[persist.ComponentNotifyExport]
	return b, ok
}

// Reader opens checkpoint generations. The zero value uses the real OS
// filesystem; FS is a seam for tests.
type Reader struct {
	FS FS
}

func (r *Reader) fsys() FS {
	if r.FS != nil {
		return r.FS
	}
	return osFS{}
}

// OpenLatest opens session's newest valid checkpoint using the default Reader.
func OpenLatest(root, session string) (*Loaded, LoadReport, error) {
	return (&Reader{}).OpenLatest(root, session)
}

// OpenLatest scans <root>/<session> newest-first and returns the first
// generation whose manifest parses strictly AND whose every component matches
// its manifest SHA-256 and size, with the graph document decoded. Its behavior
// is the executable form of the ADR-0005 recovery rules:
//
//   - A generation with a missing/corrupt manifest or a failed component check
//     is SKIPPED with a diagnostic in the LoadReport, and the previous
//     known-good generation is used — including a legacy graph document whose
//     forward migration fails.
//   - A generation written by a NEWER schema (manifest or graph) returns the
//     typed ErrNewerSchema refusal immediately, touching nothing on disk:
//     falling back to older state that a newer build superseded would silently
//     roll the session back.
//   - When no generation validates, the typed ErrNoValidGeneration refusal is
//     returned with the full diagnostic report — never a partial load.
func (r *Reader) OpenLatest(root, session string) (*Loaded, LoadReport, error) {
	var report LoadReport
	fsys := r.fsys()
	sessionDir := filepath.Join(root, session)
	entries, err := fsys.ReadDir(sessionDir)
	if err != nil {
		return nil, report, fmt.Errorf("amux/snapshot: session %q: reading %s: %v: %w", session, sessionDir, err, ErrNoValidGeneration)
	}
	for _, name := range genDirNamesNewestFirst(entries) {
		m, comps, doc, err := loadGeneration(fsys, filepath.Join(sessionDir, name), name, session)
		if errors.Is(err, ErrNewerSchema) {
			return nil, report, fmt.Errorf("amux/snapshot: generation %s: %w", name, err)
		}
		if err != nil {
			report.Rejected = append(report.Rejected, RejectedGeneration{Dir: name, Reason: err.Error()})
			continue
		}
		report.LoadedCheckpointID = m.CheckpointID
		report.LoadedDir = name
		return &Loaded{Manifest: *m, Graph: doc, components: comps}, report, nil
	}
	return nil, report, fmt.Errorf("amux/snapshot: session %q has no loadable generation (%d rejected): %w", session, len(report.Rejected), ErrNoValidGeneration)
}

// genDirNamesNewestFirst filters generation directories and sorts them newest
// first. CheckpointIDs are UUIDv7, so descending lexicographic order of the
// gen-<id> names is descending commit time.
func genDirNamesNewestFirst(entries []fs.DirEntry) []string {
	var names []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), genPrefix) {
			names = append(names, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	return names
}

// loadGeneration validates and decodes one generation end-to-end: strict
// manifest decode, identity cross-checks, per-component size and SHA-256
// verification, and graph decode/migration. Any failure returns with no
// partial result; only an ErrNewerSchema failure is a hard refusal, everything
// else is a skip-with-diagnostic for the caller.
func loadGeneration(fsys FS, dir, name, session string) (*persist.Manifest, map[persist.ComponentKind][]byte, *GraphDoc, error) {
	raw, err := fsys.ReadFile(filepath.Join(dir, ManifestName))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("manifest unreadable (uncommitted or damaged generation): %v", err)
	}
	var probe struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, nil, nil, fmt.Errorf("manifest is not valid JSON: %v", err)
	}
	if probe.SchemaVersion > ManifestSchemaVersion {
		return nil, nil, nil, fmt.Errorf("manifest schema %d, this build reads up to %d: %w", probe.SchemaVersion, ManifestSchemaVersion, ErrNewerSchema)
	}
	var m persist.Manifest
	if err := apiv1.DecodeStrict(raw, &m); err != nil {
		return nil, nil, nil, fmt.Errorf("manifest strict decode: %v", err)
	}
	if m.SchemaVersion != ManifestSchemaVersion {
		return nil, nil, nil, fmt.Errorf("manifest schema %d is not the supported version %d", m.SchemaVersion, ManifestSchemaVersion)
	}
	if genPrefix+m.CheckpointID != name {
		return nil, nil, nil, fmt.Errorf("manifest checkpoint id %q does not match directory %q", m.CheckpointID, name)
	}
	if m.Session != session {
		return nil, nil, nil, fmt.Errorf("manifest session %q does not match %q", m.Session, session)
	}

	comps := make(map[persist.ComponentKind][]byte, len(m.Components))
	for _, ref := range m.Components {
		if ref.Path == "" || ref.Path != filepath.Base(ref.Path) || ref.Path == "." || ref.Path == ".." {
			return nil, nil, nil, fmt.Errorf("component %s has non-local path %q", ref.Kind, ref.Path)
		}
		b, err := fsys.ReadFile(filepath.Join(dir, ref.Path))
		if err != nil {
			return nil, nil, nil, fmt.Errorf("component %s unreadable: %v", ref.Kind, err)
		}
		if int64(len(b)) != ref.SizeBytes {
			return nil, nil, nil, fmt.Errorf("component %s size %d does not match manifest size %d", ref.Kind, len(b), ref.SizeBytes)
		}
		sum := sha256.Sum256(b)
		if hex.EncodeToString(sum[:]) != strings.ToLower(ref.SHA256) {
			return nil, nil, nil, fmt.Errorf("component %s sha256 mismatch against manifest", ref.Kind)
		}
		comps[ref.Kind] = b
	}

	graphRaw, ok := comps[persist.ComponentGraph]
	if !ok {
		return nil, nil, nil, fmt.Errorf("generation lacks the %q component", persist.ComponentGraph)
	}
	// %w preserves ErrNewerSchema so the caller can distinguish the hard
	// refusal from a skip-with-diagnostic (e.g. a failed 0->1 migration).
	doc, err := DecodeGraph(graphRaw)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("graph component: %w", err)
	}
	return &m, comps, doc, nil
}
