// tui.go implements `amux tui`: the interactive terminal multiplexer client. It
// runs a real Bubble Tea v2 program (internal/tui/teabridge) over the SAME
// shared protocol client every other command uses — there is no TUI-only
// mutation path. The UI consumes the daemon's read-only projections
// (surface.cells, workspace.tree, pane.context, hook.inspect) and issues daemon
// commands for every mutation; it never parses raw VT output, owns an
// authoritative grid, sequences attach streams, or decides trust/lease
// authority. All terminal I/O (raw mode, input decode, resize, alt-screen,
// bracketed paste) is owned by Bubble Tea; the pure model transitions stay
// deterministic and golden-tested.
package main

import (
	"context"
	"fmt"
	"io"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/amux-run/amux/internal/client"
	"github.com/amux-run/amux/internal/rpcapi"
	"github.com/amux-run/amux/internal/tui/a11y"
	tuiapp "github.com/amux-run/amux/internal/tui/app"
	"github.com/amux-run/amux/internal/tui/bench"
	"github.com/amux-run/amux/internal/tui/keys"
	"github.com/amux-run/amux/internal/tui/render"
	"github.com/amux-run/amux/internal/tui/teabridge"
)

// caller is the subset of the shared client the session/workspace resolver
// needs. The TUI issues the SAME methods as the CLI — no TUI-only path.
type caller interface {
	Call(ctx context.Context, method string, params, result any) error
}

func (a *app) tuiCmd() *cobra.Command {
	var session, workspace string
	var preview, mono bool
	c := &cobra.Command{
		Use:   "tui",
		Short: "Interactive terminal multiplexer client (Bubble Tea v2 UI over the frozen client contracts)",
		Long: "amux tui runs the interactive split-pane client. It renders daemon-\n" +
			"provided projections (cells, split tree, pane context, hook trust) and\n" +
			"issues daemon commands; it never parses raw terminal output or owns\n" +
			"authoritative state. Use --preview to render a static demo frame to\n" +
			"stdout (no daemon, no TTY required) for docs/screenshots.\n\n" +
			"Accessibility: honours NO_COLOR (monochrome focus path), AMUX_REDUCED_MOTION,\n" +
			"and AMUX_ASCII; every interactive action also has a non-interactive `amux`\n" +
			"subcommand (see docs/tui.md).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			profile := resolveProfile(mono)
			if preview {
				return runTUIPreview(cmd.OutOrStdout(), profile)
			}
			return a.runTUIInteractive(cmd, session, workspace, profile)
		},
	}
	c.Flags().StringVarP(&session, "session", "s", "", "session id to attach (default: first session)")
	c.Flags().StringVarP(&workspace, "workspace", "w", "", "workspace id (default: first workspace)")
	c.Flags().BoolVar(&preview, "preview", false, "render a static demo frame to stdout and exit (no daemon)")
	c.Flags().BoolVar(&mono, "mono", false, "force the monochrome/no-color rendering path")
	return c
}

// resolveProfile derives the accessibility profile from env + the --mono flag.
func resolveProfile(mono bool) a11y.Profile {
	return a11y.Resolve(a11y.Env{
		NoColor:       os.Getenv("NO_COLOR") != "" || mono,
		Term:          os.Getenv("TERM"),
		ColorTerm:     os.Getenv("COLORTERM"),
		ReducedMotion: truthy(os.Getenv("AMUX_REDUCED_MOTION")),
		ASCIIOnly:     truthy(os.Getenv("AMUX_ASCII")),
	})
}

func truthy(s string) bool {
	switch s {
	case "", "0", "false", "no":
		return false
	default:
		return true
	}
}

// runTUIPreview renders a deterministic demo scene to stdout. This exercises the
// full render pipeline as a real command (and needs no daemon or TTY), so it is
// usable for documentation and smoke checks.
func runTUIPreview(out io.Writer, profile a11y.Profile) error {
	scene := bench.BuildScene(96, 28, 0)
	sc := render.Render(scene.Rows, scene.Cols, scene.Panes, profile.RenderOptions())
	fmt.Fprintln(out, "amux tui — preview (static demo; not connected to a daemon)")
	fmt.Fprintln(out, sc.PlainString())
	return nil
}

// runTUIInteractive dials the daemon and runs the Bubble Tea program. It
// degrades honestly when the terminal is not interactive or the daemon is
// unreachable. The bridge seeds itself from the four projections, polls them,
// and holds the real attach stream for the focused surface on a dedicated
// connection — there is no bespoke seed path here.
func (a *app) runTUIInteractive(cmd *cobra.Command, session, workspace string, profile a11y.Profile) error {
	if !stdoutIsTTY() {
		fmt.Fprintln(cmd.ErrOrStderr(),
			"amux tui: stdout is not an interactive terminal.\n"+
				"Use `amux tui --preview` for a static frame, or the non-interactive\n"+
				"subcommands (amux pane/surface/attach/notification/hook — see docs/tui.md).")
		return &exitError{code: 2, err: fmt.Errorf("not a tty")}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cli, err := a.dial(ctx)
	if err != nil {
		return err
	}

	cols, rows := terminalSize()
	appModel := tuiapp.New(cols, rows, keys.DefaultKeymap(), profile)
	session, workspace = a.resolveSessionWorkspace(ctx, cli, session, workspace)

	bridge := teabridge.New(teabridge.Config{
		App:       appModel,
		Client:    cli,
		Ctx:       ctx,
		Session:   session,
		Workspace: workspace,
		// The attach stream (flow 12) runs on its OWN connection per session:
		// the shared client multiplexes one stream per connection and the
		// stream reader must never stall unary commands. Closing the connection
		// is the daemon-observed detach (stream end → lease release).
		AttachDial: func(dctx context.Context) (teabridge.AttachConn, error) {
			c, err := a.dialFresh(dctx)
			if err != nil {
				return nil, err
			}
			return attachConn{c}, nil
		},
	})

	prog := tea.NewProgram(bridge,
		tea.WithContext(ctx),
		tea.WithInput(os.Stdin),
		tea.WithOutput(os.Stdout),
	)
	if _, err := prog.Run(); err != nil {
		return &exitError{code: 1, err: err}
	}
	return nil
}

// attachConn adapts the shared *client.Client to the bridge's AttachConn seam
// (the concrete Stream satisfies teabridge.AttachStream). Same client, same
// negotiation — only the connection is dedicated to the attach stream.
type attachConn struct{ *client.Client }

func (c attachConn) Attach(ctx context.Context, p rpcapi.AttachParams) (teabridge.AttachStream, error) {
	return c.Client.Attach(ctx, p)
}

// resolveSessionWorkspace fills in the session/workspace ids from the daemon
// when not supplied, defaulting to the first of each. Missing context is not an
// error — the UI runs and shows the connection banner.
func (a *app) resolveSessionWorkspace(ctx context.Context, cli caller, session, workspace string) (string, string) {
	if session == "" {
		var sl rpcapi.SessionListResult
		if err := cli.Call(ctx, rpcapi.MethodSessionList, nil, &sl); err == nil && len(sl.Sessions) > 0 {
			session = sl.Sessions[0].ID
		}
	}
	if session != "" && workspace == "" {
		var wl rpcapi.WorkspaceListResult
		if err := cli.Call(ctx, rpcapi.MethodWorkspaceList, rpcapi.WorkspaceListParams{Session: session}, &wl); err == nil && len(wl.Workspaces) > 0 {
			workspace = wl.Workspaces[0].ID
		}
	}
	return session, workspace
}
