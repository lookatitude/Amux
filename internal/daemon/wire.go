// wire.go adds the Engine methods that surface the attach, input-lease, and
// event-stream subsystems to the protocol layer (server.go): flow 12 (attach),
// flow 13 (send input), and flow 20 (subscribe to events), plus the health
// counters server.go reports. The heavy lifting lives in internal/attach and
// internal/session; this file only locates the runtime objects and maps their
// typed errors onto the wire taxonomy.
package daemon

import (
	"context"
	"encoding/base64"

	v1 "github.com/amux-run/amux/api/v1"

	"github.com/amux-run/amux/internal/attach"
	"github.com/amux-run/amux/internal/domain"
	"github.com/amux-run/amux/internal/rpcapi"
	"github.com/amux-run/amux/internal/session"
)

// SessionCount reports the number of live session runtimes (daemon.health).
func (e *Engine) SessionCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.sessions)
}

// surface locates one surface runtime, mapping absence to not_found.
func (e *Engine) surface(sessionID, surfaceID string) (*sessionRuntime, *surfaceRuntime, error) {
	rt, err := e.runtime(domain.SessionID(sessionID))
	if err != nil {
		return nil, nil, err
	}
	rt.mu.Lock()
	sr := rt.surfaces[domain.SurfaceID(surfaceID)]
	rt.mu.Unlock()
	if sr == nil {
		return nil, nil, &engineError{code: v1.ErrNotFound, msg: "surface not found: " + surfaceID}
	}
	return rt, sr, nil
}

// attachSurfaceFor builds the attach fanout/lease surface for one surface
// runtime, wiring the raw ring as the output authority, the VT engine as the
// cell-snapshot source, and the PTY supervisor as the lease-gated input sink
// (ADR-0004). Called at spawn and restore, before the surface is published.
func attachSurfaceFor(rt *sessionRuntime, sr *surfaceRuntime) error {
	surf, err := attach.NewSurface(attach.SurfaceConfig{
		ID:       string(sr.id),
		Ring:     sr.ring,
		Snapshot: sr.engine.CellSnapshot,
		InputSink: func(p []byte) error {
			return rt.sup.Input(string(sr.id), p)
		},
	}, nil)
	if err != nil {
		return &engineError{code: v1.ErrInternal, msg: err.Error()}
	}
	sr.surface = surf
	return nil
}

// mapAttachError translates internal/attach errors to the wire taxonomy.
func mapAttachError(err error) error {
	if code, ok := attach.ErrorCode(err); ok {
		return &engineError{code: code, msg: err.Error()}
	}
	return &engineError{code: v1.ErrInternal, msg: err.Error()}
}

// SendInput implements flow 13: lease-gated input. The lease identity is the
// wire lease_id; acquisition never implicitly takes over another holder
// (ADR-0004), so a second writer is rejected before any byte reaches the PTY.
func (e *Engine) SendInput(ctx context.Context, p rpcapi.InputSendParams) (rpcapi.InputSendResult, error) {
	if p.LeaseID == "" {
		return rpcapi.InputSendResult{}, &engineError{code: v1.ErrInvalidArgument, msg: "input.send requires lease_id"}
	}
	_, sr, err := e.surface(p.Session, p.Surface)
	if err != nil {
		return rpcapi.InputSendResult{}, err
	}
	if sr.surface == nil {
		return rpcapi.InputSendResult{}, &engineError{code: v1.ErrConflict, msg: "surface has no attach runtime"}
	}
	data, derr := base64.StdEncoding.DecodeString(p.DataB64)
	if derr != nil {
		return rpcapi.InputSendResult{}, &engineError{code: v1.ErrInvalidArgument, msg: "data_b64: " + derr.Error()}
	}
	client := attach.ClientID(p.LeaseID)
	if p.Takeover {
		// Lease takeover displaces the current holder — a destructive
		// transition in the security confirmation matrix, so it fails closed
		// without the confirmation token.
		if !p.Confirm {
			return rpcapi.InputSendResult{}, &engineError{code: v1.ErrInvalidArgument, msg: "lease takeover requires confirmation"}
		}
		if err := sr.surface.TakeoverInput(client); err != nil {
			return rpcapi.InputSendResult{}, mapAttachError(err)
		}
	} else if err := sr.surface.AcquireInput(client); err != nil {
		return rpcapi.InputSendResult{}, mapAttachError(err)
	}
	if err := sr.surface.Write(client, data); err != nil {
		return rpcapi.InputSendResult{}, mapAttachError(err)
	}
	return rpcapi.InputSendResult{Bytes: len(data)}, nil
}

