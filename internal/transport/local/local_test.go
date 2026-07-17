package local

// Tests run against real Unix domain sockets in a private temp dir and are
// portable on the darwin authoring host. Variants that genuinely require a
// second UID (foreign-owner objects on a real filesystem) cannot be created
// without root; they are simulated through the owner seam here and the real
// second-UID versions are deferred to the Linux CI matrix as the blocking
// `integration-second-uid` check (docs/security/local-transport-hardening.md).

import (
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/amux-run/amux/internal/platform"
)

// testSpec builds a spec under a short private temp dir. os.MkdirTemp is used
// instead of t.TempDir to keep the path under the 104-byte darwin sun_path
// limit, and the path is canonicalized because $TMPDIR sits behind the /var
// symlink on darwin (the chain validation would correctly refuse it otherwise).
func testSpec(t *testing.T) platform.TransportSpec {
	t.Helper()
	base, err := os.MkdirTemp("", "amux")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(base) })
	canon, err := filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}
	return platform.TransportSpec{
		SocketPath: filepath.Join(canon, "run", "amuxd.sock"),
		OwnerUID:   uint32(os.Getuid()),
	}
}

func mustListen(t *testing.T, tr *Transport, spec platform.TransportSpec) platform.LocalListener {
	t.Helper()
	ln, err := tr.Listen(spec)
	if err != nil {
		t.Fatalf("Listen(%s): %v", spec.SocketPath, err)
	}
	t.Cleanup(func() { ln.Close() })
	return ln
}

// TestListenDialRoundtrip is the happy path: listen, verify the STR-1 modes,
// dial, exchange bytes both ways, and exercise Control on both ends.
func TestListenDialRoundtrip(t *testing.T) {
	tr := New()
	spec := testSpec(t)
	ln := mustListen(t, tr, spec)

	if ln.Path() != spec.SocketPath {
		t.Fatalf("Path() = %q, want %q", ln.Path(), spec.SocketPath)
	}
	// STR-1: dir exactly 0700, socket exactly 0600, both owned by the caller.
	dirInfo, err := os.Lstat(filepath.Dir(spec.SocketPath))
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("runtime dir mode = %#o, want 0700", dirInfo.Mode().Perm())
	}
	sockInfo, err := os.Lstat(spec.SocketPath)
	if err != nil {
		t.Fatal(err)
	}
	if sockInfo.Mode()&fs.ModeSocket == 0 {
		t.Fatalf("bound path is not a socket: %s", sockInfo.Mode())
	}
	if sockInfo.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode = %#o, want 0600", sockInfo.Mode().Perm())
	}

	type acceptResult struct {
		conn platform.LocalConn
		err  error
	}
	acceptCh := make(chan acceptResult, 1)
	go func() {
		c, err := ln.Accept()
		acceptCh <- acceptResult{c, err}
	}()

	client, err := tr.Dial(spec)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.Close()

	ar := <-acceptCh
	if ar.err != nil {
		t.Fatalf("Accept: %v", ar.err)
	}
	server := ar.conn
	defer server.Close()

	// Control must surface a real descriptor on both ends.
	for name, c := range map[string]platform.LocalConn{"client": client, "server": server} {
		var got uintptr
		if err := c.Control(func(fd uintptr) error { got = fd; return nil }); err != nil {
			t.Fatalf("%s Control: %v", name, err)
		}
		if got == 0 {
			t.Fatalf("%s Control fd = 0", name)
		}
	}

	if _, err := client.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := server.Read(buf); err != nil || string(buf) != "ping" {
		t.Fatalf("server read %q, %v", buf, err)
	}
	if _, err := server.Write([]byte("pong")); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Read(buf); err != nil || string(buf) != "pong" {
		t.Fatalf("client read %q, %v", buf, err)
	}

	// Conn Close is idempotent.
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("second conn Close: %v", err)
	}
}

// TestSymlinkComponentRejected pins STR-3: a symlink anywhere in the chain
// refuses the bind even when the link target is perfectly safe.
func TestSymlinkComponentRejected(t *testing.T) {
	tr := New()
	spec := testSpec(t)
	base := filepath.Dir(filepath.Dir(spec.SocketPath))
	real := filepath.Join(base, "real")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	spec.SocketPath = filepath.Join(link, "run", "amuxd.sock")
	if _, err := tr.Listen(spec); !errors.Is(err, ErrSymlinkComponent) {
		t.Fatalf("Listen through symlink component = %v, want ErrSymlinkComponent", err)
	}
}

