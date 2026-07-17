// Command amux is the Amux CLI: every command family speaks to the amuxd
// daemon through the shared protocol client (internal/client) — there is no
// CLI-only durable mutation path (ADR-0001; spec/PRD F1). Machine consumers
// use --json for stable output schemas (the rpcapi result types verbatim) and
// the deterministic exit-code table below; humans get terse text.
//
// Exit codes (stable contract, mirrors the frozen ADR-0003 error taxonomy):
//
//	0  success
//	1  internal error or unclassified failure
//	2  usage error, invalid argument, or a missing destructive-op confirmation
//	3  not_found
//	4  conflict (includes an input lease held by another client)
//	5  authorization: project_trust_required, hook_grant_required,
//	   hook_grant_stale, scope_denied, not_input_lease_holder
//	6  resource_exhausted (includes slow-consumer disconnects)
//	7  event_gap or replay_gap (re-snapshot and resume)
//	8  unsupported_version (protocol major mismatch)
//	9  daemon unreachable (dial/connection failure)
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	v1 "github.com/amux-run/amux/api/v1"

	"github.com/amux-run/amux/internal/client"
	"github.com/amux-run/amux/internal/config"
	"github.com/amux-run/amux/internal/platform"
	"github.com/amux-run/amux/internal/transport/local"
	"github.com/amux-run/amux/internal/version"
)

func main() {
	a := &app{}
	root := a.rootCmd()
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "amux:", err)
		os.Exit(exitCode(err))
	}
}

// app carries the global flags and the lazily dialed shared client.
type app struct {
	socket  string
	jsonOut bool
	yes     bool
	timeout time.Duration

	cli *client.Client
}

// exitError pins a specific exit code onto an error.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }

// errConfirmationRequired is the fail-closed refusal for a destructive op
// without --yes on a non-interactive stdin (security confirmation matrix).
func errConfirmationRequired(action string) error {
	return &exitError{code: 2, err: fmt.Errorf("%s requires confirmation: re-run with --yes, or run interactively and answer the prompt", action)}
}

// exitCode maps err onto the stable exit-code table.
func exitCode(err error) int {
	var ee *exitError
	if errors.As(err, &ee) {
		return ee.code
	}
	switch client.CodeOf(err) {
	case v1.ErrInvalidArgument:
		return 2
	case v1.ErrNotFound:
		return 3
	case v1.ErrConflict:
		return 4
	case v1.ErrProjectTrustRequired, v1.ErrHookGrantRequired, v1.ErrHookGrantStale,
		v1.ErrScopeDenied, v1.ErrNotInputLeaseHolder:
		return 5
	case v1.ErrResourceExhausted:
		return 6
	case v1.ErrEventGap, v1.ErrReplayGap:
		return 7
	case v1.ErrUnsupportedVersion:
		return 8
	case v1.ErrInternal:
		return 1
	}
	return 1
}

// socketSpec resolves the daemon socket path: the --socket override, else the
// XDG runtime path (config.Resolve — the same resolution amuxd itself uses).
func (a *app) socketSpec() (string, error) {
	if a.socket != "" {
		return a.socket, nil
	}
	paths, err := config.Resolve(os.Getenv)
	if err != nil {
		return "", &exitError{code: 2, err: err}
	}
	return paths.SocketPath(), nil
}

// dial connects the shared client (once per invocation).
func (a *app) dial(ctx context.Context) (*client.Client, error) {
	if a.cli != nil {
		return a.cli, nil
	}
	cli, err := a.dialFresh(ctx)
	if err != nil {
		return nil, err
	}
	a.cli = cli
	return cli, nil
}

// dialFresh always opens a NEW connection (same negotiation and error typing
// as dial, no caching). The TUI uses it for the dedicated attach stream
// connection: the shared client multiplexes one stream per connection, and the
// attach stream must never contend with the unary command path.
func (a *app) dialFresh(ctx context.Context) (*client.Client, error) {
	sock, err := a.socketSpec()
	if err != nil {
		return nil, err
	}
	spec := platform.TransportSpec{SocketPath: sock, OwnerUID: uint32(os.Getuid())}
	cli, err := client.Dial(ctx, local.New(), spec, "amux-cli/"+version.Version)
	if err != nil {
		if client.CodeOf(err) == v1.ErrUnsupportedVersion {
			return nil, err
		}
		return nil, &exitError{code: 9, err: fmt.Errorf("cannot reach amuxd at %s (is the daemon running?): %w", sock, err)}
	}
	return cli, nil
}

