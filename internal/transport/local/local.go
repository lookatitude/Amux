// Package local implements platform.LocalTransport over real Unix domain
// sockets (ADR-0006 §LocalTransport; ADR-0003 transport binding). It is the T4
// mechanism behind the frozen seam: no package above platform may touch socket
// APIs directly, and everything security-relevant here maps to a numbered
// requirement in docs/security/local-transport-hardening.md:
//
//   - STR-1: runtime dir created 0700, socket 0600, both owned by the daemon UID.
//   - STR-3: every runtime-path component is validated from the filesystem root
//     without following symlinks (os.Lstat per component); a symlink component,
//     an unexpected owner, or a group/other-writable component refuses the bind.
//   - STR-4: a pre-existing object at the socket path is removed only after
//     proving it is a socket, owned by the expected UID, and dead (a probe dial
//     fails with connection-refused). Anything else is a typed refusal.
//
// Peer identity (STR-2) is deliberately NOT enforced here: Accept is structural
// only, and the daemon runs the mandatory PeerCredentials.PeerUID check via
// LocalConn.Control before the first protocol byte (internal/protocol).
//
// The lstat-then-connect checks in Dial are advisory hardening: a local attacker
// can always race path metadata (TOCTOU). The authoritative gate is the
// server-side SO_PEERCRED check plus the 0700 runtime directory, which makes the
// socket unreachable by other UIDs regardless of the socket's own mode bits.
package local

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/amux-run/amux/internal/platform"
)

// Typed refusals. Every fail-closed branch wraps exactly one of these so
// callers (and tests) can classify the refusal with errors.Is; the message
// carries the offending path as an actionable diagnostic (STR-3).
var (
	// ErrSymlinkComponent rejects a symlink anywhere in the runtime path chain
	// or at the socket location itself (STR-3/STR-4: no symlink traversal).
	ErrSymlinkComponent = errors.New("amux transport: symlink in runtime path")
	// ErrNotDirectory rejects a runtime path component of the wrong type (STR-3).
	ErrNotDirectory = errors.New("amux transport: runtime path component is not a directory")
	// ErrUntrustedOwner rejects a component, socket, or caller whose UID is not
	// the expected owner (STR-3/STR-4: never trust or unlink another UID's file).
	ErrUntrustedOwner = errors.New("amux transport: unexpected owner")
	// ErrUnsafeMode rejects group/other-writable components and sockets whose
	// permissions exceed the owner-only discipline (STR-1/STR-3).
	ErrUnsafeMode = errors.New("amux transport: unsafe permissions")
	// ErrNotSocket rejects a non-socket object at the socket path (STR-4: a
	// hostile regular file/symlink/directory is never unlinked).
	ErrNotSocket = errors.New("amux transport: path is not a unix socket")
	// ErrSocketBusy refuses to steal a socket with a live (or unprovably dead)
	// owner: the liveness probe connected, timed out, or failed ambiguously
	// (STR-4: removal requires PROOF of death, so ambiguity fails closed).
	ErrSocketBusy = errors.New("amux transport: socket has a live owner")
)

const (
	// runtimeDirMode is the exact mode of the private runtime directory (STR-1).
	runtimeDirMode = fs.FileMode(0o700)
	// socketMode is the exact mode of the bound control socket (STR-1).
	socketMode = fs.FileMode(0o600)
	// probeTimeout bounds the STR-4 liveness probe so a wedged listener cannot
	// stall daemon startup; an unanswered probe is treated as live (fail closed).
	probeTimeout = 250 * time.Millisecond
	// unsafeWriteBits are the group/other write bits that make a runtime path
	// component rewritable by another principal (STR-3).
	unsafeWriteBits = fs.FileMode(0o022)
)

// Transport is the Unix-domain-socket implementation of platform.LocalTransport.
// The unexported function fields are test seams: tests substitute lstat/owner
// to simulate foreign-owner objects that cannot be created without a second UID
// on the authoring host (the real second-UID variants are Linux-CI checks,
// STR "integration-second-uid").
type Transport struct {
	lstat func(path string) (os.FileInfo, error)
	owner func(path string, fi os.FileInfo) (uint32, error)
}

var _ platform.LocalTransport = (*Transport)(nil)

// New returns the production transport backed by os.Lstat and the platform's
// stat-owner extraction.
func New() *Transport {
	return &Transport{lstat: os.Lstat, owner: fileOwner}
}

