//go:build integration && linux

package local

// Second-UID transport hardening cases (T6 QA, work package Q5). These are
// the real foreign-owner variants that local_test.go can only simulate through
// the owner seam on the authoring host: they create genuinely foreign-owned
// filesystem objects, which requires root plus a second UID, so they run as
// the blocking `integration-second-uid` check on Linux
// (docs/security/readiness-manifest.json):
//
//	go test -count=1 -tags integration -run 'SecondUID' ./internal/transport/local
//
// Each case asserts the STR-3/STR-4 typed refusal — never a soft skip when
// the prerequisites (root on Linux) are present.

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/amux-run/amux/internal/platform"
)

// foreignUID is the conventional unprivileged "nobody" account present in the
// Arch and Ubuntu base images this check runs on.
const foreignUID = 65534

func requireRoot(t *testing.T) {
	t.Helper()
	if os.Getuid() != 0 {
		t.Skip("integration-second-uid requires root to create foreign-owned objects")
	}
}

// TestSecondUIDForeignChainComponent: a runtime-path component owned by a
// different UID must refuse Listen (STR-3: never trust another principal's
// directory).
func TestSecondUIDForeignChainComponent(t *testing.T) {
	requireRoot(t)
	base := t.TempDir()
	run := filepath.Join(base, "run")
	if err := os.Mkdir(run, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(run, foreignUID, foreignUID); err != nil {
		t.Fatal(err)
	}
	spec := testSpecAt(t, filepath.Join(run, "amux", "amuxd.sock"))
	if _, err := New().Listen(spec); !errors.Is(err, ErrUntrustedOwner) {
		t.Fatalf("Listen over foreign-owned component = %v, want ErrUntrustedOwner", err)
	}
}

// TestSecondUIDForeignStaleSocket: a dead socket owned by another UID is never
// unlinked or reused (STR-4: reclaim requires proof of same-owner death). A
// clean listener unlinks its socket, so the stale object is left behind the
// way a crash leaves it: bound, then closed without unlinking.
func TestSecondUIDForeignStaleSocket(t *testing.T) {
	requireRoot(t)
	spec := testSpecAt(t, filepath.Join(t.TempDir(), "run", "amuxd.sock"))
	if err := os.MkdirAll(filepath.Dir(spec.SocketPath), 0o700); err != nil {
		t.Fatal(err)
	}
	raw, err := net.ListenUnix("unix", &net.UnixAddr{Name: spec.SocketPath, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	raw.SetUnlinkOnClose(false)
	raw.Close() // crash-shaped: the socket file survives with no listener
	if err := os.Chown(spec.SocketPath, foreignUID, foreignUID); err != nil {
		t.Fatal(err)
	}
	if _, err := New().Listen(spec); !errors.Is(err, ErrUntrustedOwner) {
		t.Fatalf("Listen over foreign-owned stale socket = %v, want ErrUntrustedOwner", err)
	}
	if _, statErr := os.Lstat(spec.SocketPath); statErr != nil {
		t.Fatalf("foreign-owned socket was removed: %v", statErr)
	}
}

// TestSecondUIDForeignLiveSocketDial: a live socket owned by another UID must
// refuse Dial (STR-4: the client never talks to a foreign daemon).
func TestSecondUIDForeignLiveSocketDial(t *testing.T) {
	requireRoot(t)
	spec := testSpecAt(t, filepath.Join(t.TempDir(), "run", "amuxd.sock"))
	ln, err := New().Listen(spec)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if err := os.Chown(spec.SocketPath, foreignUID, foreignUID); err != nil {
		t.Fatal(err)
	}
	if _, err := New().Dial(spec); !errors.Is(err, ErrUntrustedOwner) {
		t.Fatalf("Dial to foreign-owned live socket = %v, want ErrUntrustedOwner", err)
	}
}

// TestSecondUIDExpectedOwnerMismatch: dialing with a spec that expects a
// different owner refuses even when the socket is healthy (the OwnerUID
// contract is exact, not advisory).
func TestSecondUIDExpectedOwnerMismatch(t *testing.T) {
	requireRoot(t)
	spec := testSpecAt(t, filepath.Join(t.TempDir(), "run", "amuxd.sock"))
	ln, err := New().Listen(spec)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	foreign := spec
	foreign.OwnerUID = foreignUID
	if _, err := New().Dial(foreign); !errors.Is(err, ErrUntrustedOwner) {
		t.Fatalf("Dial expecting foreign owner = %v, want ErrUntrustedOwner", err)
	}
}

// testSpecAt mirrors testSpec but at a caller-chosen socket path (the
// second-UID cases need to doctor specific components).
func testSpecAt(t *testing.T, socket string) platform.TransportSpec {
	t.Helper()
	canon, err := filepath.EvalSymlinks(filepath.Dir(filepath.Dir(socket)))
	if err != nil {
		t.Fatal(err)
	}
	return platform.TransportSpec{
		SocketPath: filepath.Join(canon, filepath.Base(filepath.Dir(socket)), filepath.Base(socket)),
		OwnerUID:   uint32(os.Getuid()),
	}
}