// callCtx returns the per-call context honoring --timeout.
func (a *app) callCtx() (context.Context, context.CancelFunc) {
	if a.timeout > 0 {
		return context.WithTimeout(context.Background(), a.timeout)
	}
	return context.Background(), func() {}
}

// call dials (if needed) and performs one unary method call.
func (a *app) call(method string, params, result any) error {
	ctx, cancel := a.callCtx()
	defer cancel()
	cli, err := a.dial(ctx)
	if err != nil {
		return err
	}
	return cli.Call(ctx, method, params, result)
}

// emit writes the command result: --json prints the stable JSON schema (the
// rpcapi result verbatim); otherwise human writes terse text.
func (a *app) emit(out io.Writer, result any, human func(io.Writer)) error {
	if a.jsonOut {
		return json.NewEncoder(out).Encode(result)
	}
	human(out)
	return nil
}

// ok is the --json body for mutations whose wire result is empty.
type ok struct {
	OK bool `json:"ok"`
}

// confirm enforces the security confirmation matrix (destroy, stop, lease
// takeover, hook approval, trust revocation): --yes confirms explicitly; an
// interactive stdin prompts; anything else fails closed.
func (a *app) confirm(cmd *cobra.Command, action string) error {
	if a.yes {
		return nil
	}
	stdin, isFile := cmd.InOrStdin().(*os.File)
	if !isFile {
		return errConfirmationRequired(action)
	}
	fi, err := stdin.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return errConfirmationRequired(action)
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "%s — proceed? [y/N] ", action)
	var line string
	if _, err := fmt.Fscanln(stdin, &line); err != nil {
		return errConfirmationRequired(action)
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return nil
	}
	return errConfirmationRequired(action)
}

func (a *app) rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "amux",
		Short: "Amux — a Go-authoritative, Linux-first terminal workspace runtime",
		// Version backs the root --version flag (frozen install-smoke and
		// rollback-runbook contract): the same stamped identity string the
		// `version` subcommand and `amuxd --version` print, emitted without
		// dialing the daemon. Machine consumers use `amux version --json`.
		Version:       version.String(),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetVersionTemplate("{{.Version}}\n")
	pf := root.PersistentFlags()
	pf.StringVar(&a.socket, "socket", "", "daemon control socket path (default: $XDG_RUNTIME_DIR/amux/amuxd.sock)")
	pf.BoolVar(&a.jsonOut, "json", false, "emit stable machine-readable JSON")
	pf.BoolVar(&a.yes, "yes", false, "confirm destructive operations non-interactively")
	pf.DurationVar(&a.timeout, "timeout", 10*time.Second, "per-request timeout (0 disables; streams are unbounded unless set)")
	root.PersistentPostRun = func(*cobra.Command, []string) {
		if a.cli != nil {
			a.cli.Close()
		}
	}

	root.AddCommand(
		a.versionCmd(),
		a.daemonCmd(),
		a.sessionCmd(),
		a.workspaceCmd(),
		a.paneCmd(),
		a.surfaceCmd(),
		a.attachCmd(),
		a.inputCmd(),
		a.replayCmd(),
		a.snapshotCmd(),
		a.eventCmd(),
		a.hookCmd(),
		a.notificationCmd(),
		a.diagnosticsCmd(),
		// Top-level verb families from the frozen contract; each dispatches to
		// the same implementation as its namespaced form.
		a.inspectCmd(),
		a.restoreCmd(),
		a.restartCmd(),
		a.stopCmd(),
		a.tuiCmd(),
	)
	return root
}

func (a *app) versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			type v struct {
				Version  string `json:"version"`
				Protocol string `json:"protocol"`
			}
			return a.emit(cmd.OutOrStdout(), v{Version: version.Version, Protocol: version.Protocol}, func(w io.Writer) {
				fmt.Fprintln(w, version.String())
			})
		},
	}
}