// Listen binds the daemon control endpoint described by spec, enforcing the
// full STR-1/STR-3/STR-4 discipline before the socket exists:
//
//  1. The calling process UID must equal spec.OwnerUID (a process cannot create
//     a runtime dir owned by someone else, so a mismatch is a misconfiguration
//     and fails closed).
//  2. Every component of the socket directory's parent chain, from "/" down,
//     is lstat'ed without following symlinks and must be a non-symlink
//     directory owned by spec.OwnerUID or root with no group/other write bits.
//  3. The final runtime directory is created 0700 (or, when pre-existing,
//     must be a non-symlink directory owned exactly by spec.OwnerUID with no
//     group/other bits at all).
//  4. A pre-existing socket-path object is reclaimed only after the STR-4
//     proof: lstat says socket, owner is spec.OwnerUID, and a probe dial fails
//     with connection-refused. Every other condition is a typed refusal.
//  5. The socket is bound and chmod'ed to 0600. The chmod-after-bind window is
//     harmless because the 0700 parent directory already blocks other UIDs.
func (t *Transport) Listen(spec platform.TransportSpec) (platform.LocalListener, error) {
	if err := validateSpec(spec); err != nil {
		return nil, err
	}
	if uid := uint32(os.Getuid()); uid != spec.OwnerUID {
		return nil, fmt.Errorf("%w: process uid %d cannot own endpoint for uid %d", ErrUntrustedOwner, uid, spec.OwnerUID)
	}
	dir := filepath.Dir(spec.SocketPath)
	if err := t.validateChain(filepath.Dir(dir), spec.OwnerUID); err != nil {
		return nil, err
	}
	if err := t.ensureRuntimeDir(dir, spec.OwnerUID); err != nil {
		return nil, err
	}
	if err := t.reclaimStale(spec); err != nil {
		return nil, err
	}
	ul, err := net.ListenUnix("unix", &net.UnixAddr{Name: spec.SocketPath, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("amux transport: bind %s: %w", spec.SocketPath, err)
	}
	if err := os.Chmod(spec.SocketPath, socketMode); err != nil {
		ul.Close()
		return nil, fmt.Errorf("amux transport: chmod socket %s: %w", spec.SocketPath, err)
	}
	// Defense in depth (STR-1): verify the object we just bound is a socket we
	// own with the exact owner-only mode before handing it to the daemon.
	if err := t.checkSocket(spec, socketMode.Perm()); err != nil {
		ul.Close()
		return nil, err
	}
	return &Listener{ul: ul, path: spec.SocketPath}, nil
}

// Dial connects a client to the endpoint, failing closed when the socket's
// type, ownership, or mode does not match spec (ADR-0006: Dial fails closed on
// an owner mismatch). See the package comment for why these lstat checks are
// advisory: the authoritative identity gate is server-side SO_PEERCRED.
func (t *Transport) Dial(spec platform.TransportSpec) (platform.LocalConn, error) {
	if err := validateSpec(spec); err != nil {
		return nil, err
	}
	// Unsafe mode on Dial means group/other-writable: a socket another UID can
	// rebind/replace is not trustworthy even when the owner matches.
	if err := t.checkSocket(spec, 0); err != nil {
		return nil, err
	}
	uc, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: spec.SocketPath, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("amux transport: dial %s: %w", spec.SocketPath, err)
	}
	return &Conn{uc: uc}, nil
}

// validateSpec rejects a relative or empty socket path before any filesystem
// access; component validation is only meaningful on an absolute chain (STR-3).
func validateSpec(spec platform.TransportSpec) error {
	if spec.SocketPath == "" || !filepath.IsAbs(spec.SocketPath) {
		return fmt.Errorf("amux transport: socket path must be absolute, got %q", spec.SocketPath)
	}
	return nil
}

