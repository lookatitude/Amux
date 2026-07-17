// graph.go implements the daemon, session, workspace, and pane command
// families (flows 1–10 and 15). Every mutation goes through the shared
// protocol client; results echo the daemon's revision so scripts can correlate
// with the event stream.
package main

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/amux-run/amux/internal/daemon"
	"github.com/amux-run/amux/internal/rpcapi"
)

func (a *app) daemonCmd() *cobra.Command {
	root := &cobra.Command{Use: "daemon", Short: "Start, stop, and inspect the amuxd daemon"}

	root.AddCommand(&cobra.Command{
		Use:   "start",
		Short: "Run the daemon in the foreground (flow 1); logs go to stderr only",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return daemon.Run(context.Background(), daemon.RunOptions{})
		},
	})
	root.AddCommand(&cobra.Command{
		Use:   "stop",
		Short: "Request a clean daemon shutdown (flow 2; reaps every PTY)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := a.confirm(cmd, "stopping the daemon (all sessions detach, processes are reaped)"); err != nil {
				return err
			}
			var res rpcapi.ShutdownResult
			if err := a.call(rpcapi.MethodDaemonShutdown, nil, &res); err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), res, func(w io.Writer) {
				fmt.Fprintf(w, "shutdown accepted: %v\n", res.Accepted)
			})
		},
	})
	root.AddCommand(&cobra.Command{
		Use:     "health",
		Aliases: []string{"status"},
		Short:   "Report daemon liveness and identity",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var res rpcapi.HealthResult
			if err := a.call(rpcapi.MethodDaemonHealth, nil, &res); err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), res, func(w io.Writer) {
				fmt.Fprintf(w, "boot %s  version %s  protocol %s  sessions %d  uptime %dms\n",
					res.BootID, res.Version, res.Protocol, res.Sessions, res.UptimeMS)
			})
		},
	})
	return root
}

func (a *app) sessionCmd() *cobra.Command {
	root := &cobra.Command{Use: "session", Short: "Create, list, and destroy sessions"}

	var name string
	create := &cobra.Command{
		Use:   "create",
		Short: "Create a session (flow 3)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var res rpcapi.SessionCreateResult
			if err := a.call(rpcapi.MethodSessionCreate, rpcapi.SessionCreateParams{Name: name}, &res); err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), res, func(w io.Writer) {
				fmt.Fprintln(w, res.Session.ID)
			})
		},
	}
	create.Flags().StringVar(&name, "name", "", "human-readable session name")
	root.AddCommand(create)

	root.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List sessions (flow 4)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var res rpcapi.SessionListResult
			if err := a.call(rpcapi.MethodSessionList, nil, &res); err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), res, func(w io.Writer) {
				for _, s := range res.Sessions {
					fmt.Fprintf(w, "%s\t%s\n", s.ID, s.Name)
				}
			})
		},
	})

	root.AddCommand(&cobra.Command{
		Use:   "destroy <session>",
		Short: "Destroy a session and its whole graph",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.confirm(cmd, fmt.Sprintf("destroying session %s (all workspaces, panes, and processes)", args[0])); err != nil {
				return err
			}
			if err := a.call(rpcapi.MethodSessionDestroy, rpcapi.SessionDestroyParams{Session: args[0]}, nil); err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), ok{OK: true}, func(w io.Writer) {
				fmt.Fprintln(w, "destroyed", args[0])
			})
		},
	})
	return root
}

