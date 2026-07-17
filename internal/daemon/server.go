// server.go wires the transport-independent Engine onto the local protocol:
// it registers one handler per rpcapi method on an internal/protocol.Server,
// decoding params strictly (v1.DecodeStrict — an unknown field is a contract
// violation, ADR-0003), calling the engine, and mapping typed errors onto the
// wire taxonomy. The CLI and TUI reach the daemon ONLY through these methods —
// there is no second mutation path (ADR-0001).
package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	v1 "github.com/amux-run/amux/api/v1"

	"github.com/amux-run/amux/internal/attach"
	"github.com/amux-run/amux/internal/control"
	"github.com/amux-run/amux/internal/notify"
	"github.com/amux-run/amux/internal/observability"
	"github.com/amux-run/amux/internal/platform"
	"github.com/amux-run/amux/internal/protocol"
	"github.com/amux-run/amux/internal/rpcapi"
	"github.com/amux-run/amux/internal/store"
	"github.com/amux-run/amux/internal/version"
)

// shutdownGrace is how long the daemon.shutdown handler waits before firing
// OnShutdown, so the acknowledging response reaches the client first.
const shutdownGrace = 100 * time.Millisecond

// ServerConfig assembles one protocol server over the daemon runtime.
type ServerConfig struct {
	Engine  *Engine
	Control *control.Actor
	// Store backs hook-grant listing; nil disables hook.list.
	Store *store.Store
	// Notify backs the notification family; nil disables it.
	Notify *notify.Service
	// Metrics contributes the diagnostics.dump metrics section (optional).
	Metrics *observability.Registry
	// BootID identifies this daemon incarnation (required).
	BootID string
	// Peers is the mandatory SO_PEERCRED seam (STR-2). Production wires
	// platform.NewLinuxPeerCredentials; tests inject a fake.
	Peers platform.PeerCredentials
	Clock platform.Clock
	Log   *slog.Logger
	// OnShutdown is invoked (once, asynchronously) when a client requests a
	// clean daemon shutdown.
	OnShutdown func()
	// HeartbeatMS overrides the stream heartbeat cadence (<=0 = default).
	HeartbeatMS int64
}

