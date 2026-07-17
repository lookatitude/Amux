// Package platform defines the narrow, implementation-neutral interfaces that
// keep every OS-specific concern behind a seam (ADR-0006). The domain, control,
// session, protocol, and persistence layers depend only on these interfaces;
// Linux is the sole implemented platform in the MVP, and Darwin/Windows exist
// here only as compile-time placeholders that fail closed with
// ErrUnsupportedPlatform. Adding a real non-Linux implementation is a supported-
// platform change and requires spec confirmation — it must never happen by
// silently filling in a stub.
//
// Each interface is deliberately minimal: it exposes exactly the capability the
// runtime needs, so a future port implements a small surface and cannot leak
// platform types (e.g. raw syscall structs) into the domain.
package platform

import (
	"errors"
	"io"
	"os"
)

// ErrUnsupportedPlatform is returned by every capability whose current build
// target has no real implementation. Callers fail closed on it rather than
// degrading silently.
var ErrUnsupportedPlatform = errors.New("amux: capability not supported on this platform")

// FSIdentity is a filesystem object's stable identity tuple: the device and
// inode numbers behind a canonicalized path. It underpins project trust: a
// project's durable key is SHA-256(realpath || dev || ino), so moving,
// replacing, or remounting a root changes the identity and invalidates trust
// (spec "Project identity and trust boundary").
type FSIdentity struct {
	Dev uint64
	Ino uint64
}

// FilesystemIdentity resolves the FSIdentity of a path. Implemented on Linux and
// Darwin (both expose dev/ino via stat); Windows is a placeholder.
type FilesystemIdentity interface {
	// Identify canonicalizes path (resolving symlinks) and returns the resolved
	// absolute path plus its device/inode identity.
	Identify(path string) (realpath string, id FSIdentity, err error)
}

// PeerCredentials validates the identity of a connected local-socket peer. On
// Linux this is SO_PEERCRED (mandatory per PRD F4); other platforms fail closed.
type PeerCredentials interface {
	// PeerUID returns the connected peer's effective UID from a raw connection
	// file descriptor. A mismatch with the daemon owner must reject the peer.
	PeerUID(rawConnFD uintptr) (uid uint32, err error)
}

// Containment models daemon-death descendant containment (spec MVP feature
// "PTY/signal edge cases"; ADR-0006 §containment). A Linux implementation uses a
// guardian/cgroup/parent-death strategy so that killing amuxd with SIGKILL reaps
// every descendant, including double-forked grandchildren and process-group
// escapees. This is Linux-only; the runtime evidence is deferred to a Linux host.
type Containment interface {
	// Prepare returns SysProcAttr-shaped options (opaque here) and a cleanup
	// handle that, when the daemon dies, guarantees descendant termination.
	Prepare(spec ContainmentSpec) (ContainmentHandle, error)
}

// ContainmentSpec configures a contained launch.
type ContainmentSpec struct {
	// NewProcessGroup places the child in its own process group so signals can
	// target the whole tree.
	NewProcessGroup bool
	// Label is a human tag used in the cgroup/guardian name for diagnostics.
	Label string
}

// ContainmentHandle is the live containment for one child tree.
type ContainmentHandle interface {
	// KillTree terminates the whole descendant tree (SIGTERM then SIGKILL after
	// the grace period). Idempotent.
	KillTree() error
	// Close releases containment resources (e.g. removes the cgroup).
	Close() error
}

// DescriptorLaunch performs a race-safe, descriptor-bound executable launch
// (spec "Linearizable hook launch contract"; PRD F9). The Linux implementation
// opens the executable and config with openat2 (no symlink/mount traversal),
// verifies their digests against the grant, and launches the exact opened
// descriptor with execveat/fexecve — so a symlink swap, rename, or byte
// replacement between check and exec cannot substitute a different object.
// Path-only revalidation is explicitly insufficient. Linux-only; deferred
// runtime evidence.
type DescriptorLaunch interface {
	// OpenBound opens path relative to a trusted directory descriptor with
	// no-symlink, no-magiclinks resolution, returning a descriptor bound to the
	// resolved inode plus its FSIdentity for digest/epoch checks.
	OpenBound(dirFD int, path string) (fd int, id FSIdentity, err error)
	// LaunchBound execs the already-open descriptor (fexecve/execveat semantics)
	// with the given argv/env, after the caller has re-validated digest+epoch.
	LaunchBound(fd int, argv []string, env []string, spec LaunchSpec) (pid int, err error)
}

// LaunchSpec carries the non-secret launch parameters for a bound launch.
type LaunchSpec struct {
	Dir             string // scratch or granted cwd, already validated
	Containment     ContainmentSpec
	CloseDescOnExec bool
}

// ProcessInspector reads live process context for a pane (foreground command,
// PID liveness, exit state) without owning the process (PRD F8 context
// collectors). Linux reads /proc; other platforms fail closed.
type ProcessInspector interface {
	// ForegroundPID returns the foreground process group leader of a PTY.
	ForegroundPID(ptyFD uintptr) (pid int, err error)
	// Alive reports whether pid is still a live process.
	Alive(pid int) (bool, error)
}

// Clock is the injectable time source. Production wires a real monotonic clock;
// tests wire a deterministic fake so time-dependent behavior (deadlines,
// heartbeats, the 250 ms trust gate) is reproducible.
type Clock interface {
	// NowUnixMilli returns the current wall-clock time in Unix milliseconds.
	NowUnixMilli() int64
	// MonotonicNanos returns a monotonic reading for measuring durations; it is
	// unaffected by wall-clock adjustments.
	MonotonicNanos() int64
}