func (a *app) workspaceCmd() *cobra.Command {
	root := &cobra.Command{Use: "workspace", Short: "Create, list, rename, and destroy workspaces"}
	var session string
	root.PersistentFlags().StringVarP(&session, "session", "s", "", "session id (required)")
	_ = root.MarkPersistentFlagRequired("session")

	var name, primaryRoot, firstCwd string
	create := &cobra.Command{
		Use:   "create",
		Short: "Create a workspace with its first pane and surface (flow 5)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var res rpcapi.WorkspaceCreateResult
			err := a.call(rpcapi.MethodWorkspaceCreate, rpcapi.WorkspaceCreateParams{
				Session: session, Name: name, PrimaryRoot: primaryRoot, FirstPaneCwd: firstCwd,
			}, &res)
			if err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), res, func(w io.Writer) {
				fmt.Fprintf(w, "workspace %s\tpane %s\tsurface %s\trev %d\n", res.Workspace, res.FirstPane, res.FirstSurface, res.Rev)
			})
		},
	}
	create.Flags().StringVar(&name, "name", "", "workspace name")
	create.Flags().StringVar(&primaryRoot, "root", "", "primary project root")
	create.Flags().StringVar(&firstCwd, "cwd", "", "first pane working directory")
	root.AddCommand(create)

	root.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List a session's workspaces (flow 6)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var res rpcapi.WorkspaceListResult
			if err := a.call(rpcapi.MethodWorkspaceList, rpcapi.WorkspaceListParams{Session: session}, &res); err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), res, func(w io.Writer) {
				for _, ws := range res.Workspaces {
					fmt.Fprintf(w, "%s\t%s\tpanes %d\tfocused %s\trev %d\n", ws.ID, ws.Name, ws.PaneCount, ws.Focused, ws.Rev)
				}
			})
		},
	})

	root.AddCommand(&cobra.Command{
		Use:   "rename <workspace> <name>",
		Short: "Rename a workspace",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			var res rpcapi.RevResult
			err := a.call(rpcapi.MethodWorkspaceRename, rpcapi.WorkspaceRenameParams{
				Session: session, Workspace: args[0], Name: args[1],
			}, &res)
			if err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), res, func(w io.Writer) {
				fmt.Fprintf(w, "rev %d\n", res.Rev)
			})
		},
	})

	root.AddCommand(&cobra.Command{
		Use:   "destroy <workspace>",
		Short: "Destroy a workspace and its subtree",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.confirm(cmd, fmt.Sprintf("destroying workspace %s (its panes and processes)", args[0])); err != nil {
				return err
			}
			var res rpcapi.RevResult
			err := a.call(rpcapi.MethodWorkspaceDestroy, rpcapi.WorkspaceDestroyParams{Session: session, Workspace: args[0]}, &res)
			if err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), res, func(w io.Writer) {
				fmt.Fprintf(w, "destroyed %s at rev %d\n", args[0], res.Rev)
			})
		},
	})
	return root
}