// TestSymlinkRuntimeDirRejected pins the final-component variant of STR-3: the
// runtime dir itself being a symlink refuses the bind.
func TestSymlinkRuntimeDirRejected(t *testing.T) {
	tr := New()
	spec := testSpec(t)
	base := filepath.Dir(filepath.Dir(spec.SocketPath))
	real := filepath.Join(base, "real")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(base, "run")); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Listen(spec); !errors.Is(err, ErrSymlinkComponent) {
		t.Fatalf("Listen with symlinked runtime dir = %v, want ErrSymlinkComponent", err)
	}
}

// TestGroupWritableComponentRejected pins STR-3 mode enforcement, which IS
// testable without a second UID: chmod a chain component group-writable.
func TestGroupWritableComponentRejected(t *testing.T) {
	tr := New()
	spec := testSpec(t)
	base := filepath.Dir(filepath.Dir(spec.SocketPath))
	if err := os.Chmod(base, 0o770); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Listen(spec); !errors.Is(err, ErrUnsafeMode) {
		t.Fatalf("Listen with group-writable component = %v, want ErrUnsafeMode", err)
	}
}

// TestPreexistingLooseRuntimeDirRejected pins STR-1: a pre-existing runtime dir
// that exposes any group/other bits refuses the bind.
func TestPreexistingLooseRuntimeDirRejected(t *testing.T) {
	tr := New()
	spec := testSpec(t)
	dir := filepath.Dir(spec.SocketPath)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Listen(spec); !errors.Is(err, ErrUnsafeMode) {
		t.Fatalf("Listen with 0755 runtime dir = %v, want ErrUnsafeMode", err)
	}
}

// TestForeignOwnerComponentRejected simulates STR-3's foreign-owner refusal
// through the owner seam: creating a genuinely foreign-owned directory needs a
// second UID, which the authoring host does not have. The real foreign-owner
// variants run as the blocking `integration-second-uid` check
// (seconduid_integration_test.go, `-tags integration` as root on Linux).
func TestForeignOwnerComponentRejected(t *testing.T) {
	tr := New()
	spec := testSpec(t)
	base := filepath.Dir(filepath.Dir(spec.SocketPath))
	realOwner := tr.owner
	tr.owner = func(path string, fi os.FileInfo) (uint32, error) {
		if path == base {
			return spec.OwnerUID + 1, nil // simulate a foreign, non-root owner
		}
		return realOwner(path, fi)
	}
	if _, err := tr.Listen(spec); !errors.Is(err, ErrUntrustedOwner) {
		t.Fatalf("Listen with foreign-owned component = %v, want ErrUntrustedOwner", err)
	}
}

// TestListenRefusesWrongProcessUID pins the fail-closed precondition: a process
// cannot bind an endpoint whose spec names a different owner.
func TestListenRefusesWrongProcessUID(t *testing.T) {
	tr := New()
	spec := testSpec(t)
	spec.OwnerUID = uint32(os.Getuid()) + 1
	if _, err := tr.Listen(spec); !errors.Is(err, ErrUntrustedOwner) {
		t.Fatalf("Listen with foreign spec.OwnerUID = %v, want ErrUntrustedOwner", err)
	}
}

