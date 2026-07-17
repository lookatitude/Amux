// Package config owns Amux's durable on-disk configuration and XDG base
// directory resolution (ADR-0001 package map: "internal/config — JSONC,
// schema, XDG resolution").
//
// The configuration file is JSONC: strict JSON plus // line and /* block */
// comments (never inside strings). Everything else keeps strict JSON
// semantics — trailing commas, unknown fields, and trailing data are all
// rejected — because a config file is a durable boundary, and durable
// boundaries decode strictly (ADR-0003 unknown-field policy; api/v1
// DecodeStrict is the wire-side twin of this posture). Every decode error
// carries a 1-based line:column location in the original text.
//
// Validation is fail-closed: only an ABSENT field takes its documented
// default; a present-but-invalid value is always an error and is never
// silently replaced or clamped (spec "JSONC configuration with explicit
// schema version"). A missing config file is the one benign absence: Load
// returns the documented defaults for it.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

const (
	// CurrentSchemaVersion is the only config schema this build reads. The
	// version is explicit and required in every file (spec "JSONC configuration
	// with explicit schema version"). A file declaring a NEWER version is
	// refused fail-closed — a newer schema may carry semantics this build
	// cannot honor, and half-applying it would be a silent misconfiguration.
	CurrentSchemaVersion = 1

	// DefaultLogLevel is the log level applied when log_level is absent.
	DefaultLogLevel = "info"

	// ReplayFloorBytes is the frozen per-surface replay minimum. The spec
	// requires each surface to retain "at least the most recent 16 MiB of raw
	// PTY output, configurable upward under a documented global storage
	// budget", so the floor is a minimum that values may only exceed: a
	// configured value below it is rejected outright, never silently clamped
	// up (fail-closed validation, no default substitution for present values).
	ReplayFloorBytes int64 = 16 << 20

	// DefaultPerSurfaceBytes is the per-surface replay retention applied when
	// replay.per_surface_bytes is absent: exactly the spec floor.
	DefaultPerSurfaceBytes int64 = ReplayFloorBytes

	// DefaultStorageBudgetBytes is the documented global storage budget (256
	// MiB) bounding total replay retention when replay.storage_budget_bytes is
	// absent. It must always cover at least one surface's retention.
	DefaultStorageBudgetBytes int64 = 256 << 20
)

// Config is the validated runtime configuration. A Config only ever exists in
// a valid state: Parse and Load return either a fully validated value or an
// error, never a half-applied mixture.
type Config struct {
	// SchemaVersion is the explicit schema version the file declared (or
	// CurrentSchemaVersion for defaults). Always CurrentSchemaVersion after
	// validation.
	SchemaVersion int
	// LogLevel is one of "debug", "info", "warn", "error".
	LogLevel string
	// Replay is the raw-output retention configuration.
	Replay Replay
	// Notifications is the notification delivery configuration.
	Notifications Notifications
}

// Replay bounds raw PTY output retention (spec replay guarantees).
type Replay struct {
	// PerSurfaceBytes is the per-surface replay retention. Always >=
	// ReplayFloorBytes after validation.
	PerSurfaceBytes int64
	// StorageBudgetBytes is the documented global storage budget across
	// surfaces. Always >= PerSurfaceBytes after validation.
	StorageBudgetBytes int64
}

// Notifications configures delivery adapters (PRD F8; the daemon-owned store
// remains the state authority regardless of delivery settings).
type Notifications struct {
	// Desktop enables best-effort desktop notification delivery.
	Desktop bool
}

// Default returns the documented defaults — the exact configuration a missing
// config file yields.
func Default() Config {
	return Config{
		SchemaVersion: CurrentSchemaVersion,
		LogLevel:      DefaultLogLevel,
		Replay: Replay{
			PerSurfaceBytes:    DefaultPerSurfaceBytes,
			StorageBudgetBytes: DefaultStorageBudgetBytes,
		},
		Notifications: Notifications{Desktop: true},
	}
}

// fileConfig is the on-disk shape. Every field is a pointer so an ABSENT
// field (nil, defaulted) is distinguishable from a PRESENT-but-invalid one
// (error): defaults never mask an explicit bad value. JSON null decodes to a
// nil pointer and is therefore treated as absent, not invalid.
type fileConfig struct {
	SchemaVersion *int               `json:"schema_version"`
	LogLevel      *string            `json:"log_level"`
	Replay        *fileReplay        `json:"replay"`
	Notifications *fileNotifications `json:"notifications"`
}