func (a *app) paneCmd() *cobra.Command {
	root := &cobra.Command{Use: "pane", Short: "Split, focus, resize, close, and inspect panes"}
	var session, workspace string
	root.PersistentFlags().StringVarP(&session, "session", "s", "", "session id (required)")
	root.PersistentFlags().StringVarP(&workspace, "workspace", "w", "", "workspace id (required)")
	_ = root.MarkPersistentFlagRequired("session")
	_ = root.MarkPersistentFlagRequired("workspace")

	var orientation string
	var ratio float64
	var newCwd string
	split := &cobra.Command{
		Use:   "split <pane>",
		Short: "Split a pane horizontally or vertically (flows 7 and 8)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var res rpcapi.PaneSplitResult
			err := a.call(rpcapi.MethodPaneSplit, rpcapi.PaneSplitParams{
				Session: session, Workspace: workspace, Target: args[0],
				Orientation: rpcapi.Orientation(orientation), Ratio: ratio, NewPaneCwd: newCwd,
			}, &res)
			if err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), res, func(w io.Writer) {
				fmt.Fprintf(w, "pane %s\tsurface %s\tratio %.3f\trev %d\n", res.NewPane, res.NewSurface, res.Ratio, res.Rev)
			})
		},
	}
	split.Flags().StringVarP(&orientation, "orientation", "o", "horizontal", "split orientation: horizontal|vertical")
	split.Flags().Float64Var(&ratio, "ratio", 0, "split ratio for the existing pane (0 = even)")
	split.Flags().StringVar(&newCwd, "cwd", "", "new pane working directory")
	root.AddCommand(split)

	root.AddCommand(&cobra.Command{
		Use:   "focus <pane>",
		Short: "Focus a pane (flow 9)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var res rpcapi.RevResult
			err := a.call(rpcapi.MethodPaneFocus, rpcapi.PaneFocusParams{Session: session, Workspace: workspace, Pane: args[0]}, &res)
			if err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), res, func(w io.Writer) { fmt.Fprintf(w, "rev %d\n", res.Rev) })
		},
	})

	var resizeRatio float64
	resize := &cobra.Command{
		Use:   "resize <pane>",
		Short: "Resize a pane's parent split (flow 10)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var res rpcapi.RevResult
			err := a.call(rpcapi.MethodPaneResize, rpcapi.PaneResizeParams{
				Session: session, Workspace: workspace, Pane: args[0], Ratio: resizeRatio,
			}, &res)
			if err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), res, func(w io.Writer) { fmt.Fprintf(w, "rev %d\n", res.Rev) })
		},
	}
	resize.Flags().Float64Var(&resizeRatio, "ratio", 0.5, "new ratio of this pane within its parent split")
	root.AddCommand(resize)

	root.AddCommand(&cobra.Command{
		Use:   "close <pane>",
		Short: "Close a pane (destroys its surfaces)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.confirm(cmd, fmt.Sprintf("closing pane %s (its surfaces and processes)", args[0])); err != nil {
				return err
			}
			var res rpcapi.RevResult
			err := a.call(rpcapi.MethodPaneClose, rpcapi.PaneCloseParams{Session: session, Workspace: workspace, Pane: args[0]}, &res)
			if err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), res, func(w io.Writer) { fmt.Fprintf(w, "closed %s at rev %d\n", args[0], res.Rev) })
		},
	})

	root.AddCommand(a.paneInspectSub())
	return root
}

// paneInspectSub is `amux pane inspect`; the top-level `amux inspect` wraps
// the same call with explicit flags.
func (a *app) paneInspectSub() *cobra.Command {
	return &cobra.Command{
		Use:   "inspect <pane>",
		Short: "Inspect a pane's observable state (flow 15)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			session, _ := cmd.Flags().GetString("session")
			workspace, _ := cmd.Flags().GetString("workspace")
			return a.runInspect(cmd, session, workspace, args[0])
		},
	}
}

func (a *app) runInspect(cmd *cobra.Command, session, workspace, pane string) error {
	var res rpcapi.PaneInspectResult
	err := a.call(rpcapi.MethodPaneInspect, rpcapi.PaneInspectParams{Session: session, Workspace: workspace, Pane: pane}, &res)
	if err != nil {
		return err
	}
	return a.emit(cmd.OutOrStdout(), res, func(w io.Writer) {
		fmt.Fprintf(w, "pane %s\tcwd %s\tfocused %v\tactive %s\tlatest_seq %d\n", res.Pane, res.Cwd, res.Focused, res.Active, res.LatestSeq)
		for _, s := range res.Surfaces {
			fmt.Fprintf(w, "  surface %s\ttitle %q\tactive %v\tclass %s\t%s\n", s.ID, s.Title, s.Active, s.Class, s.ExitReason)
		}
	})
}

// inspectCmd is the top-level `amux inspect` family.
func (a *app) inspectCmd() *cobra.Command {
	var session, workspace string
	c := &cobra.Command{
		Use:   "inspect <pane>",
		Short: "Inspect a pane's observable state (flow 15)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runInspect(cmd, session, workspace, args[0])
		},
	}
	c.Flags().StringVarP(&session, "session", "s", "", "session id (required)")
	c.Flags().StringVarP(&workspace, "workspace", "w", "", "workspace id (required)")
	_ = c.MarkFlagRequired("session")
	_ = c.MarkFlagRequired("workspace")
	return c
}