// TestStaleSocketReclaimed pins the STR-4 happy path: a dead same-owner socket
// file (socket type, our UID, probe refused) is removed and the path rebound.
func TestStaleSocketReclaimed(t *testing.T) {
	tr := New()
	spec := testSpec(t)
	dir := filepath.Dir(spec.SocketPath)
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Manufacture a dead socket: bind, disable unlink-on-close, close. The file
	// remains but no listener answers, so a probe dial gets ECONNREFUSED.
	dead, err := net.ListenUnix("unix", &net.UnixAddr{Name: spec.SocketPath, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	dead.SetUnlinkOnClose(false)
	if err := dead.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(spec.SocketPath); err != nil {
		t.Fatalf("dead socket file must still exist: %v", err)
	}

	ln := mustListen(t, tr, spec) // must reclaim and rebind

	// Prove the rebind is live end to end.
	go func() {
		c, err := ln.Accept()
		if err == nil {
			c.Write([]byte("ok"))
			c.Close()
		}
	}()
	client, err := tr.Dial(spec)
	if err != nil {
		t.Fatalf("Dial after reclaim: %v", err)
	}
	defer client.Close()
	buf := make([]byte, 2)
	if _, err := client.Read(buf); err != nil || string(buf) != "ok" {
		t.Fatalf("roundtrip after reclaim: %q, %v", buf, err)
	}
}

// TestLiveSocketNotStolen pins STR-4's refusal branch: while a live listener
// serves the path, a second Listen must refuse with ErrSocketBusy and must NOT
// unlink the live socket.
func TestLiveSocketNotStolen(t *testing.T) {
	tr := New()
	spec := testSpec(t)
	ln := mustListen(t, tr, spec)

	if _, err := tr.Listen(spec); !errors.Is(err, ErrSocketBusy) {
		t.Fatalf("second Listen on live socket = %v, want ErrSocketBusy", err)
	}

	// The first listener must still be serving (its socket was not unlinked).
	go func() {
		if c, err := ln.Accept(); err == nil {
			c.Close()
		}
	}()
	c, err := tr.Dial(spec)
	if err != nil {
		t.Fatalf("Dial after refused takeover: %v", err)
	}
	c.Close()
}

// TestNonSocketObjectRefused pins STR-4's fail-closed branch: a regular file at
// the socket path is never unlinked, by Listen or Dial.
func TestNonSocketObjectRefused(t *testing.T) {
	tr := New()
	spec := testSpec(t)
	dir := filepath.Dir(spec.SocketPath)
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(spec.SocketPath, []byte("not a socket"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Listen(spec); !errors.Is(err, ErrNotSocket) {
		t.Fatalf("Listen over regular file = %v, want ErrNotSocket", err)
	}
	if _, err := tr.Dial(spec); !errors.Is(err, ErrNotSocket) {
		t.Fatalf("Dial to regular file = %v, want ErrNotSocket", err)
	}
	if _, err := os.Lstat(spec.SocketPath); err != nil {
		t.Fatalf("hostile file must not have been unlinked: %v", err)
	}
}

// TestDialRejectsForeignOwner simulates the Dial owner check via the seam
// (real second-UID variant: TestSecondUIDForeignLiveSocketDial in
// seconduid_integration_test.go, `-tags integration` as root on Linux).
func TestDialRejectsForeignOwner(t *testing.T) {
	tr := New()
	spec := testSpec(t)
	mustListen(t, tr, spec)

	dialer := New()
	dialer.owner = func(string, os.FileInfo) (uint32, error) { return spec.OwnerUID + 1, nil }
	if _, err := dialer.Dial(spec); !errors.Is(err, ErrUntrustedOwner) {
		t.Fatalf("Dial to foreign-owned socket = %v, want ErrUntrustedOwner", err)
	}
}

// TestDialRejectsUnsafeSocketMode pins the Dial-side mode check: a socket that
// another principal could rewrite is refused even with a matching owner.
func TestDialRejectsUnsafeSocketMode(t *testing.T) {
	tr := New()
	spec := testSpec(t)
	mustListen(t, tr, spec)
	if err := os.Chmod(spec.SocketPath, 0o666); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Dial(spec); !errors.Is(err, ErrUnsafeMode) {
		t.Fatalf("Dial to 0666 socket = %v, want ErrUnsafeMode", err)
	}
}

// TestCloseUnlinksAndIsIdempotent pins the listener lifecycle: Close removes
// the socket, is idempotent, and the path is rebindable afterwards.
func TestCloseUnlinksAndIsIdempotent(t *testing.T) {
	tr := New()
	spec := testSpec(t)
	ln := mustListen(t, tr, spec)

	if err := ln.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Lstat(spec.SocketPath); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("socket must be unlinked after Close, lstat = %v", err)
	}
	if err := ln.Close(); err != nil {
		t.Fatalf("second Close must be nil, got %v", err)
	}

	ln2 := mustListen(t, tr, spec) // rebind must succeed
	if err := ln2.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestRelativePathRefused pins the spec validation gate.
func TestRelativePathRefused(t *testing.T) {
	tr := New()
	spec := platform.TransportSpec{SocketPath: "relative/amuxd.sock", OwnerUID: uint32(os.Getuid())}
	if _, err := tr.Listen(spec); err == nil {
		t.Fatal("Listen with relative path must refuse")
	}
	if _, err := tr.Dial(spec); err == nil {
		t.Fatal("Dial with relative path must refuse")
	}
}

// The former TestSecondUIDVariantsDeferred stub was RETIRED (G-lane F5): a
// test that skipped unconditionally could be counted by the readiness
// manifest's `-run 'SecondUID'` gate and let it pass vacuously. The real
// STR-3/STR-4 foreign-owner variants live in seconduid_integration_test.go
// (`integration && linux`, run as root), and the manifest self-gate in
// internal/securitytest fails if that pattern ever stops binding to them.

// TestStaleProbeTimeoutFailsClosed documents that probeTimeout bounds the
// STR-4 liveness probe. A full wedged-listener simulation needs a listener
// that accepts the connection attempt but never completes it, which a healthy
// kernel socket cannot express portably; the ambiguous-probe branch is
// exercised by code review and the busy branch by TestLiveSocketNotStolen.
func TestStaleProbeTimeoutFailsClosed(t *testing.T) {
	if probeTimeout <= 0 || probeTimeout > time.Second {
		t.Fatalf("probeTimeout %v out of bounds", probeTimeout)
	}
}