// PTY spawns child processes on a pseudo-terminal, exposing exactly the
// spawn/resize/input/output/signal/reap surface the session supervisor needs
// (ADR-0006 §PTY). The T4 Unix implementation wraps github.com/creack/pty —
// the capability spikes/pty proved on the author host — but no package above
// this seam may see that library or any termios/ioctl/syscall type.
type PTY interface {
	// Start launches spec.Argv on a fresh pseudo-terminal under the containment
	// described by spec.Containment, so daemon death cannot orphan descendants.
	Start(spec PTYSpec) (PTYHandle, error)
}

// PTYSpec describes one PTY launch. Env is the fully resolved, explicit
// non-secret environment (the caller applies the persistence allowlist from
// ADR-0005; this layer never filters).
type PTYSpec struct {
	Argv        []string
	Dir         string
	Env         []string
	Size        PTYSize
	Containment ContainmentSpec
	// UseCgroupFD asks the Linux PTY launcher to place the child into CgroupFD
	// atomically during clone, before user code can fork. Other platforms ignore
	// both fields. The Supervisor populates them from a prepared containment
	// handle; callers outside the PTY/containment assembly leave them zero.
	UseCgroupFD bool
	CgroupFD    int
}

// PTYSize is terminal geometry in cells.
type PTYSize struct {
	Rows uint16
	Cols uint16
}

// PTYExit classifies how the child terminated. Code is the exit status of a
// normal exit and Signal is empty; for a signal death Signal names the signal
// (e.g. "SIGKILL") and Code is implementation-defined and must not be trusted.
type PTYExit struct {
	Code   int
	Signal string
}

// PTYHandle is one live pseudo-terminal session. Read yields master-side
// output bytes — the daemon's event/replay pipeline is the sole consumer
// (ADR-0004 output sequencing). Write injects input bytes; input-lease
// validation (ADR-0004) happens in the attach layer, so an expired lease is
// rejected before bytes ever reach this seam.
type PTYHandle interface {
	io.Reader
	io.Writer
	// Resize applies new geometry and delivers SIGWINCH to the foreground group.
	Resize(size PTYSize) error
	// Signal sends sig to the child (whole group when contained).
	Signal(sig os.Signal) error
	// Wait blocks until the child terminates and reaps it, returning the exit
	// classification. Call exactly once per handle.
	Wait() (PTYExit, error)
	// MasterFD exposes the master descriptor solely for
	// ProcessInspector.ForegroundPID queries; callers must not read, write, or
	// close it directly.
	MasterFD() uintptr
	// Close releases the master descriptor. Idempotent. It does not imply child
	// termination — that is Signal/containment territory.
	Close() error
}

// LocalTransport owns the owner-only local control endpoint lifecycle
// (ADR-0003 transport binding; ADR-0006). Listen must validate every
// runtime-path component (no symlink traversal, expected owner, safe mode),
// bind the endpoint owner-only, and remove a stale socket only after proving
// its ownership/type and the absence of a live owner. The Linux mechanism
// (Unix socket listener, permissions) lands in T4 internal/transport/local;
// no package above this seam may touch socket APIs directly.
type LocalTransport interface {
	// Listen binds the daemon control endpoint described by spec.
	Listen(spec TransportSpec) (LocalListener, error)
	// Dial connects a client to the endpoint, failing closed when the socket's
	// ownership or mode does not match spec.OwnerUID.
	Dial(spec TransportSpec) (LocalConn, error)
}

// TransportSpec locates and constrains the control endpoint.
type TransportSpec struct {
	// SocketPath is the absolute socket path beneath the private runtime
	// directory (spec: $XDG_RUNTIME_DIR/amux/).
	SocketPath string
	// OwnerUID is the only UID permitted to own the runtime path and connect.
	OwnerUID uint32
}

// LocalListener accepts control connections. Accept is structural only:
// per-connection identity is verified by the daemon via
// PeerCredentials.PeerUID against the connection's raw descriptor before the
// first protocol byte (PRD F4 mandates this on Linux).
type LocalListener interface {
	// Accept returns the next connection.
	Accept() (LocalConn, error)
	// Path returns the bound socket path for health and cleanup reporting.
	Path() string
	// Close closes the listener and unlinks the socket. Idempotent.
	Close() error
}

// LocalConn is one control-connection byte stream. Framing, negotiation, and
// envelopes live in the protocol layer (ADR-0003), never here.
type LocalConn interface {
	io.Reader
	io.Writer
	// Control runs f with the connection's raw file descriptor — mirroring
	// syscall.RawConn semantics without leaking that type above the seam — so
	// the daemon can run the mandatory PeerCredentials.PeerUID check. The
	// descriptor is valid only for the duration of f.
	Control(f func(fd uintptr) error) error
	// Close terminates the connection. Idempotent.
	Close() error
}

// Notifier delivers best-effort desktop notifications (ADR-0006; PRD F8). The
// daemon-owned store (live SQLite, ADR-0005) is the sole authority for
// notification state: a delivery failure is advisory and must never create,
// remove, or mark the in-app notification. The Linux delivery mechanism lands
// in T4 internal/notify; other platforms fail closed with
// ErrUnsupportedPlatform.
type Notifier interface {
	// Notify attempts one desktop delivery. Errors are advisory only.
	Notify(n Notification) error
}

// Notification is the implementation-neutral desktop payload.
type Notification struct {
	Title   string
	Body    string
	Urgency NotifyUrgency
}

// NotifyUrgency maps to the desktop urgency hint.
type NotifyUrgency uint8

const (
	NotifyUrgencyLow NotifyUrgency = iota
	NotifyUrgencyNormal
	NotifyUrgencyCritical
)
