package daemon

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/amux-run/amux/internal/client"
	"github.com/amux-run/amux/internal/config"
	"github.com/amux-run/amux/internal/platform"
	"github.com/amux-run/amux/internal/rpcapi"
	"github.com/amux-run/amux/internal/transport/local"
)

// testEnv builds a hermetic XDG environment rooted in a short temp dir (darwin
// sun_path limit) whose /var symlink is resolved so transport path validation
// passes.
func testEnv(t *testing.T) (func(string) string, platform.TransportSpec) {
	t.Helper()
	base, err := os.MkdirTemp("", "amuxrun")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(base) })
	canon, err := filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"HOME":            canon,
		"XDG_RUNTIME_DIR": filepath.Join(canon, "run"),
	}
	if err := os.MkdirAll(env["XDG_RUNTIME_DIR"], 0o700); err != nil {
		t.Fatal(err)
	}
	paths, err := config.Resolve(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	return func(k string) string { return env[k] },
		platform.TransportSpec{SocketPath: paths.SocketPath(), OwnerUID: uint32(os.Getuid())}
}

// TestRunProductionAssembly boots the EXACT production wiring (Run: XDG paths,
// config, SQLite store, control actor over the durable trust seam, engine,
// owner-only socket) in-process — only the Linux-only peer-credential seam is
// injected off Linux — then drives it through the shared client and shuts it
// down cleanly via the protocol.
func TestRunProductionAssembly(t *testing.T) {
	getenv, spec := testEnv(t)
	ready := make(chan struct{})
	runErr := make(chan error, 1)
	go func() {
		runErr <- Run(context.Background(), RunOptions{
			Getenv: getenv,
			Logger: slog.New(slog.DiscardHandler),
			Peers:  fakePeers{uid: uint32(os.Getuid())},
			Ready:  ready,
		})
	}()
	select {
	case <-ready:
	case err := <-runErr:
		t.Fatalf("Run exited before ready: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("daemon never became ready")
	}

	ctx := context.Background()
	cli, err := client.Dial(ctx, local.New(), spec, "run-test")
	if err != nil {
		t.Fatalf("dial production socket: %v", err)
	}
	defer cli.Close()

	var health rpcapi.HealthResult
	if err := cli.Call(ctx, rpcapi.MethodDaemonHealth, nil, &health); err != nil {
		t.Fatalf("daemon.health: %v", err)
	}
	if health.BootID == "" || health.BootID != cli.BootID() {
		t.Fatalf("health boot id %q vs welcome %q", health.BootID, cli.BootID())
	}

	// A second daemon must refuse to start while the socket is live
	// (single-instance semantics: a live socket is never stolen).
	if err := Run(ctx, RunOptions{Getenv: getenv, Logger: slog.New(slog.DiscardHandler), Peers: fakePeers{uid: uint32(os.Getuid())}}); err == nil {
		t.Fatal("second daemon on a live socket must fail")
	}

	// Full trust round-trip against the real SQLite-backed truststore.
	project := t.TempDir()
	var epoch rpcapi.EpochResult
	if err := cli.Call(ctx, rpcapi.MethodHookApprove, rpcapi.HookApproveParams{Project: project, Confirm: true}, &epoch); err != nil {
		t.Fatalf("hook.approve over production assembly: %v", err)
	}
	if epoch.Epoch == 0 {
		t.Fatalf("approve epoch = %+v", epoch)
	}

	var res rpcapi.ShutdownResult
	if err := cli.Call(ctx, rpcapi.MethodDaemonShutdown, nil, &res); err != nil {
		t.Fatalf("daemon.shutdown: %v", err)
	}
	if !res.Accepted {
		t.Fatalf("shutdown = %+v", res)
	}
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run returned %v after clean shutdown, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run never returned after shutdown request")
	}
}
