package observability

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"path/filepath"
	"sync"
)

// PprofServer serves the net/http/pprof handlers over an owner-only UNIX
// socket. It is a local, owner-gated diagnostic surface (ADR-0006; PRD F4):
// there is deliberately no way to bind it to a TCP address, so profiles can
// never be exposed off-host or to another user.
type PprofServer struct {
	path      string
	srv       *http.Server
	closeOnce sync.Once
	closeErr  error
}

// StartPprof binds a UNIX socket at path, tightens it to 0600, and serves the
// pprof index/cmdline/profile/symbol/trace handlers on it in a background
// goroutine until Close.
//
// Fail-closed preconditions:
//   - the parent directory must exist, be a real directory (not a symlink),
//     be owned by the current user, and grant no group/other access — the
//     same posture as the daemon's private runtime dir;
//   - nothing may already exist at path. A stale object is never removed or
//     bound over; the caller owns stale-socket policy.
//
// The listener is created first and the socket then chmod'ed to 0600; the
// parent directory's owner-only mode covers the window in between, so no
// other user can ever reach the socket.
func StartPprof(path string) (*PprofServer, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("observability: pprof socket path %q is not absolute", path)
	}
	if err := validateOwnerOnlyDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(path); err == nil {
		return nil, fmt.Errorf("observability: pprof socket path %q already exists (refusing to remove or bind over it)", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("observability: inspecting pprof socket path %q: %w", path, err)
	}

	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("observability: binding pprof socket %q: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("observability: tightening pprof socket %q to 0600: %w", path, err)
	}

	// A dedicated mux: the pprof handlers are wired explicitly so nothing is
	// ever served off http.DefaultServeMux (which third-party imports mutate).
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	s := &PprofServer{
		path: path,
		srv:  &http.Server{Handler: mux},
	}
	go func() {
		// Serve returns http.ErrServerClosed on Close; any other exit is a
		// diagnostic-surface failure and must not take the daemon down, so it
		// is intentionally dropped here (the socket simply stops answering).
		_ = s.srv.Serve(ln)
	}()
	return s, nil
}

// Path returns the bound socket path.
func (s *PprofServer) Path() string { return s.path }

// Close stops the server, closes the listener (which unlinks the socket), and
// removes any leftover socket file. Idempotent.
func (s *PprofServer) Close() error {
	s.closeOnce.Do(func() {
		err := s.srv.Close()
		if rmErr := os.Remove(s.path); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			err = errors.Join(err, rmErr)
		}
		s.closeErr = err
	})
	return s.closeErr
}
