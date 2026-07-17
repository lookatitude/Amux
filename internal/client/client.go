// Package client is the shared reconnecting protocol client for the local
// control protocol (ADR-0003). The CLI, the TUI, and the test suites all speak
// to the daemon through this one implementation — there is deliberately no
// CLI-only mutation path, so every caller inherits the same negotiation,
// deadline, error-typing, and reconnect discipline.
//
// Design (kept deliberately simple and correct):
//
//   - A Client owns ONE connection with a demultiplexing read loop that routes
//     response frames by request ID to the waiting call and every other frame
//     (events, raw-body stream frames, heartbeats) to the single active
//     stream. At most one stream per Client: a second Stream returns
//     ErrStreamActive — open a second Client for a second subscription.
//   - One in-flight Call at a time per connection (an internal mutex
//     serializes them). This matches the server's sequential per-connection
//     request handling and keeps ID/write ordering trivial.
//   - Reconnection is NOT automatic mid-call: a broken connection fails the
//     in-flight call with a typed retryable *Error, and the caller decides
//     when to Redial. Redial re-dials and renegotiates; when the daemon's boot
//     ID changed (a restart) it returns ErrBootChanged even though the new
//     connection is live, because the caller MUST snapshot and re-subscribe
//     before trusting any cursor (ADR-0003/0004 boot-id contract).
//   - Heartbeat events (event "heartbeat") are surfaced to Stream.Recv callers
//     unfiltered: consumers use them as liveness signals and filter them out
//     of their event streams themselves.
//
// Time: a ctx deadline on Call/Stream is forwarded to the daemon as the
// request's deadline_ms hint and also bounds the local wait.
package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	v1 "github.com/amux-run/amux/api/v1"
	"github.com/amux-run/amux/internal/platform"
)

// Error is the typed protocol error every failed Call/Stream surfaces. It is
// errors.As-able; Code is the frozen ADR-0003 taxonomy and Retryable tells the
// caller whether a bare retry could succeed. Details is the server's optional
// structured context (e.g. rpcapi.ReplayGapDetails on a replay_gap) verbatim —
// automation branches on Code + Details, never on the Message text.
type Error struct {
	Code      v1.ErrorCode
	Message   string
	Retryable bool
	Details   json.RawMessage
}

func (e *Error) Error() string { return "amux client: " + string(e.Code) + ": " + e.Message }

// CodeOf extracts the protocol error code from err, or "" when err carries no
// *Error (automation must branch on codes, never on message strings).
func CodeOf(err error) v1.ErrorCode {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// ErrBootChanged is returned by Redial when reconnection SUCCEEDED but the
// daemon's boot ID differs from the previous connection: the daemon restarted,
// every event cursor is void, and the caller must snapshot and re-subscribe
// before resuming (ADR-0004 event-gap contract). The Client is usable.
var ErrBootChanged = errors.New("amux client: daemon boot id changed; snapshot and re-subscribe")

// ErrStreamActive is returned by Stream while another stream is active on this
// Client. A Client multiplexes exactly one stream; open a second Client for a
// concurrent subscription.
var ErrStreamActive = errors.New("amux client: a stream is already active on this client; open a second Client")

// ErrClosed is returned by every method after Close.
var ErrClosed = errors.New("amux client: closed")

// connLost types a transport-level failure as the retryable protocol error the
// package contract promises: the frozen taxonomy has no transport code, so a
// lost connection maps to a retryable internal (retry via Redial).
func connLost(cause error) *Error {
	msg := "connection lost; Redial to reconnect"
	if cause != nil {
		msg += ": " + cause.Error()
	}
	return &Error{Code: v1.ErrInternal, Message: msg, Retryable: true}
}

// Client is a reconnecting protocol client. Create it with Dial; it is safe
// for concurrent use (calls serialize; see the package comment).
type Client struct {
	transport platform.LocalTransport
	spec      platform.TransportSpec
	tag       string

	// callMu serializes Call/Stream/Redial so exactly one request is in flight
	// per connection and a Redial never swaps the session under a caller.
	callMu sync.Mutex

	mu     sync.Mutex // guards sess, bootID, minor, closed
	sess   *session
	bootID string
	minor  int
	closed bool

	reqSeq atomic.Uint64
}

// Dial connects to the daemon endpoint via t, performs the hello/welcome
// negotiation advertising tag as the client identity, and returns a ready
// Client. A major-version rejection surfaces as *Error with code
// unsupported_version.
func Dial(ctx context.Context, t platform.LocalTransport, spec platform.TransportSpec, tag string) (*Client, error) {
	c := &Client{transport: t, spec: spec, tag: tag}
	sess, boot, minor, err := c.connect(ctx)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.sess, c.bootID, c.minor = sess, boot, minor
	c.mu.Unlock()
	return c, nil
}

// BootID returns the daemon incarnation ID from the most recent welcome.
func (c *Client) BootID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bootID
}

