// admin.go implements the snapshot/restore, hook, notification, and
// diagnostics command families (flows 16 and 17 plus the trust and inbox
// surfaces). Trust-granting and destructive transitions enforce the security
// confirmation matrix client-side AND daemon-side (defense in depth: the wire
// token is only sent after an explicit operator confirmation).
package main

import (
	"encoding/base64"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/amux-run/amux/internal/rpcapi"
)

func b64encode(p []byte) string { return base64.StdEncoding.EncodeToString(p) }

func (a *app) snapshotCmd() *cobra.Command {
	root := &cobra.Command{Use: "snapshot", Short: "Save and restore session snapshots"}
	var session string
	root.PersistentFlags().StringVarP(&session, "session", "s", "", "session id (required)")
	_ = root.MarkPersistentFlagRequired("session")

	root.AddCommand(&cobra.Command{
		Use:   "save",
		Short: "Commit an atomic checkpoint generation (flow 16)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var res rpcapi.SnapshotSaveResult
			if err := a.call(rpcapi.MethodSnapshotSave, rpcapi.SnapshotSaveParams{Session: session}, &res); err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), res, func(w io.Writer) {
				fmt.Fprintf(w, "checkpoint %s\tcursor %d\n", res.CheckpointID, res.Cursor)
			})
		},
	})

	root.AddCommand(&cobra.Command{
		Use:   "restore",
		Short: "Restore the latest valid checkpoint (flow 17); surfaces classify live|restarted|stopped, never resurrected",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runRestore(cmd, session)
		},
	})
	return root
}

func (a *app) runRestore(cmd *cobra.Command, session string) error {
	var res rpcapi.SnapshotRestoreResult
	if err := a.call(rpcapi.MethodSnapshotRestore, rpcapi.SnapshotRestoreParams{Session: session}, &res); err != nil {
		return err
	}
	return a.emit(cmd.OutOrStdout(), res, func(w io.Writer) {
		fmt.Fprintf(w, "session %s\tcursor %d\n", res.Session, res.Cursor)
		for _, s := range res.Surfaces {
			fmt.Fprintf(w, "  surface %s\tclass %s\t%s\n", s.Surface, s.Class, s.Reason)
		}
	})
}

// restoreCmd is the top-level `amux restore` family (snapshot restore).
func (a *app) restoreCmd() *cobra.Command {
	var session string
	c := &cobra.Command{
		Use:   "restore",
		Short: "Restore the latest valid checkpoint (flow 17)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runRestore(cmd, session)
		},
	}
	c.Flags().StringVarP(&session, "session", "s", "", "session id (required)")
	_ = c.MarkFlagRequired("session")
	return c
}

func (a *app) hookCmd() *cobra.Command {
	root := &cobra.Command{Use: "hook", Short: "Inspect, approve, deny, and revoke project hook trust"}
	var project string
	root.PersistentFlags().StringVar(&project, "project", "", "project root path (required)")
	_ = root.MarkPersistentFlagRequired("project")

	root.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List a project's hook grants (active and retained history)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var res rpcapi.HookListResult
			if err := a.call(rpcapi.MethodHookList, rpcapi.HookListParams{Project: project}, &res); err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), res, func(w io.Writer) {
				for _, g := range res.Grants {
					fmt.Fprintf(w, "%s\thook %s\tscope %s\tactive %v\tepoch %d\n", g.ID, g.HookID, g.Scope, g.Active, g.BoundEpoch)
				}
			})
		},
	})

	root.AddCommand(&cobra.Command{
		Use:   "approve",
		Short: "Grant project trust (trust-granting: requires confirmation)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := a.confirm(cmd, fmt.Sprintf("approving hook trust for project %s (its hooks become launchable after per-hook grants)", project)); err != nil {
				return err
			}
			var res rpcapi.EpochResult
			if err := a.call(rpcapi.MethodHookApprove, rpcapi.HookApproveParams{Project: project, Confirm: true}, &res); err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), res, func(w io.Writer) {
				fmt.Fprintf(w, "approved at epoch %d\n", res.Epoch)
			})
		},
	})

	root.AddCommand(&cobra.Command{
		Use:   "deny",
		Short: "Record an explicit operator denial for a project",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := a.call(rpcapi.MethodHookDeny, rpcapi.HookDenyParams{Project: project}, nil); err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), ok{OK: true}, func(w io.Writer) {
				fmt.Fprintln(w, "denied")
			})
		},
	})

	root.AddCommand(&cobra.Command{
		Use:   "revoke",
		Short: "Revoke project trust (destructive: cancels queued hook work, deactivates every grant)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := a.confirm(cmd, fmt.Sprintf("revoking hook trust for project %s (queued hook work is cancelled, all grants deactivate)", project)); err != nil {
				return err
			}
			var res rpcapi.EpochResult
			if err := a.call(rpcapi.MethodHookRevoke, rpcapi.HookRevokeParams{Project: project, Confirm: true}, &res); err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), res, func(w io.Writer) {
				fmt.Fprintf(w, "revoked at epoch %d\n", res.Epoch)
			})
		},
	})
	return root
}

func (a *app) notificationCmd() *cobra.Command {
	root := &cobra.Command{
		Use:     "notification",
		Aliases: []string{"notify"},
		Short:   "List and manage the daemon-owned notification inbox",
	}

	var session string
	var unreadOnly bool
	var limit int
	list := &cobra.Command{
		Use:   "list",
		Short: "List notifications, newest first, with the unread count",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var res rpcapi.NotificationListResult
			err := a.call(rpcapi.MethodNotificationList, rpcapi.NotificationListParams{
				Session: session, UnreadOnly: unreadOnly, Limit: limit,
			}, &res)
			if err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), res, func(w io.Writer) {
				fmt.Fprintf(w, "unread %d\n", res.Unread)
				for _, n := range res.Notifications {
					read := " "
					if n.Read {
						read = "r"
					}
					fmt.Fprintf(w, "%s %s\t[%s] %s — %s\n", read, n.ID, n.Kind, n.Title, n.Body)
				}
			})
		},
	}
	list.Flags().StringVarP(&session, "session", "s", "", "session id (required)")
	list.Flags().BoolVar(&unreadOnly, "unread", false, "unread only")
	list.Flags().IntVar(&limit, "limit", 0, "bound the list (0 = server default)")
	_ = list.MarkFlagRequired("session")
	root.AddCommand(list)

	root.AddCommand(&cobra.Command{
		Use:   "read <id>",
		Short: "Mark a notification read",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var res rpcapi.NotificationReadResult
			if err := a.call(rpcapi.MethodNotificationRead, rpcapi.NotificationReadParams{ID: args[0]}, &res); err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), res, func(w io.Writer) {
				fmt.Fprintln(w, "read")
			})
		},
	})
	return root
}

func (a *app) diagnosticsCmd() *cobra.Command {
	root := &cobra.Command{Use: "diagnostics", Short: "Bounded daemon diagnostics"}
	root.AddCommand(&cobra.Command{
		Use:   "dump",
		Short: "Fetch the bounded diagnostic document (one deterministic JSON object)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var res rpcapi.DiagnosticsDumpResult
			if err := a.call(rpcapi.MethodDiagnosticsDump, nil, &res); err != nil {
				return err
			}
			// The dump is already one JSON object — emit it verbatim in both
			// modes (it IS the machine contract).
			_, err := fmt.Fprintln(cmd.OutOrStdout(), string(res.Dump))
			return err
		},
	})
	return root
}