type fileReplay struct {
	PerSurfaceBytes    *int64 `json:"per_surface_bytes"`
	StorageBudgetBytes *int64 `json:"storage_budget_bytes"`
}

type fileNotifications struct {
	Desktop *bool `json:"desktop"`
}

// Parse decodes and validates one JSONC configuration document. Decode errors
// (syntax, wrong type, unknown field, trailing data) are located *Error
// values; validation errors are descriptive but unlocated (the value is
// well-formed JSON, just semantically invalid). On any error the returned
// Config is the zero value — never a partial application.
func Parse(src []byte) (Config, error) {
	var fc fileConfig
	if err := decodeJSONC(src, &fc); err != nil {
		return Config{}, err
	}
	return fc.resolve()
}

// resolve validates fc fail-closed and fills documented defaults for absent
// fields only.
func (fc *fileConfig) resolve() (Config, error) {
	cfg := Default()

	// Schema gate first: nothing else in the file is interpretable until the
	// declared schema is the one this build understands.
	switch {
	case fc.SchemaVersion == nil:
		return Config{}, fmt.Errorf("config: schema_version is required (this build supports schema version %d)", CurrentSchemaVersion)
	case *fc.SchemaVersion > CurrentSchemaVersion:
		return Config{}, fmt.Errorf(
			"config: file declares schema_version %d, but this build supports only schema version %d; refusing to load a newer schema (upgrade amux or rewrite the config for version %d)",
			*fc.SchemaVersion, CurrentSchemaVersion, CurrentSchemaVersion)
	case *fc.SchemaVersion < CurrentSchemaVersion:
		return Config{}, fmt.Errorf("config: schema_version %d is invalid; the minimum (and current) schema version is %d", *fc.SchemaVersion, CurrentSchemaVersion)
	}

	if fc.LogLevel != nil {
		switch *fc.LogLevel {
		case "debug", "info", "warn", "error":
			cfg.LogLevel = *fc.LogLevel
		default:
			return Config{}, fmt.Errorf("config: log_level %q is invalid; must be one of debug, info, warn, error", *fc.LogLevel)
		}
	}

	if fc.Replay != nil {
		if v := fc.Replay.PerSurfaceBytes; v != nil {
			if *v < ReplayFloorBytes {
				return Config{}, fmt.Errorf(
					"config: replay.per_surface_bytes %d is below the frozen 16 MiB replay floor (%d bytes); the floor is a spec minimum and is never silently clamped",
					*v, ReplayFloorBytes)
			}
			cfg.Replay.PerSurfaceBytes = *v
		}
		if v := fc.Replay.StorageBudgetBytes; v != nil {
			cfg.Replay.StorageBudgetBytes = *v
		}
	}
	// The budget must cover at least one surface's retention. This holds for
	// the RESOLVED pair: raising per_surface_bytes past the default budget
	// requires raising storage_budget_bytes explicitly too.
	if cfg.Replay.StorageBudgetBytes < cfg.Replay.PerSurfaceBytes {
		return Config{}, fmt.Errorf(
			"config: replay.storage_budget_bytes %d is smaller than replay.per_surface_bytes %d; the global budget must cover at least one surface",
			cfg.Replay.StorageBudgetBytes, cfg.Replay.PerSurfaceBytes)
	}

	if fc.Notifications != nil && fc.Notifications.Desktop != nil {
		cfg.Notifications.Desktop = *fc.Notifications.Desktop
	}

	return cfg, nil
}

// Load reads the optional configuration file at path. A MISSING file yields
// the documented defaults (the config file is optional); any other read
// failure and any PRESENT-but-invalid content is an error — a broken file
// must surface, never half-apply (fail-closed, spec "JSONC configuration
// with explicit schema version").
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("config: read %s: %w", path, err)
	}
	cfg, err := Parse(data)
	if err != nil {
		var ce *Error
		if errors.As(err, &ce) {
			ce.Path = path // freshly allocated by this Parse call; safe to fill
			return Config{}, err
		}
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}