// Close tears down the connection. Idempotent. In-flight calls and streams
// fail with the retryable connection-lost error.
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	sess := c.sess
	c.sess = nil
	c.mu.Unlock()
	if sess != nil {
		sess.close()
	}
	return nil
}

// Redial drops the current connection (if any), re-dials, and renegotiates.
// It returns nil when reconnected to the same daemon incarnation,
// ErrBootChanged when reconnected to a RESTARTED daemon (the connection is
// live, but the caller must snapshot + re-subscribe), and any other error when
// reconnection failed. Redial is never automatic: a broken connection fails
// the in-flight call as retryable and waits for the caller's decision.
func (c *Client) Redial(ctx context.Context) error {
	c.callMu.Lock()
	defer c.callMu.Unlock()

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClosed
	}
	old := c.sess
	prevBoot := c.bootID
	c.mu.Unlock()

	if old != nil {
		old.close()
	}
	sess, boot, minor, err := c.connect(ctx)
	if err != nil {
		return err
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		sess.close()
		return ErrClosed
	}
	c.sess, c.bootID, c.minor = sess, boot, minor
	c.mu.Unlock()
	if boot != prevBoot {
		return ErrBootChanged
	}
	return nil
}

// Call performs one unary request. params (when non-nil) is marshaled as the
// request params; result (when non-nil) receives the response result via
// strict decode (unknown result fields are contract violations, STR-6). A ctx
// deadline is forwarded as deadline_ms. Protocol failures surface as *Error;
// a broken connection surfaces as a retryable *Error and the caller chooses
// when to Redial.
func (c *Client) Call(ctx context.Context, method string, params any, result any) error {
	c.callMu.Lock()
	defer c.callMu.Unlock()

	sess, err := c.session()
	if err != nil {
		return err
	}
	raw, deadlineMS, err := encodeParams(ctx, method, params)
	if err != nil {
		return err
	}
	id := c.nextID()
	ch := make(chan *v1.Response, 1)
	sess.addPending(id, ch)
	defer sess.removePending(id)

	req := v1.Request{Type: v1.TypeRequest, ID: id, Method: method, DeadlineMS: deadlineMS, Params: raw}
	if err := sess.writeHeader(req, nil); err != nil {
		return connLost(err)
	}
	select {
	case resp := <-ch:
		if resp.Error != nil {
			return &Error{Code: resp.Error.Code, Message: resp.Error.Message, Retryable: resp.Error.Retryable, Details: resp.Error.Details}
		}
		if result == nil {
			return nil
		}
		if err := v1.DecodeStrict(resp.Result, result); err != nil {
			return fmt.Errorf("amux client: strict result decode for %s: %w", method, err)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-sess.done:
		return connLost(sess.failure())
	}
}

// Stream opens the Client's single stream. Events — including heartbeats and
// raw-body frames — arrive via Recv until the server ends the stream (Recv
// returns io.EOF on {"done":true}, or the typed *Error the server sent). A
// second Stream while one is active returns ErrStreamActive.
func (c *Client) Stream(ctx context.Context, method string, params any) (*Stream, error) {
	c.callMu.Lock()
	defer c.callMu.Unlock()

	sess, err := c.session()
	if err != nil {
		return nil, err
	}
	raw, deadlineMS, err := encodeParams(ctx, method, params)
	if err != nil {
		return nil, err
	}

	id := c.nextID()
	st := &Stream{
		id:   id,
		ctx:  ctx,
		sess: sess,
		ch:   make(chan streamMsg, streamBuffer),
		term: make(chan struct{}),
	}
	sess.mu.Lock()
	if cur := sess.stream; cur != nil {
		select {
		case <-cur.term: // previous stream finished; replace it
		default:
			sess.mu.Unlock()
			return nil, ErrStreamActive
		}
	}
	sess.stream = st
	sess.mu.Unlock()

	req := v1.Request{Type: v1.TypeRequest, ID: id, Method: method, DeadlineMS: deadlineMS, Params: raw}
	if err := sess.writeHeader(req, nil); err != nil {
		sess.mu.Lock()
		if sess.stream == st {
			sess.stream = nil
		}
		sess.mu.Unlock()
		return nil, connLost(err)
	}
	return st, nil
}

// session returns the live session or the typed error explaining why there is
// none. A dead-but-present session is returned as-is: its writes fail and the
// caller receives the retryable connection-lost error.
func (c *Client) session() (*session, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, ErrClosed
	}
	if c.sess == nil {
		return nil, connLost(nil)
	}
	return c.sess, nil
}

