package snapshot

import (
	"encoding/json"
	"fmt"
	"strings"

	apiv1 "github.com/amux-run/amux/api/v1"
	"github.com/amux-run/amux/internal/domain"
	"github.com/amux-run/amux/internal/persist"
)

// GraphSchemaVersion is the graph-component schema this build writes and the
// newest it reads. Compatibility follows ADR-0005: a NEWER version is refused
// with ErrNewerSchema (touching nothing), an OLDER version is migrated forward
// exactly one step per version, in memory only — the on-disk generation is
// never rewritten by a load.
const GraphSchemaVersion = 1

// ReplayConfig carries the replay-ring bounds recorded with the checkpoint so
// a restore recreates rings of the committed capacity (ADR-0005 ComponentGraph
// "replay config").
type ReplayConfig struct {
	// PerSurfaceBytes is the raw-output ring capacity per surface.
	PerSurfaceBytes int64 `json:"per_surface_bytes"`
}

// SurfaceRuntime is the per-surface relaunch record inside the graph
// component: everything restore classification (persist.Classify) and an
// automatic relaunch need, and nothing more. Raw PTY bytes live in the replay
// sidecar, never here (ADR-0005 authority table).
type SurfaceRuntime struct {
	// Surface is the domain.SurfaceID as a string (identity is by opaque ID,
	// ADR-0002).
	Surface string `json:"surface"`
	// Argv is the recorded launch command; Argv[0] must still resolve for an
	// automatic restart.
	Argv []string `json:"argv,omitempty"`
	// Cwd is the recorded working directory.
	Cwd string `json:"cwd,omitempty"`
	// EnvAllowlist holds environment variable KEYS only — never values and
	// never secrets (ADR-0005 "non-secret env allowlist"). The restore path
	// resolves each key against the restoring environment. An entry containing
	// '=' would smuggle a value into durable state, so both the encoder and the
	// decoder reject it.
	EnvAllowlist []string `json:"env_allowlist,omitempty"`
	// RestartPolicy is the contract restart policy; empty decodes as the spec
	// default persist.RestartManual (restore never relaunches a manual surface).
	RestartPolicy persist.RestartPolicy `json:"restart_policy,omitempty"`
	// SidecarPath is the generation-relative replay-sidecar file holding this
	// surface's raw output, when one was captured.
	SidecarPath string `json:"sidecar_path,omitempty"`
	// SidecarOffset and SidecarLength delimit this surface's section inside the
	// replay-sidecar component. Every surface owns its own output sequence
	// space (first chunk = 1), so a shared flat chunk list would be ambiguous;
	// instead each surface's retained output is encoded as its own complete
	// sidecar stream and the streams are concatenated. A zero SidecarLength
	// means no raw output was captured for this surface.
	SidecarOffset int64 `json:"sidecar_offset,omitempty"`
	SidecarLength int64 `json:"sidecar_length,omitempty"`
	// ReplayNextSeq is the ring's next output sequence at capture time, so a
	// restore resumes allocation without reuse even when every retained chunk
	// was evicted (ADR-0004: sequences are never reallocated).
	ReplayNextSeq uint64 `json:"replay_next_seq,omitempty"`
}

// GraphDoc is the versioned JSON graph component (persist.ComponentGraph):
// the domain graph DTO plus surface runtimes, the event cursor for ADR-0004
// replay continuity, the committed notification checkpoint link, and replay
// configuration. It is a durable payload, so it decodes strictly — unknown
// fields are rejected (api/v1 DecodeStrict; PRD F10).
type GraphDoc struct {
	SchemaVersion int `json:"schema_version"`
	// Graph is the session graph DTO; domain.Rehydrate fail-closes on any
	// invariant violation when the caller rebuilds live state from it.
	Graph    *domain.Snapshot `json:"graph"`
	Surfaces []SurfaceRuntime `json:"surfaces,omitempty"`
	// EventCursor is the last event sequence covered by this checkpoint.
	EventCursor uint64 `json:"event_cursor"`
	// NotifyCheckpointID names the matching committed notification export —
	// the ONLY thing an explicit snapshot restore may import (ADR-0005).
	NotifyCheckpointID string       `json:"notify_checkpoint_id,omitempty"`
	ReplayConfig       ReplayConfig `json:"replay_config"`
}

// EncodeGraph serializes doc at the current schema version, enforcing the
// env-allowlist and restart-policy rules before any bytes exist. A zero
// SchemaVersion is stamped to the current version; any other non-current
// version is refused — the encoder never fabricates old or future schemas.
// The caller's doc is not mutated.
func EncodeGraph(doc *GraphDoc) ([]byte, error) {
	if doc == nil {
		return nil, fmt.Errorf("amux/snapshot: nil GraphDoc")
	}
	out := *doc
	out.Surfaces = append([]SurfaceRuntime(nil), doc.Surfaces...)
	if out.SchemaVersion == 0 {
		out.SchemaVersion = GraphSchemaVersion
	}
	if out.SchemaVersion != GraphSchemaVersion {
		return nil, fmt.Errorf("amux/snapshot: encoder writes graph schema %d only, got %d", GraphSchemaVersion, out.SchemaVersion)
	}
	if err := validateGraphDoc(&out); err != nil {
		return nil, err
	}
	return json.Marshal(&out)
}

