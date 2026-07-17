// run.go assembles the full amuxd runtime: XDG paths, JSONC config, boot
// identity, the SQLite store, the control actor over the durable trust seam,
// the notification service, the engine, and the protocol server on the
// owner-only control socket — then blocks until a shutdown signal, a client
// shutdown request, or context cancellation, and tears everything down
// reaping every PTY. cmd/amuxd is a thin flag wrapper over Run so tests can
// exercise the exact production assembly in-process (with only the Linux-only
// peer-credential seam injected off-Linux).
package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/amux-run/amux/internal/config"
	panectx "github.com/amux-run/amux/internal/context"
	"github.com/amux-run/amux/internal/control"
	"github.com/amux-run/amux/internal/notify"
	"github.com/amux-run/amux/internal/observability"
	"github.com/amux-run/amux/internal/platform"
	"github.com/amux-run/amux/internal/protocol"
	"github.com/amux-run/amux/internal/redact"
	"github.com/amux-run/amux/internal/store"
	"github.com/amux-run/amux/internal/transport/local"
)

// RunOptions parameterizes Run. The zero value is the production assembly.
type RunOptions struct {
	// Getenv resolves the environment (XDG paths). Nil means os.Getenv.
	Getenv func(string) string
	// Logger receives all daemon diagnostics; it must NEVER write to stdout
	// (PRD F10: logs stay off protocol and TUI streams). Nil means slog to
	// stderr at the configured level.
	Logger *slog.Logger
	// Peers overrides the SO_PEERCRED seam. Nil wires the production
	// implementation, which is Linux-only and fails closed elsewhere
	// (ADR-0006); tests off Linux inject a fake.
	Peers platform.PeerCredentials
	// Ready, when non-nil, is closed once the daemon is accepting connections
	// (test synchronization).
	Ready chan<- struct{}
}

// Run starts the daemon and blocks until shutdown. It returns nil on a clean
// shutdown (signal, daemon.shutdown request, or ctx cancellation).
func Run(ctx context.Context, opts RunOptions) error {
	getenv := opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	paths, err := config.Resolve(getenv)
	if err != nil {
		return err
	}
	cfg, err := config.Load(paths.ConfigFile())
	if err != nil {
		return err
	}
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel(cfg.LogLevel)}))
	}

	for _, dir := range []string{paths.StateDir, paths.DataDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("amuxd: create %s: %w", dir, err)
		}
	}

	// Boot identity: fresh per daemon incarnation, carried in welcome and
	// every event so clients detect restarts (ADR-0003).
	bootID := control.NewID()
	clock := platform.NewSystemClock()

	st, err := store.Open(filepath.Join(paths.StateDir, "db"))
	if err != nil {
		return err
	}
	defer st.Close()

	ctrl := control.New(control.Deps{Clock: clock, Store: NewTrustStore(st)})
	ctrl.Start()
	defer ctrl.Stop()

	var notifier platform.Notifier = notify.NopNotifier{}
	if cfg.Notifications.Desktop {
		notifier = notify.NewDesktopNotifier()
	}
	notifySvc, err := notify.NewService(st, notifier, clock, redact.New().Redact, log)
	if err != nil {
		return err
	}

	// B10 pane-context collectors (pane.context projection): bounded git facts
	// and the foreground-process probe. Both fail closed off Linux / on error —
	// the projection reports honest absence, never a fabricated field.
	gitCollector := &panectx.GitCollector{}
	fgCollector := &panectx.ForegroundCollector{
		Inspector: platform.NewLinuxProcessInspector(),
		Comm:      panectx.NewCommProber(),
	}
	engine, err := New(Deps{
		Control:     ctrl,
		Clock:       clock,
		Store:       st,
		SnapshotDir: filepath.Join(paths.StateDir, "snapshots"),
		ReplayBytes: int(cfg.Replay.PerSurfaceBytes),
		GitContext:  gitCollector.Collect,
		Foreground:  fgCollector.Collect,
	})
	if err != nil {
		return err
	}
	defer engine.Close()

	peers := opts.Peers
	if peers == nil {
		peers = platform.NewLinuxPeerCredentials()
	}
	metrics := observability.NewRegistry()
	shutdown := make(chan struct{})
	var once sync.Once
	shutdownOnce := func() { once.Do(func() { close(shutdown) }) }
	srv, err := NewServer(ServerConfig{
		Engine:     engine,
		Control:    ctrl,
		Store:      st,
		Notify:     notifySvc,
		Metrics:    metrics,
		BootID:     bootID,
		Peers:      peers,
		Clock:      clock,
		Log:        log,
		OnShutdown: shutdownOnce,
	})
	if err != nil {
		return err
	}

	// Owner-only control socket beneath the private runtime dir. Listen owns
	// single-instance semantics: a live socket is never stolen, a stale one is
	// reclaimed only after proof (internal/transport/local).
	if err := os.MkdirAll(paths.RuntimeDir, 0o700); err != nil {
		return fmt.Errorf("amuxd: create runtime dir: %w", err)
	}
	spec := platform.TransportSpec{SocketPath: paths.SocketPath(), OwnerUID: uint32(os.Getuid())}
	ln, err := local.New().Listen(spec)
	if err != nil {
		return err
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	sigc := make(chan os.Signal, 2)
	signal.Notify(sigc, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigc)

	log.Info("amuxd ready", "boot_id", bootID, "socket", spec.SocketPath, "version", "amuxd")
	if opts.Ready != nil {
		close(opts.Ready)
	}

	serveReturned := false
	select {
	case sig := <-sigc:
		log.Info("amuxd shutting down", "signal", sig.String())
	case <-shutdown:
		log.Info("amuxd shutting down", "reason", "client shutdown request")
	case <-ctx.Done():
		log.Info("amuxd shutting down", "reason", "context cancelled")
	case err := <-serveErr:
		serveReturned = true
		if err != nil && !errors.Is(err, protocol.ErrServerClosed) {
			srv.Close()
			return err
		}
	}
	srv.Close()
	if !serveReturned {
		<-serveErr
	}
	return nil
}

// logLevel maps the validated config level to slog.
func logLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
