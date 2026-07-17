// surface.go implements the surface, input, and replay command families
// (flows 11, 13, 14, 18, 19). Input is lease-gated: acquisition never
// displaces a holder, takeover is deliberate and confirmed (ADR-0004 +
// security confirmation matrix).
package main

import (
	"encoding/base64"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/amux-run/amux/internal/rpcapi"
)

func (a *app) surfaceCmd() *cobra.Command {
	root := &cobra.Command{Use: "surface", Short: "Spawn, select, stop, and restart terminal surfaces"}
	var session, workspace string
	root.PersistentFlags().StringVarP(&session, "session", "s", "", "session id (required)")
	root.PersistentFlags().StringVarP(&workspace, "workspace", "w", "", "workspace id (required)")
	_ = root.MarkPersistentFlagRequired("session")
	_ = root.MarkPersistentFlagRequired("workspace")

	var pane, title, cwd, restartPolicy string
	var cols, rows uint16
	var env []string
	spawn := &cobra.Command{
		Use:   "spawn -- <argv>...",
		Short: "Spawn a terminal surface running argv on a pane (flow 11)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var res rpcapi.SurfaceSpawnResult
			err := a.call(rpcapi.MethodSurfaceSpawn, rpcapi.SurfaceSpawnParams{
				Session: session, Workspace: workspace, Pane: pane,
				Title: title, Argv: args, Cwd: cwd, Cols: cols, Rows: rows,
				Env: env, RestartPolicy: restartPolicy,
			}, &res)
			if err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), res, func(w io.Writer) {
				fmt.Fprintf(w, "surface %s\trev %d\n", res.Surface, res.Rev)
			})
		},
	}
	spawn.Flags().StringVarP(&pane, "pane", "p", "", "pane id (required)")
	spawn.Flags().StringVar(&title, "title", "", "surface title")
	spawn.Flags().StringVar(&cwd, "cwd", "", "working directory")
	spawn.Flags().Uint16Var(&cols, "cols", 0, "initial columns (default 80)")
	spawn.Flags().Uint16Var(&rows, "rows", 0, "initial rows (default 24)")
	spawn.Flags().StringArrayVar(&env, "env", nil, "environment allowlist KEY (repeatable; keys only, never values)")
	spawn.Flags().StringVar(&restartPolicy, "restart-policy", "", "restart policy: manual|automatic")
	_ = spawn.MarkFlagRequired("pane")
	root.AddCommand(spawn)

	var selPane string
	sel := &cobra.Command{
		Use:   "select <surface>",
		Short: "Select a pane's active surface",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var res rpcapi.RevResult
			err := a.call(rpcapi.MethodSurfaceSelect, rpcapi.SurfaceSelectParams{
				Session: session, Workspace: workspace, Pane: selPane, Surface: args[0],
			}, &res)
			if err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), res, func(w io.Writer) { fmt.Fprintf(w, "rev %d\n", res.Rev) })
		},
	}
	sel.Flags().StringVarP(&selPane, "pane", "p", "", "pane id (required)")
	_ = sel.MarkFlagRequired("pane")
	root.AddCommand(sel)

	var stopPane string
	stop := &cobra.Command{
		Use:   "stop <surface>",
		Short: "Stop a surface's process (flow 19; detach never stops)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runSurfaceStop(cmd, session, workspace, stopPane, args[0])
		},
	}
	stop.Flags().StringVarP(&stopPane, "pane", "p", "", "pane id (required)")
	_ = stop.MarkFlagRequired("pane")
	root.AddCommand(stop)

	var restartPane string
	restart := &cobra.Command{
		Use:   "restart <surface>",
		Short: "Relaunch a stopped surface's process (flow 18; a fresh process, classified restarted)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runSurfaceRestart(cmd, session, workspace, restartPane, args[0])
		},
	}
	restart.Flags().StringVarP(&restartPane, "pane", "p", "", "pane id (required)")
	_ = restart.MarkFlagRequired("pane")
	root.AddCommand(restart)

	return root
}

func (a *app) runSurfaceStop(cmd *cobra.Command, session, workspace, pane, surface string) error {
	if err := a.confirm(cmd, fmt.Sprintf("stopping the process of surface %s", surface)); err != nil {
		return err
	}
	var res rpcapi.SurfaceStopResult
	err := a.call(rpcapi.MethodSurfaceStop, rpcapi.SurfaceStopParams{
		Session: session, Workspace: workspace, Pane: pane, Surface: surface, Confirm: true,
	}, &res)
	if err != nil {
		return err
	}
	return a.emit(cmd.OutOrStdout(), res, func(w io.Writer) {
		fmt.Fprintf(w, "class %s\t%s\n", res.Class, res.ExitReason)
	})
}

func (a *app) runSurfaceRestart(cmd *cobra.Command, session, workspace, pane, surface string) error {
	var res rpcapi.SurfaceRestartResult
	err := a.call(rpcapi.MethodSurfaceRestart, rpcapi.SurfaceRestartParams{
		Session: session, Workspace: workspace, Pane: pane, Surface: surface,
	}, &res)
	if err != nil {
		return err
	}
	return a.emit(cmd.OutOrStdout(), res, func(w io.Writer) {
		fmt.Fprintf(w, "class %s\t%s\n", res.Class, res.ExitReason)
	})
}