// validateChain lstat-walks every component of dir from the filesystem root
// down (STR-3). No component may be a symlink (no symlink traversal), every
// component must be a directory owned by ownerUID or root (root owns "/",
// "/run", and the $XDG_RUNTIME_DIR parents), and none may be group/other-
// writable. The walk never follows a link, so a hostile symlink swap of any
// prefix is detected at that component.
func (t *Transport) validateChain(dir string, ownerUID uint32) error {
	dir = filepath.Clean(dir)
	for _, component := range chain(dir) {
		fi, err := t.lstat(component)
		if err != nil {
			return fmt.Errorf("amux transport: lstat runtime path component %s: %w", component, err)
		}
		if fi.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%w: component %s", ErrSymlinkComponent, component)
		}
		if !fi.IsDir() {
			return fmt.Errorf("%w: component %s", ErrNotDirectory, component)
		}
		uid, err := t.owner(component, fi)
		if err != nil {
			return fmt.Errorf("amux transport: owner of %s: %w", component, err)
		}
		if uid != ownerUID && uid != 0 {
			return fmt.Errorf("%w: component %s owned by uid %d, want %d or root", ErrUntrustedOwner, component, uid, ownerUID)
		}
		if fi.Mode().Perm()&unsafeWriteBits != 0 {
			return fmt.Errorf("%w: component %s mode %#o is group/other-writable", ErrUnsafeMode, component, fi.Mode().Perm())
		}
	}
	return nil
}

// ensureRuntimeDir creates the final runtime directory 0700 owned by the
// caller, or validates a pre-existing one (STR-1). The pre-existing case is
// stricter than the chain rule: the final directory must be owned exactly by
// ownerUID (not root) and must expose no group/other bits at all, because it
// is the directory whose 0700 mode is the real reachability barrier.
func (t *Transport) ensureRuntimeDir(dir string, ownerUID uint32) error {
	err := os.Mkdir(dir, runtimeDirMode)
	if err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("amux transport: create runtime dir %s: %w", dir, err)
	}
	// Mkdir does not follow a symlink at the final component (it fails EEXIST),
	// so lstat-verifying after the call closes the create/validate race.
	fi, err := t.lstat(dir)
	if err != nil {
		return fmt.Errorf("amux transport: lstat runtime dir %s: %w", dir, err)
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("%w: runtime dir %s", ErrSymlinkComponent, dir)
	}
	if !fi.IsDir() {
		return fmt.Errorf("%w: runtime dir %s", ErrNotDirectory, dir)
	}
	uid, err := t.owner(dir, fi)
	if err != nil {
		return fmt.Errorf("amux transport: owner of runtime dir %s: %w", dir, err)
	}
	if uid != ownerUID {
		return fmt.Errorf("%w: runtime dir %s owned by uid %d, want %d", ErrUntrustedOwner, dir, uid, ownerUID)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%w: runtime dir %s mode %#o, want %#o", ErrUnsafeMode, dir, fi.Mode().Perm(), runtimeDirMode)
	}
	return nil
}

// reclaimStale implements the STR-4 stale-socket proof. A pre-existing object
// at the socket path is unlinked ONLY when all three hold:
//
//	(a) lstat reports a socket (a symlink, file, or directory is hostile),
//	(b) it is owned by spec.OwnerUID (never unlink another UID's object), and
//	(c) a probe dial fails with connection-refused (no live server holds it).
//
// A successful probe, a probe timeout, or any ambiguous probe error refuses the
// bind with ErrSocketBusy: removal requires proof of death, not absence of
// proof of life.
func (t *Transport) reclaimStale(spec platform.TransportSpec) error {
	fi, err := t.lstat(spec.SocketPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("amux transport: lstat socket path %s: %w", spec.SocketPath, err)
	}
	if fi.Mode()&fs.ModeSocket == 0 {
		return fmt.Errorf("%w: refusing to remove %s (mode %s)", ErrNotSocket, spec.SocketPath, fi.Mode())
	}
	uid, err := t.owner(spec.SocketPath, fi)
	if err != nil {
		return fmt.Errorf("amux transport: owner of %s: %w", spec.SocketPath, err)
	}
	if uid != spec.OwnerUID {
		return fmt.Errorf("%w: socket %s owned by uid %d, want %d; refusing to remove", ErrUntrustedOwner, spec.SocketPath, uid, spec.OwnerUID)
	}
	conn, err := net.DialTimeout("unix", spec.SocketPath, probeTimeout)
	if err == nil {
		conn.Close()
		return fmt.Errorf("%w: %s answered the liveness probe", ErrSocketBusy, spec.SocketPath)
	}
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		// Proven dead: socket type, our owner, no listener behind it.
	case errors.Is(err, fs.ErrNotExist):
		return nil // vanished between lstat and probe: nothing to reclaim
	default:
		return fmt.Errorf("%w: probe of %s failed ambiguously: %v", ErrSocketBusy, spec.SocketPath, err)
	}
	if err := os.Remove(spec.SocketPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("amux transport: remove stale socket %s: %w", spec.SocketPath, err)
	}
	return nil
}