// nextID returns the next monotonic per-client request ID.
func (c *Client) nextID() string {
	return fmt.Sprintf("req-%d", c.reqSeq.Add(1))
}

// connect dials the transport and performs the hello/welcome negotiation,
// returning a running session. ctx bounds the handshake (the transport seam's
// Dial itself is not ctx-aware; the handshake read is).
func (c *Client) connect(ctx context.Context) (*session, string, int, error) {
	conn, err := c.transport.Dial(c.spec)
	if err != nil {
		return nil, "", 0, err
	}
	hello := v1.Hello{Type: v1.TypeHello, Major: v1.Major, Minor: v1.Minor, Client: c.tag}
	hb, err := json.Marshal(hello)
	if err != nil {
		conn.Close()
		return nil, "", 0, err
	}
	if err := v1.WriteFrame(conn, hb, nil); err != nil {
		conn.Close()
		return nil, "", 0, fmt.Errorf("amux client: write hello: %w", err)
	}

	type readResult struct {
		header []byte
		err    error
	}
	ch := make(chan readResult, 1)
	go func() {
		h, _, err := v1.ReadFrame(conn)
		ch <- readResult{h, err}
	}()
	var header []byte
	select {
	case r := <-ch:
		if r.err != nil {
			conn.Close()
			return nil, "", 0, fmt.Errorf("amux client: handshake read: %w", r.err)
		}
		header = r.header
	case <-ctx.Done():
		conn.Close()
		<-ch // reap the reader
		return nil, "", 0, ctx.Err()
	}

	var probe struct {
		Type v1.MessageType `json:"type"`
	}
	_ = v1.DecodeLenient(header, &probe)
	switch probe.Type {
	case v1.TypeWelcome:
		var w v1.Welcome
		if err := v1.DecodeLenient(header, &w); err != nil {
			conn.Close()
			return nil, "", 0, fmt.Errorf("amux client: decode welcome: %w", err)
		}
		sess := newSession(conn)
		go sess.readLoop()
		return sess, w.BootID, w.Minor, nil
	case v1.TypeResponse:
		// The server rejects negotiation with an error response (ADR-0003).
		var resp v1.Response
		conn.Close()
		if err := v1.DecodeLenient(header, &resp); err == nil && resp.Error != nil {
			return nil, "", 0, &Error{Code: resp.Error.Code, Message: resp.Error.Message, Retryable: resp.Error.Retryable, Details: resp.Error.Details}
		}
		return nil, "", 0, errors.New("amux client: malformed handshake rejection")
	default:
		conn.Close()
		return nil, "", 0, fmt.Errorf("amux client: unexpected handshake frame type %q", probe.Type)
	}
}

// encodeParams marshals params and derives deadline_ms from the ctx deadline.
func encodeParams(ctx context.Context, method string, params any) (json.RawMessage, int64, error) {
	var raw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, 0, fmt.Errorf("amux client: marshal params for %s: %w", method, err)
		}
		raw = b
	}
	var deadlineMS int64
	if d, ok := ctx.Deadline(); ok {
		ms := time.Until(d).Milliseconds()
		if ms <= 0 {
			return nil, 0, context.DeadlineExceeded
		}
		deadlineMS = ms
	}
	return raw, deadlineMS, nil
}

// streamBuffer bounds queued stream frames. When a Recv caller lags this far
// behind, the read loop blocks — which also stalls concurrent calls on this
// Client — so stream consumers must Recv promptly. The daemon additionally
// disconnects pathological laggards server-side (STR-8, ADR-0004).
const streamBuffer = 64

type streamMsg struct {
	ev   v1.Event
	body []byte
}

// Stream is one server-ended event stream. Recv returns each frame's lenient-
// decoded event header and its raw body; heartbeats are included (callers
// filter). Terminal conditions: io.EOF for a clean {"done":true} end, *Error
// for a server-typed failure or a lost connection, ctx.Err() for a local
// cancel.
type Stream struct {
	id   string
	ctx  context.Context
	sess *session
	ch   chan streamMsg

	term    chan struct{} // closed after termErr is set
	termErr error
}

