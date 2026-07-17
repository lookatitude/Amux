package snapshot

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/amux-run/amux/internal/domain"
	"github.com/amux-run/amux/internal/persist"
)

func testGraphDoc() *GraphDoc {
	return &GraphDoc{
		SchemaVersion: GraphSchemaVersion,
		Graph: &domain.Snapshot{
			Session: "sess-1",
			Rev:     9,
			Workspaces: []domain.SnapshotWorkspace{{
				ID:           "ws-1",
				Name:         "main",
				PrimaryRoot:  "/home/u/proj",
				Root:         &domain.SnapshotNode{Pane: "pane-1"},
				Panes:        []domain.SnapshotPane{{ID: "pane-1", Cwd: "/home/u/proj", Surfaces: []domain.SnapshotSurface{{ID: "srf-1", Title: "sh"}}, Active: "srf-1"}},
				Focused:      "pane-1",
				FocusHistory: []domain.PaneID{"pane-1"},
				Rev:          4,
			}},
		},
		Surfaces: []SurfaceRuntime{{
			Surface:       "srf-1",
			Argv:          []string{"bash", "-l"},
			Cwd:           "/home/u/proj",
			EnvAllowlist:  []string{"PATH", "HOME", "TERM"},
			RestartPolicy: persist.RestartManual,
			SidecarPath:   "replay_sidecar.bin",
		}},
		EventCursor:        42,
		NotifyCheckpointID: "nc-7",
		ReplayConfig:       ReplayConfig{PerSurfaceBytes: 1 << 20},
	}
}

