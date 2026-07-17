// Package protocol implements the daemon-side server loop of the local control
// protocol (ADR-0003) over any platform.LocalListener (ADR-0006 §LocalTransport).
// It owns exactly the connection lifecycle:
//
//  1. Peer gate (STR-2): the FIRST byte of every connection is gated on
//     PeerCredentials.PeerUID via LocalConn.Control; a UID mismatch with the
//     daemon (or any credential error) closes the connection with zero frames
//     served and an audit log record. There is no way to disable this.
//  2. Negotiation (ADR-0003): hello/welcome with v1.Negotiate; a major mismatch
//     is answered with an unsupported_version error response and closed BEFORE
//     any request is accepted.
//  3. Bounded request loop (STR-5): frames via api/v1.ReadFrame (limits are
//     validated before allocation there); unknown methods are not_found;
//     handler panics are recovered to internal errors; every response carries
//     exactly one of result or error.
//  4. Streams: a stream request's frames interleave with responses and
//     heartbeats on the same connection, so ALL writes go through one
//     serialized writer (per-connection mutex keeps frame boundaries atomic).
//
// Concurrency model: unary requests on one connection are handled sequentially
// in the read loop — one in-flight request per connection keeps write ordering
// trivial and matches the shared client, which also issues one call at a time.
// Stream requests run in their own goroutine so events multiplex with later
// unary requests via dedicated frames.
//
// Time: request deadlines (deadline_ms) use context real time; heartbeat
// pacing uses Options.Clock so tests drive it deterministically with
// platform.FakeClock (a short real-time ticker polls the injected clock rather
// than sleeping HeartbeatMS of wall time).
package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	v1 "github.com/amux-run/amux/api/v1"
	"github.com/amux-run/amux/internal/platform"
)

// ErrServerClosed is returned by Serve when the server has been Closed.
var ErrServerClosed = errors.New("amux protocol: server closed")

const (
	// defaultHeartbeatMS is the heartbeat cadence when Options.HeartbeatMS is
	// unset (ADR-0004 owns the heartbeat contract; 5 s is its default).
	defaultHeartbeatMS = int64(5000)
	// handshakeTimeout bounds how long a connected peer may stall before
	// sending hello (STR-7: a connect-and-stall peer is disconnected).
	handshakeTimeout = 10 * time.Second
)

// Peer identifies the verified sender of a request: the SO_PEERCRED-checked
// UID and a per-connection ID for audit correlation.
type Peer struct {
	UID    uint32
	ConnID string
}

// Request is the handler-facing view of one wire request. Params is the raw
// JSON the command layer decodes strictly (v1.DecodeStrict, STR-6); Peer is the
// verified caller identity.
type Request struct {
	Method string
	Params json.RawMessage
	Peer   Peer
}

// HandlerFunc serves one unary request. It returns exactly one of a raw JSON
// result or a typed error body; returning (nil, nil) yields an empty {} result.
// Implementations must honor ctx: it is cancelled on connection teardown and
// carries the request deadline when the client set deadline_ms.
type HandlerFunc func(ctx context.Context, req Request) (json.RawMessage, *v1.ErrorBody)

// SendFunc writes one stream frame: header is marshaled as the frame header
// JSON (typically a v1.Event, but a raw-body frame may carry any header shape)
// and body is the optional raw payload. Writes are serialized with every other
// frame on the connection; an error means the connection is being torn down.
type SendFunc func(header any, body []byte) error

// StreamFunc serves one streaming request. It emits frames via send and
// returns when the stream ends; the server then writes the final response
// frame (the request's ID) carrying {"done":true} or the returned error body.
type StreamFunc func(ctx context.Context, req Request, send SendFunc) *v1.ErrorBody