// DecodeGraph decodes a graph component fail-closed per the ADR-0005 schema
// rules: the current version decodes strictly; a NEWER version returns
// ErrNewerSchema touching nothing; the legacy version 0 migrates forward one
// step in memory. Any validation or migration failure returns an error with no
// partial document, so the reader can preserve and report the previous
// known-good generation.
func DecodeGraph(data []byte) (*GraphDoc, error) {
	var probe struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("amux/snapshot: graph component is not valid JSON: %w", err)
	}
	switch {
	case probe.SchemaVersion > GraphSchemaVersion:
		return nil, fmt.Errorf("amux/snapshot: graph schema %d, this build reads up to %d: %w", probe.SchemaVersion, GraphSchemaVersion, ErrNewerSchema)
	case probe.SchemaVersion == GraphSchemaVersion:
		var doc GraphDoc
		if err := apiv1.DecodeStrict(data, &doc); err != nil {
			return nil, fmt.Errorf("amux/snapshot: graph schema %d strict decode: %w", GraphSchemaVersion, err)
		}
		if err := validateGraphDoc(&doc); err != nil {
			return nil, err
		}
		return &doc, nil
	case probe.SchemaVersion == 0:
		return migrateGraphV0(data)
	default:
		return nil, fmt.Errorf("amux/snapshot: invalid graph schema version %d", probe.SchemaVersion)
	}
}

// graphDocV0 is the legacy schema-0 graph layout, kept only as the migration
// seam ADR-0005 requires ("migrate older forward one step"). Version 0 carried
// the replay ring bound as a flat field; version 1 nests it in ReplayConfig.
// Each future schema bump adds exactly one more shim struct and one more
// forward step, so a very old snapshot migrates through every version in
// order, entirely in memory.
type graphDocV0 struct {
	SchemaVersion      int              `json:"schema_version"`
	Graph              *domain.Snapshot `json:"graph"`
	Surfaces           []SurfaceRuntime `json:"surfaces,omitempty"`
	EventCursor        uint64           `json:"event_cursor"`
	NotifyCheckpointID string           `json:"notify_checkpoint_id,omitempty"`
	// ReplayPerSurfaceBytes is the legacy flat form of
	// ReplayConfig.PerSurfaceBytes.
	ReplayPerSurfaceBytes int64 `json:"replay_per_surface_bytes,omitempty"`
}

// migrateGraphV0 performs the 0->1 forward migration in memory. A failure here
// commits no partial load: the caller (reader) records the diagnostic and
// falls back to the previous known-good generation (ADR-0005 "migration
// failure preserves and reports the previous known-good snapshot").
func migrateGraphV0(data []byte) (*GraphDoc, error) {
	var v0 graphDocV0
	if err := apiv1.DecodeStrict(data, &v0); err != nil {
		return nil, fmt.Errorf("amux/snapshot: migrating graph schema 0->1: strict decode: %w", err)
	}
	doc := &GraphDoc{
		SchemaVersion:      GraphSchemaVersion,
		Graph:              v0.Graph,
		Surfaces:           v0.Surfaces,
		EventCursor:        v0.EventCursor,
		NotifyCheckpointID: v0.NotifyCheckpointID,
		ReplayConfig:       ReplayConfig{PerSurfaceBytes: v0.ReplayPerSurfaceBytes},
	}
	if err := validateGraphDoc(doc); err != nil {
		return nil, fmt.Errorf("amux/snapshot: migrating graph schema 0->1: %w", err)
	}
	return doc, nil
}

// validateGraphDoc enforces the durable-payload rules shared by encode, strict
// decode, and migration. It normalizes an absent restart policy to the spec
// default (manual) and fail-closes on everything else.
func validateGraphDoc(doc *GraphDoc) error {
	if doc.Graph == nil {
		return fmt.Errorf("amux/snapshot: graph document lacks the session graph")
	}
	if doc.ReplayConfig.PerSurfaceBytes < 0 {
		return fmt.Errorf("amux/snapshot: negative replay per-surface bound %d", doc.ReplayConfig.PerSurfaceBytes)
	}
	for i := range doc.Surfaces {
		s := &doc.Surfaces[i]
		if s.Surface == "" {
			return fmt.Errorf("amux/snapshot: surface runtime %d has empty surface id", i)
		}
		switch s.RestartPolicy {
		case "":
			// Spec default: restore never relaunches without an explicit policy.
			s.RestartPolicy = persist.RestartManual
		case persist.RestartManual, persist.RestartAutomatic:
		default:
			return fmt.Errorf("amux/snapshot: surface %s has unknown restart policy %q", s.Surface, s.RestartPolicy)
		}
		if err := validateEnvAllowlist(s.Surface, s.EnvAllowlist); err != nil {
			return err
		}
		if s.SidecarOffset < 0 || s.SidecarLength < 0 {
			return fmt.Errorf("amux/snapshot: surface %s has a negative sidecar section (offset %d, length %d)", s.Surface, s.SidecarOffset, s.SidecarLength)
		}
	}
	return nil
}

// validateEnvAllowlist rejects any allowlist entry that is not a bare
// environment variable KEY. Entries containing '=' carry values — potentially
// secrets — and are refused at encode AND decode so no code path can persist
// or accept them (ADR-0005 "non-secret env allowlist").
func validateEnvAllowlist(surface string, keys []string) error {
	for _, k := range keys {
		if k == "" {
			return fmt.Errorf("amux/snapshot: surface %s env allowlist has an empty key", surface)
		}
		if strings.ContainsRune(k, '=') {
			return fmt.Errorf("amux/snapshot: surface %s env allowlist entry %q contains '=': the allowlist holds keys only, never values", surface, k)
		}
	}
	return nil
}