// Recv blocks for the next stream frame. Buffered frames are always drained
// before a terminal condition is reported, so no event is lost at stream end.
func (s *Stream) Recv() (v1.Event, []byte, error) {
	// Prefer buffered frames over every terminal signal.
	select {
	case m := <-s.ch:
		return m.ev, m.body, nil
	default:
	}
	select {
	case m := <-s.ch:
		return m.ev, m.body, nil
	case <-s.term:
		select {
		case m := <-s.ch:
			return m.ev, m.body, nil
		default:
		}
		return v1.Event{}, nil, s.termErr
	case <-s.ctx.Done():
		return v1.Event{}, nil, s.ctx.Err()
	case <-s.sess.done:
		select {
		case m := <-s.ch:
			return m.ev, m.body, nil
		default:
		}
		return v1.Event{}, nil, connLost(s.sess.failure())
	}
}

// finish records the stream's terminal condition exactly once.
func (s *Stream) finish(resp *v1.Response) {
	if resp.Error != nil {
		s.termErr = &Error{Code: resp.Error.Code, Message: resp.Error.Message, Retryable: resp.Error.Retryable, Details: resp.Error.Details}
	} else {
		s.termErr = io.EOF
	}
	close(s.term)
}

// session is one connection epoch: the conn, its demux read loop, the pending
// unary call, and the single active stream. Redial replaces the whole session.
type session struct {
	conn platform.LocalConn

	wmu sync.Mutex // serializes frame writes

	mu      sync.Mutex
	pending map[string]chan *v1.Response
	stream  *Stream

	failOnce sync.Once
	err      error
	done     chan struct{} // closed by fail
}

func newSession(conn platform.LocalConn) *session {
	return &session{
		conn:    conn,
		pending: make(map[string]chan *v1.Response),
		done:    make(chan struct{}),
	}
}

func (s *session) addPending(id string, ch chan *v1.Response) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending[id] = ch
}

func (s *session) removePending(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pending, id)
}

func (s *session) writeHeader(header any, body []byte) error {
	hb, err := json.Marshal(header)
	if err != nil {
		return err
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	return v1.WriteFrame(s.conn, hb, body)
}

// fail records the terminal error and wakes every waiter. Idempotent.
func (s *session) fail(err error) {
	s.failOnce.Do(func() {
		s.err = err
		close(s.done)
	})
}

func (s *session) failure() error {
	select {
	case <-s.done:
		return s.err
	default:
		return nil
	}
}

func (s *session) close() {
	s.fail(errors.New("session closed"))
	s.conn.Close()
}

// readLoop is the demultiplexer: response frames route by ID to the waiting
// call or the active stream's terminal; every other frame (events, raw-body
// stream frames, heartbeats) goes to the active stream. Any read error ends
// the session.
func (s *session) readLoop() {
	for {
		header, body, err := v1.ReadFrame(s.conn)
		if err != nil {
			s.fail(err)
			s.conn.Close()
			return
		}
		var probe struct {
			Type v1.MessageType `json:"type"`
			ID   string         `json:"id"`
		}
		if err := v1.DecodeLenient(header, &probe); err != nil {
			continue // undecodable header: drop (envelopes are lenient, ADR-0003)
		}
		if probe.Type == v1.TypeResponse {
			var resp v1.Response
			if err := v1.DecodeLenient(header, &resp); err != nil {
				continue
			}
			s.mu.Lock()
			ch, isCall := s.pending[resp.ID]
			if isCall {
				delete(s.pending, resp.ID)
			}
			st := s.stream
			s.mu.Unlock()
			if isCall {
				ch <- &resp // buffered; the caller may already have gone away
				continue
			}
			if st != nil && st.id == resp.ID {
				st.finish(&resp)
			}
			continue
		}
		// Event or raw-body frame: deliver to the active stream (none: drop —
		// e.g. a heartbeat racing the stream's terminal response).
		var ev v1.Event
		_ = v1.DecodeLenient(header, &ev)
		s.mu.Lock()
		st := s.stream
		s.mu.Unlock()
		if st == nil {
			continue
		}
		select {
		case st.ch <- streamMsg{ev: ev, body: body}:
		case <-st.term:
		case <-s.done:
			return
		}
	}
}
