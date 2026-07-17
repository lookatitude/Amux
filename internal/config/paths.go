package config

import (
	"errors"
	"fmt"
	"path/filepath"
)

// ErrRuntimeDirUnset is returned by Resolve when XDG_RUNTIME_DIR is not set
// to an absolute path. The daemon's owner-only control socket lives beneath
// the private runtime directory (ADR-0003: "a Unix socket beneath
// $XDG_RUNTIME_DIR"), whose 0700 owner-only guarantee comes from the login
// manager. Inventing a fallback location would silently weaken that boundary,
// so resolution fails closed and the daemon surfaces this actionable message
// instead (spec "XDG config, data, state, cache, and runtime paths").
var ErrRuntimeDirUnset = errors.New(
	"XDG_RUNTIME_DIR is not set to an absolute path; amux requires the private per-user runtime directory for its owner-only control socket (log in through a session manager that provides one, e.g. systemd-logind, or export XDG_RUNTIME_DIR to a private 0700 directory you own)")

// Paths is the resolved set of Amux base directories, one per XDG base
// directory role (spec "XDG config, data, state, cache, and runtime paths").
// Resolution is pure: no directory is created or inspected here.
type Paths struct {
	// ConfigDir holds user configuration: $XDG_CONFIG_HOME/amux, else
	// $HOME/.config/amux.
	ConfigDir string
	// DataDir holds durable user data (snapshots, SQLite; ADR-0005):
	// $XDG_DATA_HOME/amux, else $HOME/.local/share/amux.
	DataDir string
	// StateDir holds re-creatable state (logs, replay spill):
	// $XDG_STATE_HOME/amux, else $HOME/.local/state/amux.
	StateDir string
	// CacheDir holds discardable caches: $XDG_CACHE_HOME/amux, else
	// $HOME/.cache/amux.
	CacheDir string
	// RuntimeDir holds the sockets for this login session:
	// $XDG_RUNTIME_DIR/amux. There is no fallback (see ErrRuntimeDirUnset).
	RuntimeDir string
}

// SocketPath is the daemon control socket beneath the private runtime
// directory (ADR-0003 transport binding).
func (p Paths) SocketPath() string { return filepath.Join(p.RuntimeDir, "amuxd.sock") }

// ConfigFile is the optional JSONC configuration file Load reads.
func (p Paths) ConfigFile() string { return filepath.Join(p.ConfigDir, "config.jsonc") }

// Resolve computes the Amux base directories from the environment. The
// environment is injected via getenv (never read from os.Getenv here) so
// resolution is deterministic and tests are hermetic.
//
// Per the XDG Base Directory Specification, a relative (non-absolute) value —
// including the empty string — is ignored and treated as unset: for the four
// home-relative roles that means the $HOME fallback, and for XDG_RUNTIME_DIR
// it means ErrRuntimeDirUnset (fail closed; the socket needs a genuinely
// private directory).
func Resolve(getenv func(string) string) (Paths, error) {
	home := getenv("HOME")
	dir := func(envVar, homeRel string) (string, error) {
		if v := getenv(envVar); filepath.IsAbs(v) {
			return filepath.Join(v, "amux"), nil
		}
		if !filepath.IsAbs(home) {
			return "", fmt.Errorf("config: %s is unset (or relative, which the XDG basedir spec ignores) and HOME is not an absolute path; cannot resolve a base directory", envVar)
		}
		return filepath.Join(home, homeRel, "amux"), nil
	}

	var p Paths
	var err error
	if p.ConfigDir, err = dir("XDG_CONFIG_HOME", ".config"); err != nil {
		return Paths{}, err
	}
	if p.DataDir, err = dir("XDG_DATA_HOME", filepath.Join(".local", "share")); err != nil {
		return Paths{}, err
	}
	if p.StateDir, err = dir("XDG_STATE_HOME", filepath.Join(".local", "state")); err != nil {
		return Paths{}, err
	}
	if p.CacheDir, err = dir("XDG_CACHE_HOME", ".cache"); err != nil {
		return Paths{}, err
	}

	runtime := getenv("XDG_RUNTIME_DIR")
	if !filepath.IsAbs(runtime) {
		return Paths{}, fmt.Errorf("config: %w", ErrRuntimeDirUnset)
	}
	p.RuntimeDir = filepath.Join(runtime, "amux")
	return p, nil
}
