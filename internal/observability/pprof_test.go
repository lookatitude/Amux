package observability

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// unixClient returns an http.Client whose every request is dialed to the given
// UNIX socket path, regardless of the request URL host.
func unixClient(path string) *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", path)
			},
		},
	}
}

// ownerOnlyTempDir returns a fresh short-pathed temp dir tightened to 0700.
// Not t.TempDir(): its per-test path segment can push a UNIX socket path past
// the kernel sun_path limit (104 bytes on darwin), and it applies the process
// umask (often yielding 0755) where the pprof surface requires the private
// runtime-dir posture.
func ownerOnlyTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "amuxobs")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("chmod temp dir: %v", err)
	}
	return dir
}

func startTestPprof(t *testing.T) (*PprofServer, string) {
	t.Helper()
	path := filepath.Join(ownerOnlyTempDir(t), "pprof.sock")
	srv, err := StartPprof(path)
	if err != nil {
		t.Fatalf("StartPprof(%q): %v", path, err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return srv, path
}

// TestStartPprofServesCmdlineOverUnixSocket starts the server on a temp path
// and asserts GET /debug/pprof/cmdline answers 200 through a unix-dialer.
func TestStartPprofServesCmdlineOverUnixSocket(t *testing.T) {
	_, path := startTestPprof(t)
	resp, err := unixClient(path).Get("http://amux-pprof/debug/pprof/cmdline")
	if err != nil {
		t.Fatalf("GET /debug/pprof/cmdline: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if len(body) == 0 {
		t.Fatal("cmdline body is empty")
	}
}

// TestStartPprofSocketModeOwnerOnly asserts the bound socket is 0600.
func TestStartPprofSocketModeOwnerOnly(t *testing.T) {
	_, path := startTestPprof(t)
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("Lstat(%q): %v", path, err)
	}
	if fi.Mode().Type() != os.ModeSocket {
		t.Fatalf("path is %v, want a socket", fi.Mode().Type())
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("socket mode = %o, want 0600", perm)
	}
}

// TestStartPprofRejectsExistingPath asserts an existing object at the path —
// even a stale one — is never removed or bound over: fail closed.
func TestStartPprofRejectsExistingPath(t *testing.T) {
	path := filepath.Join(ownerOnlyTempDir(t), "pprof.sock")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("plant existing file: %v", err)
	}
	srv, err := StartPprof(path)
	if err == nil {
		_ = srv.Close()
		t.Fatal("StartPprof on an existing path succeeded, want error")
	}
	if _, statErr := os.Lstat(path); statErr != nil {
		t.Fatalf("existing object was removed by the failed start: %v", statErr)
	}
}

// TestStartPprofRejectsNonOwnerOnlyDir asserts the parent directory must be
// owner-only (no group/other access), matching the runtime-dir posture.
func TestStartPprofRejectsNonOwnerOnlyDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	srv, err := StartPprof(filepath.Join(dir, "pprof.sock"))
	if err == nil {
		_ = srv.Close()
		t.Fatal("StartPprof inside a group/other-accessible dir succeeded, want error")
	}
}

// TestPprofCloseRemovesSocket asserts Close stops serving and unlinks the
// socket; a second Close is a no-op.
func TestPprofCloseRemovesSocket(t *testing.T) {
	srv, path := startTestPprof(t)
	if err := srv.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("socket still present after Close (err=%v)", err)
	}
	if err := srv.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
