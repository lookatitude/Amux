package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefault(t *testing.T) {
	d := Default()
	if d.SchemaVersion != 1 {
		t.Fatalf("SchemaVersion = %d, want 1", d.SchemaVersion)
	}
	if d.LogLevel != "info" {
		t.Fatalf("LogLevel = %q, want info", d.LogLevel)
	}
	if d.Replay.PerSurfaceBytes != 16<<20 {
		t.Fatalf("PerSurfaceBytes = %d, want %d (16 MiB replay floor)", d.Replay.PerSurfaceBytes, 16<<20)
	}
	if d.Replay.StorageBudgetBytes != 256<<20 {
		t.Fatalf("StorageBudgetBytes = %d, want %d (256 MiB)", d.Replay.StorageBudgetBytes, 256<<20)
	}
	if !d.Notifications.Desktop {
		t.Fatal("Notifications.Desktop should default to true")
	}
	if ReplayFloorBytes != 16<<20 {
		t.Fatalf("ReplayFloorBytes = %d, want %d", ReplayFloorBytes, 16<<20)
	}
}

func TestParseValid(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want Config
	}{
		{
			name: "minimal file takes documented defaults",
			src:  `{"schema_version": 1}`,
			want: Default(),
		},
		{
			name: "every field overridden",
			src: `{
  "schema_version": 1,
  "log_level": "debug",
  "replay": {
    "per_surface_bytes": 33554432,
    "storage_budget_bytes": 67108864
  },
  "notifications": { "desktop": false }
}`,
			want: Config{
				SchemaVersion: 1,
				LogLevel:      "debug",
				Replay:        Replay{PerSurfaceBytes: 33554432, StorageBudgetBytes: 67108864},
				Notifications: Notifications{Desktop: false},
			},
		},
		{
			name: "per-surface exactly at the 16 MiB floor",
			src:  `{"schema_version": 1, "replay": {"per_surface_bytes": 16777216}}`,
			want: Default(),
		},
		{
			name: "log_level warn",
			src:  `{"schema_version": 1, "log_level": "warn"}`,
			want: func() Config { c := Default(); c.LogLevel = "warn"; return c }(),
		},
		{
			name: "log_level error",
			src:  `{"schema_version": 1, "log_level": "error"}`,
			want: func() Config { c := Default(); c.LogLevel = "error"; return c }(),
		},
		{
			name: "null is treated as absent, not invalid",
			src:  `{"schema_version": 1, "log_level": null, "replay": null}`,
			want: Default(),
		},
		{
			name: "empty replay object takes both replay defaults",
			src:  `{"schema_version": 1, "replay": {}}`,
			want: Default(),
		},
		{
			name: "comments everywhere",
			src: `// amux configuration
{
  /* the schema version is required and pinned */
  "schema_version": 1, // current
  "log_level": "debug" /* inline */
}`,
			want: func() Config { c := Default(); c.LogLevel = "debug"; return c }(),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse([]byte(tc.src))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if got != tc.want {
				t.Fatalf("Parse\n got: %+v\nwant: %+v", got, tc.want)
			}
		})
	}
}