// stopCmd is the top-level `amux stop` family (surface process stop).
func (a *app) stopCmd() *cobra.Command {
	var session, workspace, pane string
	c := &cobra.Command{
		Use:   "stop <surface>",
		Short: "Stop a surface's process (flow 19)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runSurfaceStop(cmd, session, workspace, pane, args[0])
		},
	}
	c.Flags().StringVarP(&session, "session", "s", "", "session id (required)")
	c.Flags().StringVarP(&workspace, "workspace", "w", "", "workspace id (required)")
	c.Flags().StringVarP(&pane, "pane", "p", "", "pane id (required)")
	_ = c.MarkFlagRequired("session")
	_ = c.MarkFlagRequired("workspace")
	_ = c.MarkFlagRequired("pane")
	return c
}

// restartCmd is the top-level `amux restart` family (surface restart).
func (a *app) restartCmd() *cobra.Command {
	var session, workspace, pane string
	c := &cobra.Command{
		Use:   "restart <surface>",
		Short: "Relaunch a stopped surface's process (flow 18)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runSurfaceRestart(cmd, session, workspace, pane, args[0])
		},
	}
	c.Flags().StringVarP(&session, "session", "s", "", "session id (required)")
	c.Flags().StringVarP(&workspace, "workspace", "w", "", "workspace id (required)")
	c.Flags().StringVarP(&pane, "pane", "p", "", "pane id (required)")
	_ = c.MarkFlagRequired("session")
	_ = c.MarkFlagRequired("workspace")
	_ = c.MarkFlagRequired("pane")
	return c
}

func (a *app) inputCmd() *cobra.Command {
	root := &cobra.Command{Use: "input", Short: "Send lease-gated input to a surface"}
	var session, surface, lease string
	root.PersistentFlags().StringVarP(&session, "session", "s", "", "session id (required)")
	root.PersistentFlags().StringVar(&surface, "surface", "", "surface id (required)")
	root.PersistentFlags().StringVar(&lease, "lease", "", "input lease identity (required)")
	_ = root.MarkPersistentFlagRequired("session")
	_ = root.MarkPersistentFlagRequired("surface")
	_ = root.MarkPersistentFlagRequired("lease")

	var data, dataB64 string
	var fromStdin, takeover bool
	send := &cobra.Command{
		Use:   "send",
		Short: "Send input bytes under the lease (flow 13)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var payload string
			switch {
			case fromStdin:
				b, err := io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return err
				}
				payload = base64.StdEncoding.EncodeToString(b)
			case dataB64 != "":
				payload = dataB64
			default:
				payload = base64.StdEncoding.EncodeToString([]byte(data))
			}
			p := rpcapi.InputSendParams{Session: session, Surface: surface, LeaseID: lease, DataB64: payload}
			if takeover {
				if err := a.confirm(cmd, fmt.Sprintf("taking over the input lease of surface %s", surface)); err != nil {
					return err
				}
				p.Takeover, p.Confirm = true, true
			}
			var res rpcapi.InputSendResult
			if err := a.call(rpcapi.MethodInputSend, p, &res); err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), res, func(w io.Writer) {
				fmt.Fprintf(w, "sent %d bytes\n", res.Bytes)
			})
		},
	}
	send.Flags().StringVar(&data, "data", "", "input text to send")
	send.Flags().StringVar(&dataB64, "data-b64", "", "input bytes, base64-encoded")
	send.Flags().BoolVar(&fromStdin, "stdin", false, "read input bytes from stdin")
	send.Flags().BoolVar(&takeover, "takeover", false, "deliberately take over a held lease (requires confirmation)")
	root.AddCommand(send)

	root.AddCommand(&cobra.Command{
		Use:   "release",
		Short: "Release the held input lease",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := a.call(rpcapi.MethodInputRelease, rpcapi.InputReleaseParams{Session: session, Surface: surface, LeaseID: lease}, nil); err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), ok{OK: true}, func(w io.Writer) {
				fmt.Fprintln(w, "released")
			})
		},
	})
	return root
}

func (a *app) replayCmd() *cobra.Command {
	root := &cobra.Command{Use: "replay", Short: "Read bounded raw output replay"}
	var session, surface string
	var fromSeq uint64
	var maxBytes int64
	read := &cobra.Command{
		Use:   "read",
		Short: "Read retained raw output from a sequence cursor (flow 14)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var res rpcapi.ReplayReadResult
			err := a.call(rpcapi.MethodReplayRead, rpcapi.ReplayReadParams{
				Session: session, Surface: surface, FromSeq: fromSeq, MaxBytes: maxBytes,
			}, &res)
			if err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), res, func(w io.Writer) {
				for _, c := range res.Chunks {
					b, err := base64.StdEncoding.DecodeString(c.DataB64)
					if err != nil {
						continue
					}
					_, _ = w.Write(b)
				}
			})
		},
	}
	read.Flags().StringVarP(&session, "session", "s", "", "session id (required)")
	read.Flags().StringVar(&surface, "surface", "", "surface id (required)")
	read.Flags().Uint64Var(&fromSeq, "from-seq", 1, "replay cursor (first retained sequence = 1)")
	read.Flags().Int64Var(&maxBytes, "max-bytes", 0, "bound on decoded bytes per page (0 = server default; chunks are never split — continue from next_seq)")
	_ = read.MarkFlagRequired("session")
	_ = read.MarkFlagRequired("surface")
	root.AddCommand(read)
	return root
}
