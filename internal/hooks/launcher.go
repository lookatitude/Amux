package hooks

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
)

// OpenedObject is the descriptor-bound capture of a hook's executable and
// config at open time (HA-10/HA-11, ADR-0006 descriptor-bound launch). The
// digests describe the bytes read through the bound descriptors; a symlink
// swap, rename, or in-place byte replacement AFTER Open cannot change what
// these describe, because the launcher executes exactly the captured object.
// Path-only revalidation is explicitly insufficient and unsupported here.
type OpenedObject struct {
	ExecPath     string
	ConfigPath   string
	ExecSHA256   string
	ConfigSHA256 string
	// execBytes is the captured executable content the launcher "runs"; in the
	// portable spy path it is never actually executed (fixtures assert on the
	// digest, never on side effects). The real Linux launcher execs the bound
	// descriptor via fexecve/execveat instead (deferred runtime evidence).
	execBytes []byte
	regular   bool
}

// ExecIsRegularFile reports whether the opened executable was a regular file
// (HA-12: a FIFO/device/directory fails closed at open).
func (o OpenedObject) ExecIsRegularFile() bool { return o.regular }

// Launcher opens and launches hook objects. The portable implementation
// (SpyLauncher) models descriptor binding by capturing content at Open and
// never executing a real process — every securitytest fixture asserts on the
// recorded digest, so a spy is sufficient and safe. The real Linux launcher
// (deferred, ADR-0006) wires platform.DescriptorLaunch: openat2 with
// RESOLVE_NO_SYMLINKS|RESOLVE_NO_MAGICLINKS|RESOLVE_BENEATH then execveat of
// the already-open descriptor.
type Launcher interface {
	// Open reads and digests the executable and config as bound objects.
	Open(execPath, configPath string) (OpenedObject, error)
	// Launch "executes" the already-opened object and returns a controllable
	// process. The returned process's ExecSHA256 equals obj.ExecSHA256 — the
	// digest captured at Open, never a post-open substitution.
	Launch(obj OpenedObject, argv, env []string, dir string) (*Process, error)
}

// Process is a launched hook the runtime signals and reaps. In the spy path it
// carries no OS resource; its lifecycle timestamps are driven by the runtime's
// deterministic scheduler.
type Process struct {
	pid  int
	obj  OpenedObject
	argv []string
	env  []string
	dir  string
}

// PID returns the (spy) process id.
func (p *Process) PID() int { return p.pid }

func sha256hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// SpyLauncher is the portable launcher: it reads+digests real files (so the
// races.* fixtures have real objects to open and attack) but never execs a
// real process. It records a monotonic PID per launch.
type SpyLauncher struct {
	nextPID int
}

// NewSpyLauncher returns a spy launcher.
func NewSpyLauncher() *SpyLauncher { return &SpyLauncher{nextPID: 1000} }

// ErrNotRegularFile is returned when the executable is not a regular file.
var ErrNotRegularFile = errors.New("hooks: executable is not a regular file")

// Open reads and digests both objects. The read captures the exact bytes
// behind the descriptor at this instant; the runtime parks at
// SyncAfterObjectOpen immediately after, so any fixture mutation happens
// strictly after capture.
func (s *SpyLauncher) Open(execPath, configPath string) (OpenedObject, error) {
	info, err := os.Lstat(execPath)
	if err != nil {
		return OpenedObject{}, err
	}
	regular := info.Mode().IsRegular()
	execBytes, err := os.ReadFile(execPath)
	if err != nil {
		return OpenedObject{}, err
	}
	var configSHA string
	if configPath != "" {
		cfg, err := os.ReadFile(configPath)
		if err != nil {
			return OpenedObject{}, err
		}
		configSHA = sha256hex(cfg)
	}
	return OpenedObject{
		ExecPath:     execPath,
		ConfigPath:   configPath,
		ExecSHA256:   sha256hex(execBytes),
		ConfigSHA256: configSHA,
		execBytes:    execBytes,
		regular:      regular,
	}, nil
}

// Launch records a spy process bound to the captured object.
func (s *SpyLauncher) Launch(obj OpenedObject, argv, env []string, dir string) (*Process, error) {
	s.nextPID++
	return &Process{pid: s.nextPID, obj: obj, argv: argv, env: env, dir: dir}, nil
}