// checkSocket lstat-validates the object at spec.SocketPath: it must be a
// socket (not a symlink or any other type), owned by spec.OwnerUID, and free of
// group/other write bits. When exactPerm is non-zero the mode must match it
// exactly (the Listen-side STR-1 assertion).
func (t *Transport) checkSocket(spec platform.TransportSpec, exactPerm fs.FileMode) error {
	fi, err := t.lstat(spec.SocketPath)
	if err != nil {
		return fmt.Errorf("amux transport: lstat socket %s: %w", spec.SocketPath, err)
	}
	if fi.Mode()&fs.ModeSocket == 0 {
		return fmt.Errorf("%w: %s (mode %s)", ErrNotSocket, spec.SocketPath, fi.Mode())
	}
	uid, err := t.owner(spec.SocketPath, fi)
	if err != nil {
		return fmt.Errorf("amux transport: owner of socket %s: %w", spec.SocketPath, err)
	}
	if uid != spec.OwnerUID {
		return fmt.Errorf("%w: socket %s owned by uid %d, want %d", ErrUntrustedOwner, spec.SocketPath, uid, spec.OwnerUID)
	}
	perm := fi.Mode().Perm()
	if exactPerm != 0 && perm != exactPerm {
		return fmt.Errorf("%w: socket %s mode %#o, want %#o", ErrUnsafeMode, spec.SocketPath, perm, exactPerm)
	}
	if perm&unsafeWriteBits != 0 {
		return fmt.Errorf("%w: socket %s mode %#o is group/other-writable", ErrUnsafeMode, spec.SocketPath, perm)
	}
	return nil
}

// chain expands an absolute clean path into every prefix from "/" down,
// e.g. /run/user/1000 -> ["/", "/run", "/run/user", "/run/user/1000"].
func chain(dir string) []string {
	out := []string{"/"}
	rest := strings.TrimPrefix(dir, "/")
	if rest == "" {
		return out
	}
	acc := ""
	for _, part := range strings.Split(rest, "/") {
		acc += "/" + part
		out = append(out, acc)
	}
	return out
}

// Listener implements platform.LocalListener over a *net.UnixListener.
type Listener struct {
	ul   *net.UnixListener
	path string

	mu     sync.Mutex
	closed bool
}

var _ platform.LocalListener = (*Listener)(nil)

// Accept returns the next connection. Accept is structural only: peer identity
// is the daemon's job via PeerCredentials before the first protocol byte
// (STR-2 lives in internal/protocol, not here).
func (l *Listener) Accept() (platform.LocalConn, error) {
	uc, err := l.ul.AcceptUnix()
	if err != nil {
		return nil, err
	}
	return &Conn{uc: uc}, nil
}

// Path returns the bound socket path for health and cleanup reporting.
func (l *Listener) Path() string { return l.path }

// Close closes the listener and unlinks the socket. Idempotent: the second and
// later calls return nil. The unlink is performed by net's unlink-on-close for
// the path this listener itself bound — Close never removes a path bound by a
// successor daemon (that would violate the STR-4 never-unlink-others rule).
func (l *Listener) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	return l.ul.Close()
}

// Conn implements platform.LocalConn over a *net.UnixConn. Framing,
// negotiation, and envelopes live in internal/protocol (ADR-0003), never here.
type Conn struct {
	uc *net.UnixConn

	mu     sync.Mutex
	closed bool
}

var _ platform.LocalConn = (*Conn)(nil)

func (c *Conn) Read(p []byte) (int, error)  { return c.uc.Read(p) }
func (c *Conn) Write(p []byte) (int, error) { return c.uc.Write(p) }

// Control runs f with the connection's raw file descriptor via
// syscall.RawConn, mirroring platform.LocalConn semantics so the daemon can run
// the mandatory PeerCredentials.PeerUID check (STR-2). The descriptor is valid
// only for the duration of f.
func (c *Conn) Control(f func(fd uintptr) error) error {
	raw, err := c.uc.SyscallConn()
	if err != nil {
		return err
	}
	var inner error
	if err := raw.Control(func(fd uintptr) { inner = f(fd) }); err != nil {
		return err
	}
	return inner
}

// Close terminates the connection. Idempotent: later calls return nil.
func (c *Conn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	return c.uc.Close()
}
