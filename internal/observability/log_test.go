package observability

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
)

// TestCorrelationAttrKeysFrozen pins the attribute-key constants. These keys
// are a diagnostic contract: log consumers (grep, jq, support tooling) key on
// them, so changing a value is a breaking change and must fail this test.
func TestCorrelationAttrKeysFrozen(t *testing.T) {
	frozen := map[string]string{
		"AttrBootID":     AttrBootID,
		"AttrSession":    AttrSession,
		"AttrConn":       AttrConn,
		"AttrSurface":    AttrSurface,
		"AttrActivation": AttrActivation,
	}
	want := map[string]string{
		"AttrBootID":     "boot_id",
		"AttrSession":    "session",
		"AttrConn":       "conn",
		"AttrSurface":    "surface",
		"AttrActivation": "activation",
	}
	for name, got := range frozen {
		if got != want[name] {
			t.Errorf("%s = %q, want %q (frozen key)", name, got, want[name])
		}
	}
}

// captureRecord logs one message through the given child logger and decodes the
// emitted JSON record into a map.
func captureRecord(t *testing.T, build func(root *slog.Logger) *slog.Logger) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	root := slog.New(slog.NewJSONHandler(&buf, nil))
	build(root).Info("probe")
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("emitted record is not one JSON object: %v (raw: %q)", err, buf.String())
	}
	return rec
}

// TestCorrelationLoggersEmitFrozenKeys asserts that every helper produces a
// child logger whose emitted JSON records carry the stable correlation keys.
func TestCorrelationLoggersEmitFrozenKeys(t *testing.T) {
	cases := []struct {
		name  string
		build func(root *slog.Logger) *slog.Logger
		want  map[string]string
	}{
		{
			name:  "WithBoot",
			build: func(l *slog.Logger) *slog.Logger { return WithBoot(l, "boot-1") },
			want:  map[string]string{AttrBootID: "boot-1"},
		},
		{
			name:  "WithSession",
			build: func(l *slog.Logger) *slog.Logger { return WithSession(l, "sess-1") },
			want:  map[string]string{AttrSession: "sess-1"},
		},
		{
			name:  "WithConn",
			build: func(l *slog.Logger) *slog.Logger { return WithConn(l, "conn-1") },
			want:  map[string]string{AttrConn: "conn-1"},
		},
		{
			name:  "WithSurface",
			build: func(l *slog.Logger) *slog.Logger { return WithSurface(l, "sess-1", "surf-1") },
			want:  map[string]string{AttrSession: "sess-1", AttrSurface: "surf-1"},
		},
		{
			name:  "WithActivation",
			build: func(l *slog.Logger) *slog.Logger { return WithActivation(l, "act-1") },
			want:  map[string]string{AttrActivation: "act-1"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := captureRecord(t, tc.build)
			for key, want := range tc.want {
				got, ok := rec[key]
				if !ok {
					t.Fatalf("record missing key %q: %v", key, rec)
				}
				if got != want {
					t.Errorf("record[%q] = %v, want %q", key, got, want)
				}
			}
		})
	}
}

// TestCorrelationLoggersCompose asserts the helpers stack: a boot-scoped logger
// narrowed to a session and connection carries all three keys.
func TestCorrelationLoggersCompose(t *testing.T) {
	rec := captureRecord(t, func(l *slog.Logger) *slog.Logger {
		return WithConn(WithSession(WithBoot(l, "boot-1"), "sess-1"), "conn-1")
	})
	for key, want := range map[string]string{
		AttrBootID:  "boot-1",
		AttrSession: "sess-1",
		AttrConn:    "conn-1",
	} {
		if rec[key] != want {
			t.Errorf("record[%q] = %v, want %q", key, rec[key], want)
		}
	}
}