// Options configures a Server. Peers is mandatory in spirit (STR-2): when nil,
// every connection is rejected before its first byte, because peer identity
// can then never be verified. On the production Linux target the daemon wires
// platform's real SO_PEERCRED implementation; non-Linux platforms fail closed
// by design, so tests inject a fake.
type Options struct {
	// BootID identifies this daemon incarnation; it is carried in welcome and
	// every event so clients detect restarts (ADR-0003).
	BootID string
	// ServerTag is the human server identity advertised in welcome.
	ServerTag string
	// Clock paces heartbeats and stamps event times. Nil means the system clock.
	Clock platform.Clock
	// Peers verifies every connection's UID before the first protocol byte.
	Peers platform.PeerCredentials
	// Logger receives audit and diagnostic records. Nil discards.
	Logger *slog.Logger
	// HeartbeatMS is the heartbeat cadence while streams are active; <=0 means
	// the 5000 ms default.
	HeartbeatMS int64
}

// Server is the daemon-side protocol loop. Register handlers before Serve;
// Serve accepts until Close.
type Server struct {
	opts    Options
	log     *slog.Logger
	selfUID uint32

	baseCtx    context.Context
	baseCancel context.CancelFunc

	regMu    sync.RWMutex
	handlers map[string]HandlerFunc
	streams  map[string]StreamFunc

	mu        sync.Mutex
	listeners map[platform.LocalListener]struct{}
	conns     map[*serverConn]struct{}
	closed    bool

	connSeq atomic.Uint64
	wg      sync.WaitGroup
}

// NewServer builds a Server from o, applying the documented defaults.
func NewServer(o Options) *Server {
	if o.HeartbeatMS <= 0 {
		o.HeartbeatMS = defaultHeartbeatMS
	}
	if o.Clock == nil {
		o.Clock = platform.NewSystemClock()
	}
	log := o.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		opts:       o,
		log:        log,
		selfUID:    uint32(os.Getuid()),
		baseCtx:    ctx,
		baseCancel: cancel,
		handlers:   make(map[string]HandlerFunc),
		streams:    make(map[string]StreamFunc),
		listeners:  make(map[platform.LocalListener]struct{}),
		conns:      make(map[*serverConn]struct{}),
	}
}

// HandleFunc registers the unary handler for method, replacing any previous
// registration. A method registered as a stream takes precedence at dispatch.
func (s *Server) HandleFunc(method string, fn HandlerFunc) {
	s.regMu.Lock()
	defer s.regMu.Unlock()
	s.handlers[method] = fn
}

// StreamFunc registers the streaming handler for method, replacing any
// previous registration.
func (s *Server) StreamFunc(method string, fn StreamFunc) {
	s.regMu.Lock()
	defer s.regMu.Unlock()
	s.streams[method] = fn
}

func (s *Server) handlerFor(method string) HandlerFunc {
	s.regMu.RLock()
	defer s.regMu.RUnlock()
	return s.handlers[method]
}

func (s *Server) streamFor(method string) StreamFunc {
	s.regMu.RLock()
	defer s.regMu.RUnlock()
	return s.streams[method]
}

// Serve accepts connections from l until Close. It returns nil after Close and
// the accept error otherwise. Multiple concurrent Serve calls (one per
// listener) are supported; Close stops them all.
func (s *Server) Serve(l platform.LocalListener) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrServerClosed
	}
	s.listeners[l] = struct{}{}
	s.mu.Unlock()

	for {
		conn, err := l.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				return nil
			}
			return err
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			conn.Close()
			return nil
		}
		s.wg.Add(1)
		s.mu.Unlock()
		go func() {
			defer s.wg.Done()
			s.serveConn(conn)
		}()
	}
}

// Close stops accepting, tears down every live connection (cancelling every
// in-flight handler and stream context), and waits for connection goroutines
// to drain. Idempotent.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	listeners := make([]platform.LocalListener, 0, len(s.listeners))
	for l := range s.listeners {
		listeners = append(listeners, l)
	}
	conns := make([]*serverConn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()

	s.baseCancel()
	for _, l := range listeners {
		l.Close()
	}
	for _, c := range conns {
		c.teardown()
	}
	s.wg.Wait()
	return nil
}

