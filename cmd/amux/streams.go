// streams.go implements the attach and event streaming families (flows 12 and
// 20) over the shared client's single-stream discipline. Attach writes raw
// surface output to stdout (replay first, live strictly after the cutover
// sequence); event subscribe emits one JSON object per line.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	v1 "github.com/amux-run/amux/api/v1"

	"github.com/amux-run/amux/internal/client"
	"github.com/amux-run/amux/internal/rpcapi"
)

// streamCtx bounds a stream by --timeout only when explicitly set; streams
// default to unbounded (they end when the server ends them or the flow's
// --max-* bound is reached).
func (a *app) streamCtx(cmd *cobra.Command) (context.Context, context.CancelFunc) {
	if cmd.Flags().Changed("timeout") && a.timeout > 0 {
		return context.WithTimeout(context.Background(), a.timeout)
	}
	return context.WithCancel(context.Background())
}

func (a *app) attachCmd() *cobra.Command {
	var session string
	var fromSeq, maxFrames uint64
	c := &cobra.Command{
		Use:   "attach <surface>",
		Short: "Attach to a surface's output: snapshot, ordered replay, then live (flow 12)",
		Long: "Attach opens the linearized attach stream: one attach_snapshot header\n" +
			"(pane metadata + cutover sequence, plus a typed replay_gap boundary when\n" +
			"the requested cursor was evicted), replayed raw output ending exactly at\n" +
			"the cutover, then live output strictly after it. Raw bytes go to stdout;\n" +
			"with --json every frame is one JSON line with base64 data. Detaching\n" +
			"never stops the process.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.streamCtx(cmd)
			defer cancel()
			cli, err := a.dial(ctx)
			if err != nil {
				return err
			}
			st, err := cli.Stream(ctx, rpcapi.MethodAttach, rpcapi.AttachParams{
				Session: session, Surface: args[0], FromSeq: fromSeq,
			})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			var frames uint64
			for {
				ev, body, err := st.Recv()
				if err == io.EOF {
					return nil
				}
				if err != nil {
					return err
				}
				switch ev.Event {
				case "heartbeat":
					continue
				case "attach_snapshot":
					if a.jsonOut {
						if jerr := writeEventLine(out, ev, nil); jerr != nil {
							return jerr
						}
					}
					continue
				}
				if a.jsonOut {
					if jerr := writeEventLine(out, ev, body); jerr != nil {
						return jerr
					}
				} else if len(body) > 0 {
					if _, werr := out.Write(body); werr != nil {
						return werr
					}
				}
				frames++
				if maxFrames > 0 && frames >= maxFrames {
					return nil
				}
			}
		},
	}
	c.Flags().StringVarP(&session, "session", "s", "", "session id (required)")
	c.Flags().Uint64Var(&fromSeq, "from-seq", 1, "replay cursor (0 = live only from the cutover)")
	c.Flags().Uint64Var(&maxFrames, "max-frames", 0, "detach after N output frames (0 = stay attached)")
	_ = c.MarkFlagRequired("session")
	return c
}

func (a *app) eventCmd() *cobra.Command {
	root := &cobra.Command{Use: "event", Short: "Subscribe to the daemon event stream"}
	var session, workspace string
	var fromSeq, maxEvents uint64
	sub := &cobra.Command{
		Use:   "subscribe",
		Short: "Stream committed session events as JSON lines (flow 20)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := a.streamCtx(cmd)
			defer cancel()
			cli, err := a.dial(ctx)
			if err != nil {
				return err
			}
			st, err := cli.Stream(ctx, rpcapi.MethodEventSubscribe, rpcapi.EventSubscribeParams{
				Session: session, Workspace: workspace, FromSeq: fromSeq,
			})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			var n uint64
			for {
				ev, _, err := st.Recv()
				if err == io.EOF {
					return nil
				}
				if err != nil {
					if client.CodeOf(err) == v1.ErrEventGap {
						return fmt.Errorf("event gap: re-snapshot and re-subscribe: %w", err)
					}
					return err
				}
				if ev.Event == "heartbeat" {
					continue
				}
				if jerr := writeEventLine(out, ev, nil); jerr != nil {
					return jerr
				}
				n++
				if maxEvents > 0 && n >= maxEvents {
					return nil
				}
			}
		},
	}
	sub.Flags().StringVarP(&session, "session", "s", "", "session id (required)")
	sub.Flags().StringVarP(&workspace, "workspace", "w", "", "filter to one workspace")
	sub.Flags().Uint64Var(&fromSeq, "from-seq", 0, "replay committed events from this sequence (0 = live only)")
	sub.Flags().Uint64Var(&maxEvents, "max-events", 0, "exit after N events (0 = stream forever)")
	_ = sub.MarkFlagRequired("session")
	root.AddCommand(sub)
	return root
}

// eventLine is the stable per-event JSON schema both streaming commands emit.
type eventLine struct {
	Event   string          `json:"event"`
	Session string          `json:"session,omitempty"`
	Seq     uint64          `json:"seq"`
	TimeMS  int64           `json:"time_ms,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
	DataB64 string          `json:"data_b64,omitempty"`
}

func writeEventLine(w io.Writer, ev v1.Event, body []byte) error {
	line := eventLine{Event: ev.Event, Session: ev.Session, Seq: ev.Seq, TimeMS: ev.TimeMS, Payload: ev.Payload}
	if len(body) > 0 {
		line.DataB64 = b64encode(body)
	}
	return json.NewEncoder(w).Encode(line)
}