// TestGraphRoundtrip: encode/decode of the current schema is lossless.
func TestGraphRoundtrip(t *testing.T) {
	in := testGraphDoc()
	enc, err := EncodeGraph(in)
	if err != nil {
		t.Fatalf("EncodeGraph: %v", err)
	}
	out, err := DecodeGraph(enc)
	if err != nil {
		t.Fatalf("DecodeGraph: %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("roundtrip mismatch:\n in=%+v\nout=%+v", in, out)
	}
}

// TestGraphEnvAllowlistRejectsValues: the persisted env allowlist is KEYS only
// (ADR-0005 "non-secret env allowlist"); an entry containing '=' would smuggle
// a value (potentially a secret) into the snapshot, so both the encoder and the
// decoder fail closed on it, and on empty keys.
func TestGraphEnvAllowlistRejectsValues(t *testing.T) {
	doc := testGraphDoc()
	doc.Surfaces[0].EnvAllowlist = []string{"PATH", "SECRET=hunter2"}
	if _, err := EncodeGraph(doc); err == nil || !strings.Contains(err.Error(), "allowlist") {
		t.Fatalf("EncodeGraph accepted env entry with '=': err=%v", err)
	}

	doc.Surfaces[0].EnvAllowlist = []string{""}
	if _, err := EncodeGraph(doc); err == nil {
		t.Fatal("EncodeGraph accepted empty env allowlist key")
	}

	// Decode-side enforcement: bytes that bypassed our encoder still fail.
	good := testGraphDoc()
	enc, err := EncodeGraph(good)
	if err != nil {
		t.Fatal(err)
	}
	bad := strings.Replace(string(enc), `"PATH"`, `"PATH=/evil"`, 1)
	if _, err := DecodeGraph([]byte(bad)); err == nil || !strings.Contains(err.Error(), "allowlist") {
		t.Fatalf("DecodeGraph accepted env entry with '=': err=%v", err)
	}
}

// TestGraphDecodeRejectsUnknownFields: durable payloads decode strictly
// (api/v1 DecodeStrict; PRD F10 unknown-field policy).
func TestGraphDecodeRejectsUnknownFields(t *testing.T) {
	enc, err := EncodeGraph(testGraphDoc())
	if err != nil {
		t.Fatal(err)
	}
	withUnknown := strings.Replace(string(enc), `{"schema_version"`, `{"trojan":1,"schema_version"`, 1)
	if _, err := DecodeGraph([]byte(withUnknown)); err == nil {
		t.Fatal("DecodeGraph accepted an unknown field")
	}
}

// TestGraphDecodeNewerSchemaRefuses: a graph written by a newer build is
// refused with the typed ErrNewerSchema — never migrated backward, never
// partially loaded (ADR-0005 schema compatibility).
func TestGraphDecodeNewerSchemaRefuses(t *testing.T) {
	_, err := DecodeGraph([]byte(`{"schema_version":2}`))
	if !errors.Is(err, ErrNewerSchema) {
		t.Fatalf("err = %v, want ErrNewerSchema", err)
	}
}

// TestGraphDecodeRejectsUnknownRestartPolicy: only the two contract policies
// (persist.RestartManual/RestartAutomatic) plus the empty default are decodable.
func TestGraphDecodeRejectsUnknownRestartPolicy(t *testing.T) {
	enc, err := EncodeGraph(testGraphDoc())
	if err != nil {
		t.Fatal(err)
	}
	bad := strings.Replace(string(enc), `"manual"`, `"yolo"`, 1)
	if _, err := DecodeGraph([]byte(bad)); err == nil {
		t.Fatal("DecodeGraph accepted unknown restart policy")
	}
}

// TestGraphV0MigratesForward: a legacy schema-0 document migrates forward one
// step, in memory only, to the current schema (ADR-0005 "migrate older forward
// one step"). The legacy flat replay_per_surface_bytes moves into ReplayConfig.
func TestGraphV0MigratesForward(t *testing.T) {
	v0 := []byte(`{"schema_version":0,"graph":{"session":"s1","rev":3},"surfaces":[{"surface":"srf-1","argv":["bash"],"cwd":"/tmp","env_allowlist":["PATH","HOME"],"restart_policy":"automatic"}],"event_cursor":42,"notify_checkpoint_id":"nc-1","replay_per_surface_bytes":4096}`)
	doc, err := DecodeGraph(v0)
	if err != nil {
		t.Fatalf("DecodeGraph(v0): %v", err)
	}
	if doc.SchemaVersion != GraphSchemaVersion {
		t.Errorf("migrated SchemaVersion = %d, want %d", doc.SchemaVersion, GraphSchemaVersion)
	}
	if doc.ReplayConfig.PerSurfaceBytes != 4096 {
		t.Errorf("ReplayConfig.PerSurfaceBytes = %d, want 4096 (from legacy flat field)", doc.ReplayConfig.PerSurfaceBytes)
	}
	if doc.EventCursor != 42 || doc.NotifyCheckpointID != "nc-1" {
		t.Errorf("cursor/notify not carried over: %+v", doc)
	}
	if len(doc.Surfaces) != 1 || doc.Surfaces[0].RestartPolicy != persist.RestartAutomatic {
		t.Errorf("surfaces not carried over: %+v", doc.Surfaces)
	}
	if doc.Graph == nil || doc.Graph.Session != "s1" || doc.Graph.Rev != 3 {
		t.Errorf("graph not carried over: %+v", doc.Graph)
	}
}

// TestGraphV0MigrationFailureIsTyped: a v0 document that fails validation
// (here: an env allowlist entry smuggling a value) reports a migration error;
// the reader test proves the previous known-good generation then loads.
func TestGraphV0MigrationFailureIsTyped(t *testing.T) {
	v0 := []byte(`{"schema_version":0,"graph":{"session":"s1"},"surfaces":[{"surface":"srf-1","env_allowlist":["SECRET=x"]}],"event_cursor":0}`)
	_, err := DecodeGraph(v0)
	if err == nil || !strings.Contains(err.Error(), "0->1") {
		t.Fatalf("v0 migration failure not reported as migration error: %v", err)
	}
}

// TestGraphEncodeRefusesNonCurrentVersion: the encoder writes only the current
// schema; it never fabricates old or future versions.
func TestGraphEncodeRefusesNonCurrentVersion(t *testing.T) {
	doc := testGraphDoc()
	doc.SchemaVersion = GraphSchemaVersion + 1
	if _, err := EncodeGraph(doc); err == nil {
		t.Fatal("EncodeGraph accepted a non-current schema version")
	}
}

// TestGraphEncodeRequiresGraph: a checkpoint without the graph DTO is
// meaningless; encoding fails closed rather than persisting an empty doc.
func TestGraphEncodeRequiresGraph(t *testing.T) {
	doc := testGraphDoc()
	doc.Graph = nil
	if _, err := EncodeGraph(doc); err == nil {
		t.Fatal("EncodeGraph accepted nil Graph")
	}
}