// serveConn runs one connection: STR-2 peer gate, handshake, request loop.
func (s *Server) serveConn(conn platform.LocalConn) {
	uid, err := s.verifyPeer(conn)
	if err != nil {
		// STR-2: close with zero frames served; the rejection is audited.
		s.log.Warn("amux protocol: peer rejected before first protocol byte", "err", err)
		conn.Close()
		return
	}

	ctx, cancel := context.WithCancel(s.baseCtx)
	sc := &serverConn{
		s:      s,
		conn:   conn,
		ctx:    ctx,
		cancel: cancel,
		uid:    uid,
		id:     fmt.Sprintf("conn-%d", s.connSeq.Add(1)),
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		cancel()
		conn.Close()
		return
	}
	s.conns[sc] = struct{}{}
	s.mu.Unlock()
	defer func() {
		sc.teardown()
		s.mu.Lock()
		delete(s.conns, sc)
		s.mu.Unlock()
	}()

	if !sc.handshake() {
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		sc.heartbeatLoop()
	}()
	sc.requestLoop()
}

// verifyPeer runs the mandatory SO_PEERCRED-shaped check before any protocol
// byte (STR-2). Any error — including a nil Peers option and the fail-closed
// non-Linux platform implementation — rejects the connection.
func (s *Server) verifyPeer(conn platform.LocalConn) (uint32, error) {
	if s.opts.Peers == nil {
		return 0, errors.New("no PeerCredentials configured (STR-2 is mandatory; failing closed)")
	}
	var uid uint32
	err := conn.Control(func(fd uintptr) error {
		u, e := s.opts.Peers.PeerUID(fd)
		uid = u
		return e
	})
	if err != nil {
		return 0, fmt.Errorf("peer credential check: %w", err)
	}
	if uid != s.selfUID {
		return 0, fmt.Errorf("peer uid %d does not match daemon uid %d", uid, s.selfUID)
	}
	return uid, nil
}

// serverConn is one accepted, peer-verified connection.
type serverConn struct {
	s      *Server
	conn   platform.LocalConn
	ctx    context.Context
	cancel context.CancelFunc
	uid    uint32
	id     string

	// wmu serializes every frame write on the connection: responses, stream
	// frames, and heartbeats interleave, and a torn frame would desynchronize
	// the peer's reader permanently.
	wmu sync.Mutex

	closeOnce sync.Once

	// activeStreams gates heartbeat emission; lastBeatMS is the Options.Clock
	// timestamp of the previous beat (or the pacing baseline while idle).
	activeStreams atomic.Int64
	lastBeatMS    atomic.Int64
}

// teardown cancels every in-flight handler/stream context and closes the
// connection. Idempotent; safe from any goroutine (including write failures).
func (sc *serverConn) teardown() {
	sc.closeOnce.Do(func() {
		sc.cancel()
		sc.conn.Close()
	})
}

// handshake performs the ADR-0003 negotiation. It reports whether the
// connection may proceed to the request loop.
func (sc *serverConn) handshake() bool {
	// STR-7: a peer that connects and stalls is disconnected within the bound.
	// Closing the conn unblocks the pending ReadFrame.
	timer := time.AfterFunc(handshakeTimeout, sc.teardown)
	header, _, err := v1.ReadFrame(sc.conn)
	timer.Stop()
	if err != nil {
		return false
	}
	var hello v1.Hello
	if err := v1.DecodeLenient(header, &hello); err != nil || hello.Type != v1.TypeHello {
		sc.writeResult("", nil, &v1.ErrorBody{Code: v1.ErrInvalidArgument, Message: "expected hello frame"})
		return false
	}
	major, minor, eb := v1.Negotiate(v1.Major, v1.Minor, hello.Major, hello.Minor)
	if eb != nil {
		// Major mismatch: unsupported_version error response, closed before any
		// request is accepted (ADR-0003 negotiation).
		sc.writeResult("", nil, eb)
		return false
	}
	welcome := v1.Welcome{
		Type:   v1.TypeWelcome,
		Major:  major,
		Minor:  minor,
		BootID: sc.s.opts.BootID,
		Server: sc.s.opts.ServerTag,
	}
	return sc.writeHeader(welcome, nil) == nil
}

