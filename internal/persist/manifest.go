package persist

// This file freezes the checkpoint/manifest STRUCTURE and the commit ordering
// that makes a snapshot generation atomic (ADR-0005). Byte-level codecs live in
// internal/snapshot (T4 B8); the shapes and the ordering rule are the contract.

// Authority names which subsystem is the canonical writer of a class of data.
// Restore may only import data whose Authority permits it — in particular,
// security state (trust epochs, grants, audit) is SQLite-only and is NEVER
// imported from a layout snapshot (ADR-0005 / PRD "Data ownership and
// durability").
type Authority string

const (
	// AuthoritySessionLoop: the per-session event loop owns graph + stable IDs.
	AuthoritySessionLoop Authority = "session_loop"
	// AuthorityControlActor: the daemon-global actor owns session registry and
	// project trust epochs/grants/audit (SQLite-only, never snapshot-restored).
	AuthorityControlActor Authority = "control_actor"
	// AuthoritySurfaceRing: a surface's replay ring owns raw output bytes.
	AuthoritySurfaceRing Authority = "surface_ring"
	// AuthorityNotifyService: the notification service owns notification records.
	AuthorityNotifyService Authority = "notify_service"
)

// ComponentKind identifies a checkpoint component within a generation.
type ComponentKind string

const (
	// ComponentGraph is the versioned JSON graph manifest (tree, IDs, cwd, argv,
	// env allowlist, restart policy, replay config, notification checkpoint,
	// event cursor).
	ComponentGraph ComponentKind = "graph"
	// ComponentReplaySidecar is a versioned binary raw-output sidecar; raw bytes
	// are NEVER base64-embedded in the graph JSON (PRD F7).
	ComponentReplaySidecar ComponentKind = "replay_sidecar"
	// ComponentNotifyExport is the logical notification/read-state export (a
	// snapshot may import ONLY this, and only the matching committed checkpoint).
	ComponentNotifyExport ComponentKind = "notify_export"
)

// ComponentRef is a checksummed pointer to one on-disk component of a generation.
type ComponentRef struct {
	Kind      ComponentKind `json:"kind"`
	Path      string        `json:"path"`   // generation-relative
	SHA256    string        `json:"sha256"` // hex digest of the component bytes
	SizeBytes int64         `json:"size_bytes"`
}

// Manifest is the top-level record of one checkpoint generation. The manifest
// file's atomic rename is THE commit point: components are written, checksummed,
// and fsync'd first; the manifest is renamed last. A generation whose manifest
// is absent or fails checksum validation is ignored and the prior committed
// generation remains usable (ADR-0005 previous-known-good rule).
type Manifest struct {
	SchemaVersion int            `json:"schema_version"`
	CheckpointID  string         `json:"checkpoint_id"` // unique per generation (UUIDv7)
	Session       string         `json:"session"`
	CreatedUnixMS int64          `json:"created_unix_ms"`
	Components    []ComponentRef `json:"components"`
	// PrevCheckpointID links to the retained previous-known-good generation.
	PrevCheckpointID string `json:"prev_checkpoint_id,omitempty"`
}

// CommitOrder documents (as executable constants) the exact fsync/rename
// sequence B8 must follow. It is referenced by the ADR and by the B8 test that
// asserts each step happened in order under fault injection.
//
// The steps, in order:
//
//  1. Write every component to a temp file in the generation temp dir.
//  2. fsync each component file.
//  3. Compute and record each component's SHA-256 in the manifest.
//  4. Write the manifest to a temp file and fsync it.
//  5. Atomically rename the manifest temp file into place (THE COMMIT POINT).
//  6. fsync the generation directory so the rename is durable.
//  7. Only now retire the generation older than previous-known-good.
//
// Any crash before step 5 leaves the prior committed generation authoritative;
// a partial temp generation is ignored on next open.
var CommitOrder = []string{
	"write_components_temp",
	"fsync_components",
	"checksum_components",
	"write_manifest_temp_and_fsync",
	"atomic_rename_manifest", // commit point
	"fsync_generation_dir",
	"retire_older_than_previous_known_good",
}
