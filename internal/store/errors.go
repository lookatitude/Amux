package store

import "errors"

// Typed sentinel errors so callers branch with errors.Is instead of string
// matching. Each names the ADR-0005 rule it fails closed on.
var (
	// ErrNewerSchema reports a database whose recorded schema version is newer
	// than this binary supports. Migrations are forward-only at runtime
	// (ADR-0005); the store refuses to open rather than downgrade or write
	// partially.
	ErrNewerSchema = errors.New("store: database schema is newer than this binary supports")

	// ErrEpochNotMonotonic reports a SetProjectState whose new epoch is not
	// strictly greater than the stored epoch. Epochs never decrease
	// (ADR-0005 non-rollback of trust); the storage layer enforces this
	// independently of the control actor.
	ErrEpochNotMonotonic = errors.New("store: project epoch must strictly increase")

	// ErrCheckpointMismatch reports a notification import whose checkpoint id
	// does not match the committed checkpoint recorded in metadata. A snapshot
	// restore may import ONLY the export matching a committed checkpoint
	// (ADR-0005 notification recovery rule).
	ErrCheckpointMismatch = errors.New("store: notification import checkpoint does not match the committed checkpoint")

	// ErrImportInvalid reports a notification export payload that fails strict
	// decoding — wrong version, malformed JSON, or unknown fields (which is how
	// forged trust fields in a crafted export are rejected before any write).
	ErrImportInvalid = errors.New("store: notification export payload is invalid")

	// ErrInvalidState reports a SetProjectState with a state outside
	// approved|denied|revoked.
	ErrInvalidState = errors.New("store: invalid project state")

	// ErrNotFound reports a lookup (project, grant, notification, meta key,
	// cursor) with no matching row.
	ErrNotFound = errors.New("store: not found")
)