// requestLoop reads request frames until the connection ends. Framing errors
// tear the connection down (a desynchronized byte stream cannot be resumed);
// envelope-level errors answer invalid_argument and keep the connection up.
func (sc *serverConn) requestLoop() {
	for {
		header, _, err := v1.ReadFrame(sc.conn)
		if err != nil {
			// STR-5: an oversize length prefix surfaces as a typed FrameError
			// before allocation; answer best-effort with its code, then close.
			// io.EOF is a clean close; ErrUnexpectedEOF is a truncated frame;
			// both (and any other read error) tear the connection down.
			var fe *v1.FrameError
			if errors.As(err, &fe) {
				sc.writeResult("", nil, &v1.ErrorBody{Code: fe.Code, Message: fe.Error()})
			}
			return
		}
		var req v1.Request
		if err := v1.DecodeLenient(header, &req); err != nil {
			sc.writeResult("", nil, invalidArg("malformed frame header: "+err.Error()))
			continue
		}
		if req.Type != v1.TypeRequest {
			// Anything but a request between responses is a client bug, not an
			// attack: answer typed, keep the connection up.
			sc.writeResult(req.ID, nil, invalidArg(fmt.Sprintf("expected request frame, got %q", req.Type)))
			continue
		}
		if req.ID == "" {
			sc.writeResult("", nil, invalidArg("request id is required"))
			continue
		}
		preq := Request{Method: req.Method, Params: req.Params, Peer: Peer{UID: sc.uid, ConnID: sc.id}}
		if fn := sc.s.streamFor(req.Method); fn != nil {
			sc.s.wg.Add(1)
			go func() {
				defer sc.s.wg.Done()
				sc.runStream(req.ID, req.DeadlineMS, preq, fn)
			}()
			continue
		}
		if fn := sc.s.handlerFor(req.Method); fn != nil {
			sc.runUnary(req.ID, req.DeadlineMS, preq, fn)
			continue
		}
		sc.writeResult(req.ID, nil, &v1.ErrorBody{Code: v1.ErrNotFound, Message: "unknown method: " + req.Method})
	}
}

// reqCtx derives the per-request context: cancelled on connection teardown,
// with a real-time deadline when the client sent deadline_ms > 0 (ADR-0003).
func (sc *serverConn) reqCtx(deadlineMS int64) (context.Context, context.CancelFunc) {
	if deadlineMS > 0 {
		return context.WithTimeout(sc.ctx, time.Duration(deadlineMS)*time.Millisecond)
	}
	return context.WithCancel(sc.ctx)
}