// NewServer builds the protocol server with every rpcapi method registered.
func NewServer(cfg ServerConfig) (*protocol.Server, error) {
	if cfg.Engine == nil {
		return nil, fmt.Errorf("daemon: ServerConfig.Engine is required")
	}
	if cfg.Clock == nil {
		cfg.Clock = platform.NewSystemClock()
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	srv := protocol.NewServer(protocol.Options{
		BootID:      cfg.BootID,
		ServerTag:   "amuxd/" + version.Version,
		Clock:       cfg.Clock,
		Peers:       cfg.Peers,
		Logger:      cfg.Log,
		HeartbeatMS: cfg.HeartbeatMS,
	})
	d := &dispatcher{cfg: cfg, startedMS: cfg.Clock.NowUnixMilli()}
	d.register(srv)
	return srv, nil
}

// dispatcher holds the per-server state the handlers close over.
type dispatcher struct {
	cfg       ServerConfig
	startedMS int64
}

// unary adapts a typed engine call to a protocol.HandlerFunc with strict
// param decoding and taxonomy error mapping.
func unary[P, R any](fn func(ctx context.Context, p P) (R, error)) protocol.HandlerFunc {
	return func(ctx context.Context, req protocol.Request) (json.RawMessage, *v1.ErrorBody) {
		var p P
		if len(req.Params) > 0 && !bytes.Equal(req.Params, []byte("null")) {
			if err := v1.DecodeStrict(req.Params, &p); err != nil {
				return nil, &v1.ErrorBody{Code: v1.ErrInvalidArgument, Message: err.Error()}
			}
		}
		res, err := fn(ctx, p)
		if err != nil {
			return nil, ErrorBody(err)
		}
		raw, merr := json.Marshal(res)
		if merr != nil {
			return nil, &v1.ErrorBody{Code: v1.ErrInternal, Message: merr.Error()}
		}
		return raw, nil
	}
}

// noParams is the empty strict-decoded params shape for parameterless methods.
type noParams struct{}

func (d *dispatcher) register(srv *protocol.Server) {
	e := d.cfg.Engine

	srv.HandleFunc(rpcapi.MethodDaemonHealth, unary(func(ctx context.Context, _ noParams) (rpcapi.HealthResult, error) {
		now := d.cfg.Clock.NowUnixMilli()
		return rpcapi.HealthResult{
			BootID:        d.cfg.BootID,
			Version:       version.Version,
			Protocol:      version.Protocol,
			Sessions:      e.SessionCount(),
			UptimeMS:      now - d.startedMS,
			StartedUnixMS: d.startedMS,
		}, nil
	}))
	srv.HandleFunc(rpcapi.MethodDaemonShutdown, unary(func(ctx context.Context, _ noParams) (rpcapi.ShutdownResult, error) {
		if d.cfg.OnShutdown != nil {
			time.AfterFunc(shutdownGrace, d.cfg.OnShutdown)
		}
		return rpcapi.ShutdownResult{Accepted: d.cfg.OnShutdown != nil}, nil
	}))

	srv.HandleFunc(rpcapi.MethodSessionCreate, unary(func(ctx context.Context, p rpcapi.SessionCreateParams) (rpcapi.SessionCreateResult, error) {
		info, err := e.CreateSession(ctx, p.Name)
		if err != nil {
			return rpcapi.SessionCreateResult{}, err
		}
		return rpcapi.SessionCreateResult{Session: rpcapi.SessionInfo{ID: info.ID, Name: info.Name, CreatedMS: info.CreatedMS}}, nil
	}))
	srv.HandleFunc(rpcapi.MethodSessionList, unary(func(ctx context.Context, _ noParams) (rpcapi.SessionListResult, error) {
		infos, err := e.ListSessions(ctx)
		if err != nil {
			return rpcapi.SessionListResult{}, err
		}
		out := rpcapi.SessionListResult{Sessions: []rpcapi.SessionInfo{}}
		for _, s := range infos {
			out.Sessions = append(out.Sessions, rpcapi.SessionInfo{ID: s.ID, Name: s.Name, CreatedMS: s.CreatedMS})
		}
		return out, nil
	}))
	srv.HandleFunc(rpcapi.MethodSessionDestroy, unary(func(ctx context.Context, p rpcapi.SessionDestroyParams) (struct{}, error) {
		return struct{}{}, e.DestroySession(ctx, sid(p.Session))
	}))

	srv.HandleFunc(rpcapi.MethodWorkspaceCreate, unary(e.CreateWorkspace))
	srv.HandleFunc(rpcapi.MethodWorkspaceList, unary(e.ListWorkspaces))
	srv.HandleFunc(rpcapi.MethodWorkspaceRename, unary(e.RenameWorkspace))
	srv.HandleFunc(rpcapi.MethodWorkspaceDestroy, unary(e.DestroyWorkspace))

	srv.HandleFunc(rpcapi.MethodPaneSplit, unary(e.SplitPane))
	srv.HandleFunc(rpcapi.MethodPaneFocus, unary(e.FocusPane))
	srv.HandleFunc(rpcapi.MethodPaneResize, unary(e.ResizePane))
	srv.HandleFunc(rpcapi.MethodPaneClose, unary(e.ClosePane))
	srv.HandleFunc(rpcapi.MethodPaneInspect, unary(e.InspectPane))

	srv.HandleFunc(rpcapi.MethodSurfaceSpawn, unary(e.SpawnSurface))
	srv.HandleFunc(rpcapi.MethodSurfaceSelect, unary(e.SelectSurface))
	srv.HandleFunc(rpcapi.MethodSurfaceStop, unary(e.StopSurface))
	srv.HandleFunc(rpcapi.MethodSurfaceRestart, unary(e.RestartSurface))

	srv.HandleFunc(rpcapi.MethodInputSend, unary(e.SendInput))
	srv.HandleFunc(rpcapi.MethodInputRelease, unary(e.ReleaseInput))
	srv.HandleFunc(rpcapi.MethodReplayRead, unary(e.ReplayRead))

	// Minor-1 read-only projections (T4 handoff to T5). Additive: no minor-0
	// payload changed; these methods only project existing authority.
	srv.HandleFunc(rpcapi.MethodSurfaceCells, unary(e.SurfaceCells))
	srv.HandleFunc(rpcapi.MethodWorkspaceTree, unary(e.WorkspaceTree))
	srv.HandleFunc(rpcapi.MethodPaneContext, unary(e.PaneContext))

	srv.HandleFunc(rpcapi.MethodSnapshotSave, unary(e.SaveSnapshot))
	srv.HandleFunc(rpcapi.MethodSnapshotRestore, unary(e.RestoreSnapshot))

	d.registerHooks(srv)
	d.registerNotifications(srv)
	d.registerDiagnostics(srv)

	srv.StreamFunc(rpcapi.MethodAttach, d.attachStream)
	srv.StreamFunc(rpcapi.MethodEventSubscribe, d.eventStream)
}

// registerHooks wires the project-trust command family onto the control actor
// (the single linearization point for trust, ADR-0004). Trust-granting and
// destructive transitions fail closed without the confirmation token (spec
// confirmation matrix).
func (d *dispatcher) registerHooks(srv *protocol.Server) {
	ctrl := d.cfg.Control
	if ctrl == nil {
		return
	}
	srv.HandleFunc(rpcapi.MethodHookList, unary(func(ctx context.Context, p rpcapi.HookListParams) (rpcapi.HookListResult, error) {
		if d.cfg.Store == nil {
			return rpcapi.HookListResult{}, &engineError{code: v1.ErrInternal, msg: "hook store not configured"}
		}
		key, err := ctrl.RegisterProject(ctx, p.Project)
		if err != nil {
			return rpcapi.HookListResult{}, &engineError{code: v1.ErrInvalidArgument, msg: err.Error()}
		}
		rows, err := d.cfg.Store.ListGrants(string(key), true)
		if err != nil {
			return rpcapi.HookListResult{}, &engineError{code: v1.ErrInternal, msg: err.Error()}
		}
		out := rpcapi.HookListResult{Grants: []rpcapi.HookGrantInfo{}}
		for _, g := range rows {
			var events []string
			_ = json.Unmarshal([]byte(g.EventsJSON), &events)
			out.Grants = append(out.Grants, rpcapi.HookGrantInfo{
				ID: g.ID, HookID: g.HookID, Events: events,
				Scope: g.ScopeKind, Active: g.Active, BoundEpoch: g.BoundEpoch,
			})
		}
		return out, nil
	}))
	// hook.inspect (minor 1) is the read-only trust-detail projection: the
	// FULL frozen presentation fields (executable identity + digests, events,
	// cwd scope, env KEY allowlist, timeout, output cap, epoch/status) so a
	// client can render the confirmation matrix verbatim. It decides nothing:
	// authorization stays in the control actor, env values never cross the
	// wire, and a missing store fails closed with a typed error.
	srv.HandleFunc(rpcapi.MethodHookInspect, unary(func(ctx context.Context, p rpcapi.HookInspectParams) (rpcapi.HookInspectResult, error) {
		if d.cfg.Store == nil {
			return rpcapi.HookInspectResult{}, &engineError{code: v1.ErrInternal, msg: "hook store not configured"}
		}
		key, err := ctrl.RegisterProject(ctx, p.Project)
		if err != nil {
			return rpcapi.HookInspectResult{}, &engineError{code: v1.ErrInvalidArgument, msg: err.Error()}
		}
		rec, ok, err := ctrl.Project(ctx, key)
		if err != nil {
			return rpcapi.HookInspectResult{}, &engineError{code: v1.ErrInternal, msg: err.Error()}
		}
		if !ok {
			return rpcapi.HookInspectResult{}, &engineError{code: v1.ErrNotFound, msg: "project not registered"}
		}
		rows, err := d.cfg.Store.ListGrants(string(key), true)
		if err != nil {
			return rpcapi.HookInspectResult{}, &engineError{code: v1.ErrInternal, msg: err.Error()}
		}
		out := rpcapi.HookInspectResult{
			Project: rpcapi.HookProjectTrust{
				Key:   string(rec.Key),
				Root:  rec.Root,
				State: string(rec.State),
				Epoch: rec.Epoch,
			},
			Grants: []rpcapi.HookGrantDetail{},
		}
		for _, g := range rows {
			var events, envKeys []string
			_ = json.Unmarshal([]byte(g.EventsJSON), &events)
			_ = json.Unmarshal([]byte(g.EnvAllowlistJSON), &envKeys)
			out.Grants = append(out.Grants, rpcapi.HookGrantDetail{
				ID:           g.ID,
				HookID:       g.HookID,
				ExecPath:     g.ExecPath,
				ExecSHA256:   g.ExecSHA256,
				ConfigSHA256: g.ConfigSHA256,
				Events:       events,
				Scope:        g.ScopeKind,
				FixedPath:    g.FixedPath,
				EnvKeys:      envKeys,
				TimeoutMS:    g.TimeoutMS,
				OutputCap:    g.OutputCapBytes,
				BoundEpoch:   g.BoundEpoch,
				Active:       g.Active,
			})
		}
		return out, nil
	}))
	srv.HandleFunc(rpcapi.MethodHookApprove, unary(func(ctx context.Context, p rpcapi.HookApproveParams) (rpcapi.EpochResult, error) {
		if !p.Confirm {
			return rpcapi.EpochResult{}, &engineError{code: v1.ErrInvalidArgument, msg: "approving project trust requires confirmation"}
		}
		key, err := ctrl.RegisterProject(ctx, p.Project)
		if err != nil {
			return rpcapi.EpochResult{}, &engineError{code: v1.ErrInvalidArgument, msg: err.Error()}
		}
		epoch, err := ctrl.ApproveProject(ctx, "", key)
		if err != nil {
			return rpcapi.EpochResult{}, &engineError{code: v1.ErrInternal, msg: err.Error()}
		}
		return rpcapi.EpochResult{Epoch: epoch}, nil
	}))
	srv.HandleFunc(rpcapi.MethodHookDeny, unary(func(ctx context.Context, p rpcapi.HookDenyParams) (struct{}, error) {
		key, err := ctrl.RegisterProject(ctx, p.Project)
		if err != nil {
			return struct{}{}, &engineError{code: v1.ErrInvalidArgument, msg: err.Error()}
		}
		if err := ctrl.DenyProject(ctx, "", key); err != nil {
			return struct{}{}, &engineError{code: v1.ErrInternal, msg: err.Error()}
		}
		return struct{}{}, nil
	}))
	srv.HandleFunc(rpcapi.MethodHookRevoke, unary(func(ctx context.Context, p rpcapi.HookRevokeParams) (rpcapi.EpochResult, error) {
		if !p.Confirm {
			return rpcapi.EpochResult{}, &engineError{code: v1.ErrInvalidArgument, msg: "revoking project trust requires confirmation"}
		}
		key, err := ctrl.RegisterProject(ctx, p.Project)
		if err != nil {
			return rpcapi.EpochResult{}, &engineError{code: v1.ErrInvalidArgument, msg: err.Error()}
		}
		epoch, err := ctrl.RevokeProject(ctx, "", key)
		if err != nil {
			return rpcapi.EpochResult{}, &engineError{code: v1.ErrInternal, msg: err.Error()}
		}
		return rpcapi.EpochResult{Epoch: epoch}, nil
	}))
}

func (d *dispatcher) registerNotifications(srv *protocol.Server) {
	svc := d.cfg.Notify
	if svc == nil {
		return
	}
	srv.HandleFunc(rpcapi.MethodNotificationList, unary(func(ctx context.Context, p rpcapi.NotificationListParams) (rpcapi.NotificationListResult, error) {
		rows, err := svc.List(p.Session, p.UnreadOnly, p.Limit)
		if err != nil {
			return rpcapi.NotificationListResult{}, &engineError{code: v1.ErrInternal, msg: err.Error()}
		}
		unread, err := svc.CountUnread(p.Session)
		if err != nil {
			return rpcapi.NotificationListResult{}, &engineError{code: v1.ErrInternal, msg: err.Error()}
		}
		out := rpcapi.NotificationListResult{Notifications: []rpcapi.NotificationInfo{}, Unread: int(unread)}
		for _, n := range rows {
			out.Notifications = append(out.Notifications, rpcapi.NotificationInfo{
				ID: n.ID, Kind: n.Kind, Title: n.Title, Body: n.Body,
				CreatedMS: n.CreatedMS, Read: n.ReadAtMS != 0,
			})
		}
		return out, nil
	}))
	srv.HandleFunc(rpcapi.MethodNotificationRead, unary(func(ctx context.Context, p rpcapi.NotificationReadParams) (rpcapi.NotificationReadResult, error) {
		if err := svc.MarkRead(p.ID); err != nil {
			return rpcapi.NotificationReadResult{}, &engineError{code: v1.ErrNotFound, msg: err.Error()}
		}
		return rpcapi.NotificationReadResult{Read: true}, nil
	}))
}

func (d *dispatcher) registerDiagnostics(srv *protocol.Server) {
	srv.HandleFunc(rpcapi.MethodDiagnosticsDump, unary(func(ctx context.Context, _ noParams) (rpcapi.DiagnosticsDumpResult, error) {
		var buf bytes.Buffer
		err := observability.Dump(&buf, observability.DumpInput{
			BootID:  d.cfg.BootID,
			Version: version.Version,
			Clock:   d.cfg.Clock,
			Metrics: d.cfg.Metrics,
			Extra:   map[string]any{"sessions": d.cfg.Engine.SessionCount()},
		})
		if err != nil {
			return rpcapi.DiagnosticsDumpResult{}, &engineError{code: v1.ErrResourceExhausted, msg: err.Error()}
		}
		return rpcapi.DiagnosticsDumpResult{Dump: json.RawMessage(bytes.TrimSpace(buf.Bytes()))}, nil
	}))
}

// attachStream serves flow 12: one attach_snapshot event frame (pane meta +
// cutover sequence + optional typed replay_gap), then raw-body frames — replay
// ending exactly at UpToSeq, live strictly after (ADR-0004). A wedged client
// is disconnected with a resource_exhausted receipt carrying last_delivered.
func (d *dispatcher) attachStream(ctx context.Context, req protocol.Request, send protocol.SendFunc) *v1.ErrorBody {
	var p rpcapi.AttachParams
	if err := v1.DecodeStrict(req.Params, &p); err != nil {
		return &v1.ErrorBody{Code: v1.ErrInvalidArgument, Message: err.Error()}
	}
	clientID := req.Peer.ConnID
	att, err := d.cfg.Engine.AttachSurface(ctx, clientID, p)
	if err != nil {
		return ErrorBody(err)
	}
	defer att.Detach()
	defer d.cfg.Engine.DetachClient(p.Session, clientID)

	snap := att.Snapshot()
	snapPayload := map[string]any{
		"surface":   snap.Pane.SurfaceID,
		"rows":      snap.Pane.Rows,
		"cols":      snap.Pane.Cols,
		"title":     snap.Pane.Title,
		"up_to_seq": snap.UpToSeq,
	}
	if p.Cells {
		// Minor-1 opt-in: the derived cell grid captured under the attach lock
		// (exact snapshot-at-N; replay resumes strictly after, ADR-0004).
		// Additive field — clients that did not request it see the unchanged
		// minor-0 payload.
		snapPayload["cells"] = rpcapi.AttachSnapshotCells{
			UpToSeq: snap.UpToSeq,
			Grid:    cellGridFrom(snap.Cell),
		}
	}
	if g := snap.ReplayGap; g != nil {
		snapPayload["replay_gap"] = map[string]any{
			"requested_from":  g.RequestedFrom,
			"oldest_retained": g.OldestRetained,
			"latest_seq":      g.LatestSeq,
			"code":            string(g.Code()),
		}
	}
	payload, _ := json.Marshal(snapPayload)
	head := v1.Event{
		Type: v1.TypeEvent, BootID: d.cfg.BootID, Session: p.Session,
		Seq: snap.UpToSeq, Event: "attach_snapshot",
		TimeMS: d.cfg.Clock.NowUnixMilli(), Payload: payload,
	}
	if err := send(head, nil); err != nil {
		return nil
	}
	for {
		select {
		case f, ok := <-att.Frames():
			if !ok {
				if attErr := att.Err(); errors.Is(attErr, attach.ErrSlowConsumer) {
					details, _ := json.Marshal(map[string]uint64{"last_delivered": att.LastDelivered()})
					return &v1.ErrorBody{
						Code: v1.ErrResourceExhausted, Message: attErr.Error(),
						Retryable: true, Details: details,
					}
				}
				return nil
			}
			raw := map[string]any{
				"type": "event", "event": "raw_output",
				"session": p.Session, "surface": p.Surface, "seq": f.Seq,
			}
			if err := send(raw, f.Data); err != nil {
				return nil
			}
		case <-ctx.Done():
			return nil
		}
	}
}

// eventStream serves flow 20: bounded replay spliced with live events, typed
// event_gap boundaries, heartbeats owned by the protocol layer (ADR-0004).
func (d *dispatcher) eventStream(ctx context.Context, req protocol.Request, send protocol.SendFunc) *v1.ErrorBody {
	var p rpcapi.EventSubscribeParams
	if err := v1.DecodeStrict(req.Params, &p); err != nil {
		return &v1.ErrorBody{Code: v1.ErrInvalidArgument, Message: err.Error()}
	}
	sub, err := d.cfg.Engine.SubscribeEvents(ctx, p)
	if err != nil {
		return ErrorBody(err)
	}
	defer sub.Cancel()
	for {
		select {
		case ev, ok := <-sub.C():
			if !ok {
				if sub.Err() != nil {
					details, _ := json.Marshal(map[string]uint64{"last_delivered": sub.LastDelivered()})
					return &v1.ErrorBody{Code: v1.ErrEventGap, Message: sub.Err().Error(), Retryable: true, Details: details}
				}
				return nil
			}
			payload, merr := json.Marshal(map[string]any{
				"workspace": string(ev.Workspace),
				"rev":       ev.Rev,
				"data":      ev.Payload,
			})
			if merr != nil {
				return &v1.ErrorBody{Code: v1.ErrInternal, Message: merr.Error()}
			}
			frame := v1.Event{
				Type: v1.TypeEvent, BootID: d.cfg.BootID, Session: string(ev.Session),
				Seq: ev.Seq, Event: ev.Kind, TimeMS: ev.TimeMS, Payload: payload,
			}
			if err := send(frame, nil); err != nil {
				return nil
			}
		case <-ctx.Done():
			return nil
		}
	}
}