// ReleaseInput releases an explicitly held input lease (the CLI's lease
// identity outlives its connections, so release is an explicit op — connection
// teardown only releases connection-scoped identities).
func (e *Engine) ReleaseInput(ctx context.Context, p rpcapi.InputReleaseParams) (struct{}, error) {
	if p.LeaseID == "" {
		return struct{}{}, &engineError{code: v1.ErrInvalidArgument, msg: "input.release requires lease_id"}
	}
	_, sr, err := e.surface(p.Session, p.Surface)
	if err != nil {
		return struct{}{}, err
	}
	if sr.surface == nil {
		return struct{}{}, &engineError{code: v1.ErrConflict, msg: "surface has no attach runtime"}
	}
	if err := sr.surface.ReleaseInput(attach.ClientID(p.LeaseID)); err != nil {
		return struct{}{}, mapAttachError(err)
	}
	return struct{}{}, nil
}

// AttachSurface implements flow 12's server side: it opens the linearized
// snapshot -> replay -> live attachment (ADR-0004 cutover) and returns it for
// the stream handler to pump onto the connection. clientID identifies the
// peer for lease bookkeeping and the slow-consumer receipt.
func (e *Engine) AttachSurface(ctx context.Context, clientID string, p rpcapi.AttachParams) (*attach.Attachment, error) {
	_, sr, err := e.surface(p.Session, p.Surface)
	if err != nil {
		return nil, err
	}
	if sr.surface == nil {
		return nil, &engineError{code: v1.ErrConflict, msg: "surface has no attach runtime"}
	}
	att, aerr := sr.surface.Attach(attach.ClientID(clientID), attach.AttachOptions{FromSeq: p.FromSeq})
	if aerr != nil {
		return nil, mapAttachError(aerr)
	}
	return att, nil
}

// DetachClient releases every lease the identified client holds in the given
// session (connection-teardown hygiene; detach is not stop, ADR-0004).
func (e *Engine) DetachClient(sessionID, clientID string) {
	rt, err := e.runtime(domain.SessionID(sessionID))
	if err != nil {
		return
	}
	rt.mu.Lock()
	surfaces := make([]*surfaceRuntime, 0, len(rt.surfaces))
	for _, sr := range rt.surfaces {
		surfaces = append(surfaces, sr)
	}
	rt.mu.Unlock()
	for _, sr := range surfaces {
		if sr.surface != nil {
			_ = sr.surface.ReleaseInput(attach.ClientID(clientID))
		}
	}
}

// SubscribeEvents implements flow 20's server side: a bounded, optionally
// workspace-filtered subscription spliced replay->live with typed event_gap
// boundaries (ADR-0004).
func (e *Engine) SubscribeEvents(ctx context.Context, p rpcapi.EventSubscribeParams) (*session.Subscription, error) {
	rt, err := e.runtime(domain.SessionID(p.Session))
	if err != nil {
		return nil, err
	}
	var filter session.Filter
	if p.Workspace != "" {
		ws := domain.WorkspaceID(p.Workspace)
		filter = func(ev session.Event) bool { return ev.Workspace == ws }
	}
	sub, serr := rt.graphActor().Subscribe(ctx, session.SubscribeOptions{FromSeq: p.FromSeq, Filter: filter})
	if serr != nil {
		var gap *session.EventGapError
		if isEventGap(serr, &gap) {
			return nil, &engineError{code: v1.ErrEventGap, msg: gap.Error()}
		}
		return nil, &engineError{code: v1.ErrInternal, msg: serr.Error()}
	}
	return sub, nil
}