// runUnary invokes fn and writes the single response frame. The handler runs
// in a goroutine so a deadline expiry answers the client at the deadline even
// if the handler is slow to unwind; the handler's ctx is done at that point
// and a well-behaved handler exits promptly (a handler that ignores ctx leaks
// its goroutine until it returns — its late result is discarded).
func (sc *serverConn) runUnary(id string, deadlineMS int64, req Request, fn HandlerFunc) {
	ctx, cancel := sc.reqCtx(deadlineMS)
	defer cancel()
	type outcome struct {
		result json.RawMessage
		eb     *v1.ErrorBody
	}
	ch := make(chan outcome, 1)
	go func() {
		defer func() {
			// Fail closed on a handler panic: internal error, connection stays up.
			if r := recover(); r != nil {
				sc.s.log.Error("amux protocol: handler panic recovered",
					"method", req.Method, "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
				ch <- outcome{nil, &v1.ErrorBody{Code: v1.ErrInternal, Message: "internal error"}}
			}
		}()
		result, eb := fn(ctx, req)
		ch <- outcome{result, eb}
	}()
	select {
	case o := <-ch:
		sc.writeResult(id, o.result, o.eb)
	case <-ctx.Done():
		sc.writeResult(id, nil, ctxErrBody(ctx))
	}
}

// runStream invokes fn with a serialized SendFunc, tracking stream liveness
// for heartbeat gating, and writes the final response frame when fn returns.
func (sc *serverConn) runStream(id string, deadlineMS int64, req Request, fn StreamFunc) {
	ctx, cancel := sc.reqCtx(deadlineMS)
	defer cancel()

	// Heartbeat gating: (re)baseline the pacer when the first stream starts so
	// the first beat lands one full HeartbeatMS after stream activation.
	if sc.activeStreams.Add(1) == 1 {
		sc.lastBeatMS.Store(sc.s.opts.Clock.NowUnixMilli())
	}
	defer sc.activeStreams.Add(-1)

	send := SendFunc(func(header any, body []byte) error {
		return sc.writeHeader(header, body)
	})
	var eb *v1.ErrorBody
	func() {
		defer func() {
			if r := recover(); r != nil {
				sc.s.log.Error("amux protocol: stream panic recovered",
					"method", req.Method, "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
				eb = &v1.ErrorBody{Code: v1.ErrInternal, Message: "internal error"}
			}
		}()
		eb = fn(ctx, req, send)
	}()
	if eb != nil {
		sc.writeResult(id, nil, eb)
		return
	}
	sc.writeResult(id, json.RawMessage(`{"done":true}`), nil)
}

// heartbeatLoop emits a heartbeat event (event "heartbeat", session "", seq 0)
// every HeartbeatMS of Options.Clock time while at least one stream is active.
// A short real-time ticker polls the injected clock, so platform.FakeClock
// drives emission deterministically in tests while production simply reads the
// system clock (ADR-0004 heartbeat contract).
func (sc *serverConn) heartbeatLoop() {
	hb := sc.s.opts.HeartbeatMS
	ticker := time.NewTicker(heartbeatPoll(hb))
	defer ticker.Stop()
	for {
		select {
		case <-sc.ctx.Done():
			return
		case <-ticker.C:
		}
		if sc.activeStreams.Load() == 0 {
			continue
		}
		now := sc.s.opts.Clock.NowUnixMilli()
		last := sc.lastBeatMS.Load()
		if now-last < hb {
			continue
		}
		if !sc.lastBeatMS.CompareAndSwap(last, now) {
			continue
		}
		sc.writeHeader(v1.Event{
			Type:   v1.TypeEvent,
			BootID: sc.s.opts.BootID,
			Event:  "heartbeat",
			TimeMS: now,
		}, nil)
	}
}

// heartbeatPoll picks the real-time poll interval for the clock-driven
// heartbeat pacer: a quarter of the cadence, clamped to [1ms, 100ms].
func heartbeatPoll(hbMS int64) time.Duration {
	d := time.Duration(hbMS) * time.Millisecond / 4
	if d < time.Millisecond {
		d = time.Millisecond
	}
	if d > 100*time.Millisecond {
		d = 100 * time.Millisecond
	}
	return d
}

// writeHeader marshals header as the frame header JSON and writes one frame
// through the serialized writer. A write failure tears the connection down
// (a slow or vanished reader is the client's problem; the server never blocks
// other connections on it) and is returned to the caller.
func (sc *serverConn) writeHeader(header any, body []byte) error {
	hb, err := json.Marshal(header)
	if err != nil {
		return fmt.Errorf("amux protocol: marshal frame header: %w", err)
	}
	sc.wmu.Lock()
	err = v1.WriteFrame(sc.conn, hb, body)
	sc.wmu.Unlock()
	if err != nil {
		sc.teardown()
	}
	return err
}

// writeResult writes the response frame for id carrying exactly one of
// result or error (ADR-0003). A nil result with a nil error becomes {}.
func (sc *serverConn) writeResult(id string, result json.RawMessage, eb *v1.ErrorBody) {
	resp := v1.Response{Type: v1.TypeResponse, ID: id}
	if eb != nil {
		resp.Error = eb
	} else {
		if result == nil {
			result = json.RawMessage(`{}`)
		}
		resp.Result = result
	}
	// Best-effort: writeHeader already tears down on failure.
	_ = sc.writeHeader(resp, nil)
}

// ctxErrBody maps a done request context to the wire error: a deadline_ms
// expiry is a retryable resource_exhausted (the frozen taxonomy has no
// deadline code; exhaustion of the client-granted time budget is the closest
// fit and retryable-by-design), a teardown cancellation is internal.
func ctxErrBody(ctx context.Context) *v1.ErrorBody {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return &v1.ErrorBody{Code: v1.ErrResourceExhausted, Message: "deadline_ms exceeded", Retryable: true}
	}
	return &v1.ErrorBody{Code: v1.ErrInternal, Message: "connection closing", Retryable: true}
}

func invalidArg(msg string) *v1.ErrorBody {
	return &v1.ErrorBody{Code: v1.ErrInvalidArgument, Message: msg}
}