func TestParseInvalid(t *testing.T) {
	tests := []struct {
		name   string
		src    string
		substr string
	}{
		{
			name:   "schema_version absent",
			src:    `{}`,
			substr: "schema_version is required",
		},
		{
			name:   "schema_version zero",
			src:    `{"schema_version": 0}`,
			substr: "schema_version 0",
		},
		{
			name:   "schema_version negative",
			src:    `{"schema_version": -1}`,
			substr: "schema_version -1",
		},
		{
			name:   "newer schema_version refused fail-closed",
			src:    `{"schema_version": 2}`,
			substr: "supports only schema version 1",
		},
		{
			name:   "schema_version wrong type",
			src:    `{"schema_version": "1"}`,
			substr: "schema_version",
		},
		{
			name:   "invalid log_level",
			src:    `{"schema_version": 1, "log_level": "verbose"}`,
			substr: `log_level "verbose"`,
		},
		{
			name:   "explicit empty log_level is invalid, never defaulted",
			src:    `{"schema_version": 1, "log_level": ""}`,
			substr: `log_level ""`,
		},
		{
			name:   "per-surface below the frozen floor",
			src:    `{"schema_version": 1, "replay": {"per_surface_bytes": 16777215}}`,
			substr: "replay floor",
		},
		{
			name:   "per-surface zero",
			src:    `{"schema_version": 1, "replay": {"per_surface_bytes": 0}}`,
			substr: "replay floor",
		},
		{
			name:   "per-surface negative",
			src:    `{"schema_version": 1, "replay": {"per_surface_bytes": -1}}`,
			substr: "replay floor",
		},
		{
			name:   "budget below per-surface",
			src:    `{"schema_version": 1, "replay": {"per_surface_bytes": 33554432, "storage_budget_bytes": 16777216}}`,
			substr: "storage_budget_bytes",
		},
		{
			name:   "raised per-surface not covered by the default budget",
			src:    `{"schema_version": 1, "replay": {"per_surface_bytes": 536870912}}`,
			substr: "storage_budget_bytes",
		},
		{
			name:   "budget wrong type",
			src:    `{"schema_version": 1, "replay": {"storage_budget_bytes": "lots"}}`,
			substr: "storage_budget_bytes",
		},
		{
			name:   "desktop wrong type",
			src:    `{"schema_version": 1, "notifications": {"desktop": "yes"}}`,
			substr: "desktop",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse([]byte(tc.src))
			if err == nil {
				t.Fatalf("Parse(%q): want error, got %+v", tc.src, got)
			}
			if !strings.Contains(err.Error(), tc.substr) {
				t.Fatalf("error %q should contain %q", err.Error(), tc.substr)
			}
			if got != (Config{}) {
				t.Fatalf("an invalid config must never be half-applied, got %+v", got)
			}
		})
	}
}

func TestParseUnknownFieldLocated(t *testing.T) {
	src := "{\n  \"schema_version\": 1,\n  \"log_levell\": \"info\"\n}\n"
	_, err := Parse([]byte(src))
	var ce *Error
	if !errors.As(err, &ce) {
		t.Fatalf("want *Error, got %T: %v", err, err)
	}
	if ce.Line != 3 || ce.Col != 3 {
		t.Fatalf("unknown field located at %d:%d, want 3:3 (%v)", ce.Line, ce.Col, err)
	}
	if !strings.Contains(err.Error(), `unknown field "log_levell"`) {
		t.Fatalf("error should name the unknown field: %v", err)
	}
}

func TestLoad(t *testing.T) {
	t.Run("missing file yields documented defaults", func(t *testing.T) {
		got, err := Load(filepath.Join(t.TempDir(), "config.jsonc"))
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if got != Default() {
			t.Fatalf("Load = %+v, want %+v", got, Default())
		}
	})

	t.Run("valid file with comments is applied", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.jsonc")
		src := "// amux\n{\n  \"schema_version\": 1, // pinned\n  \"log_level\": \"debug\"\n}\n"
		if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := Load(path)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		want := Default()
		want.LogLevel = "debug"
		if got != want {
			t.Fatalf("Load = %+v, want %+v", got, want)
		}
	})

	t.Run("present-but-invalid file is an error carrying the path", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.jsonc")
		if err := os.WriteFile(path, []byte("{\n  \"schema_version\": 1,\n}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := Load(path)
		if err == nil {
			t.Fatalf("Load: want error, got %+v", got)
		}
		if !strings.Contains(err.Error(), path) {
			t.Fatalf("error should carry the file path %q: %v", path, err)
		}
		var ce *Error
		if !errors.As(err, &ce) || ce.Line != 3 || ce.Col != 1 {
			t.Fatalf("want located *Error at 3:1, got %v", err)
		}
	})

	t.Run("unreadable path is an error, not defaults", func(t *testing.T) {
		if _, err := Load(t.TempDir()); err == nil {
			t.Fatal("Load(directory): want error")
		}
	})
}
