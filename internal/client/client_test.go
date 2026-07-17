package client_test

// End-to-end tests against the REAL server from internal/protocol over a real
// unix socket bound by internal/transport/local — the same wiring the daemon
// uses. PeerCredentials note: production Linux wires platform's SO_PEERCRED
// implementation; darwin fails closed by design, so tests inject a fake that
// reports the current UID (the STR-2 gate itself is pinned in
// internal/protocol's tests).

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	v1 "github.com/amux-run/amux/api/v1"
	"github.com/amux-run/amux/internal/client"
	"github.com/amux-run/amux/internal/platform"
	"github.com/amux-run/amux/internal/protocol"
	"github.com/amux-run/amux/internal/transport/local"
)

type fakePeers struct{ uid uint32 }

func (p fakePeers) PeerUID(uintptr) (uint32, error) { return p.uid, nil }

// testSpec builds a short socket path (darwin sun_path limit) with the /var
// symlink resolved so the transport's chain validation passes.
func testSpec(t *testing.T) platform.TransportSpec {
	t.Helper()
	base, err := os.MkdirTemp("", "amux")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(base) })
	canon, err := filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}
	return platform.TransportSpec{
		SocketPath: filepath.Join(canon, "run", "amuxd.sock"),
		OwnerUID:   uint32(os.Getuid()),
	}
}

type backend struct {
	srv  *protocol.Server
	spec platform.TransportSpec
	// hangEntered counts entries into the "hang" handler so tests can wait for
	// a request to be in flight before dropping the daemon.
	hangEntered atomic.Int64
}

// startBackend boots a real protocol server on spec (fresh spec when nil).
func startBackend(t *testing.T, spec *platform.TransportSpec, bootID string, heartbeatMS int64) *backend {
	t.Helper()
	s := testSpec(t)
	if spec != nil {
		s = *spec
	}
	b := &backend{spec: s}
	b.srv = protocol.NewServer(protocol.Options{
		BootID:      bootID,
		ServerTag:   "amuxd/test",
		Peers:       fakePeers{uid: uint32(os.Getuid())},
		HeartbeatMS: heartbeatMS,
	})

	b.srv.HandleFunc("echo", func(ctx context.Context, req protocol.Request) (json.RawMessage, *v1.ErrorBody) {
		var p struct {
			Msg string `json:"msg"`
		}
		if err := v1.DecodeStrict(req.Params, &p); err != nil {
			return nil, &v1.ErrorBody{Code: v1.ErrInvalidArgument, Message: err.Error()}
		}
		return json.RawMessage(`{"echo":` + jsonString(p.Msg) + `}`), nil
	})
	b.srv.HandleFunc("extra", func(ctx context.Context, req protocol.Request) (json.RawMessage, *v1.ErrorBody) {
		return json.RawMessage(`{"known":1,"surprise":true}`), nil
	})
	b.srv.HandleFunc("fail", func(ctx context.Context, req protocol.Request) (json.RawMessage, *v1.ErrorBody) {
		return nil, &v1.ErrorBody{Code: v1.ErrNotFound, Message: "nothing here", Retryable: false}
	})
	b.srv.HandleFunc("fail.details", func(ctx context.Context, req protocol.Request) (json.RawMessage, *v1.ErrorBody) {
		return nil, &v1.ErrorBody{
			Code: v1.ErrReplayGap, Message: "oldest retained 42 (humans only)",
			Details: json.RawMessage(`{"from_seq":1,"oldest_retained":42,"latest_seq":99}`),
		}
	})
	b.srv.HandleFunc("hang", func(ctx context.Context, req protocol.Request) (json.RawMessage, *v1.ErrorBody) {
		b.hangEntered.Add(1)
		<-ctx.Done()
		return nil, nil
	})
	b.srv.StreamFunc("stream.demo", func(ctx context.Context, req protocol.Request, send protocol.SendFunc) *v1.ErrorBody {
		ev := v1.Event{Type: v1.TypeEvent, BootID: bootID, Session: "ses-1", Seq: 1, Event: "tick"}
		if err := send(ev, nil); err != nil {
			return nil
		}
		raw := map[string]any{"type": "event", "event": "raw_output", "session": "ses-1", "seq": 2}
		if err := send(raw, []byte{0x00, 0xff, 'x'}); err != nil {
			return nil
		}
		return nil
	})
	b.srv.StreamFunc("stream.fail", func(ctx context.Context, req protocol.Request, send protocol.SendFunc) *v1.ErrorBody {
		return &v1.ErrorBody{Code: v1.ErrEventGap, Message: "gap", Retryable: true}
	})
	b.srv.StreamFunc("stream.hold", func(ctx context.Context, req protocol.Request, send protocol.SendFunc) *v1.ErrorBody {
		<-ctx.Done()
		return nil
	})

	ln, err := local.New().Listen(s)
	if err != nil {
		t.Fatal(err)
	}
	go b.srv.Serve(ln)
	t.Cleanup(func() { b.srv.Close() })
	return b
}

func jsonString(s string) string { return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"` }

func dialClient(t *testing.T, b *backend) *client.Client {
	t.Helper()
	c, err := client.Dial(context.Background(), local.New(), b.spec, "amux/test-client")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// TestCallRoundtrip pins the happy path: params marshal, server-side strict
// param decode, client-side strict result decode, BootID exposure.
func TestCallRoundtrip(t *testing.T) {
	b := startBackend(t, nil, "boot-1", 0)
	c := dialClient(t, b)

	if c.BootID() != "boot-1" {
		t.Fatalf("BootID = %q", c.BootID())
	}
	var result struct {
		Echo string `json:"echo"`
	}
	if err := c.Call(context.Background(), "echo", map[string]string{"msg": "hello"}, &result); err != nil {
		t.Fatal(err)
	}
	if result.Echo != "hello" {
		t.Fatalf("result = %+v", result)
	}
	// nil result is allowed: the caller may not care about the payload.
	if err := c.Call(context.Background(), "echo", map[string]string{"msg": "x"}, nil); err != nil {
		t.Fatal(err)
	}
}

// TestStrictResultDecode pins the client-side strict boundary: a result with
// fields the caller's struct does not know is an error, not silence (STR-6).
func TestStrictResultDecode(t *testing.T) {
	b := startBackend(t, nil, "boot-1", 0)
	c := dialClient(t, b)

	var result struct {
		Known int `json:"known"`
	}
	err := c.Call(context.Background(), "extra", nil, &result)
	if err == nil {
		t.Fatal("unknown result field must fail the strict decode")
	}
}

// TestTypedErrorPropagation pins the *Error surface: errors.As, CodeOf, and
// the retryable bit all reflect the wire error body.
func TestTypedErrorPropagation(t *testing.T) {
	b := startBackend(t, nil, "boot-1", 0)
	c := dialClient(t, b)

	// Server-side strict params rejection.
	err := c.Call(context.Background(), "echo", map[string]any{"msg": "x", "bogus": 1}, nil)
	if client.CodeOf(err) != v1.ErrInvalidArgument {
		t.Fatalf("CodeOf(%v) = %q, want invalid_argument", err, client.CodeOf(err))
	}

	err = c.Call(context.Background(), "fail", nil, nil)
	var typed *client.Error
	if !errors.As(err, &typed) {
		t.Fatalf("error is not *client.Error: %v", err)
	}
	if typed.Code != v1.ErrNotFound || typed.Retryable {
		t.Fatalf("typed = %+v", typed)
	}
	if client.CodeOf(err) != v1.ErrNotFound {
		t.Fatalf("CodeOf = %q", client.CodeOf(err))
	}
	if client.CodeOf(errors.New("plain")) != "" {
		t.Fatal("CodeOf(non-protocol error) must be empty")
	}
}

// TestErrorDetailsPropagation pins the structured-details contract: an
// ErrorBody's details survive into the typed *client.Error verbatim, so
// automation (e.g. a replay_gap consumer) branches on structured data and
// never parses the human message.
func TestErrorDetailsPropagation(t *testing.T) {
	b := startBackend(t, nil, "boot-1", 0)
	c := dialClient(t, b)

	err := c.Call(context.Background(), "fail.details", nil, nil)
	var typed *client.Error
	if !errors.As(err, &typed) {
		t.Fatalf("error is not *client.Error: %v", err)
	}
	if typed.Code != v1.ErrReplayGap {
		t.Fatalf("code = %q, want replay_gap", typed.Code)
	}
	var gap struct {
		FromSeq        uint64 `json:"from_seq"`
		OldestRetained uint64 `json:"oldest_retained"`
		LatestSeq      uint64 `json:"latest_seq"`
	}
	if len(typed.Details) == 0 {
		t.Fatal("typed error dropped the wire details")
	}
	if err := json.Unmarshal(typed.Details, &gap); err != nil {
		t.Fatalf("details do not decode: %v (%s)", err, typed.Details)
	}
	if gap.OldestRetained != 42 || gap.LatestSeq != 99 || gap.FromSeq != 1 {
		t.Fatalf("details = %+v", gap)
	}
}

// TestCallDeadline pins deadline handling: the ctx deadline is forwarded as
// deadline_ms and the call ends promptly — either the server's typed
// resource_exhausted arrives first or the local ctx trips; both are correct.
func TestCallDeadline(t *testing.T) {
	b := startBackend(t, nil, "boot-1", 0)
	c := dialClient(t, b)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := c.Call(ctx, "hang", nil, nil)
	if err == nil {
		t.Fatal("deadline call must fail")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("deadline call took %v", time.Since(start))
	}
	if !errors.Is(err, context.DeadlineExceeded) && client.CodeOf(err) != v1.ErrResourceExhausted {
		t.Fatalf("deadline error = %v", err)
	}
}

// TestStreamRecv pins the stream surface: typed events, raw-body frames, and
// the io.EOF terminal for a clean {"done":true} end.
func TestStreamRecv(t *testing.T) {
	b := startBackend(t, nil, "boot-1", 0)
	c := dialClient(t, b)

	st, err := c.Stream(context.Background(), "stream.demo", nil)
	if err != nil {
		t.Fatal(err)
	}
	ev, body, err := st.Recv()
	if err != nil || ev.Event != "tick" || ev.Seq != 1 || body != nil {
		t.Fatalf("first frame = %+v body=%v err=%v", ev, body, err)
	}
	ev, body, err = st.Recv()
	if err != nil || ev.Event != "raw_output" {
		t.Fatalf("raw frame = %+v err=%v", ev, err)
	}
	if want := []byte{0x00, 0xff, 'x'}; string(body) != string(want) {
		t.Fatalf("raw body = %v, want %v", body, want)
	}
	if _, _, err := st.Recv(); err != io.EOF {
		t.Fatalf("terminal = %v, want io.EOF", err)
	}

	// The stream ended, so a new one may start; a server-typed stream failure
	// surfaces as the typed error.
	st2, err := c.Stream(context.Background(), "stream.fail", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = st2.Recv()
	if client.CodeOf(err) != v1.ErrEventGap {
		t.Fatalf("failing stream terminal = %v", err)
	}
}

// TestStreamHeartbeat pins heartbeat surfacing: heartbeats reach Recv callers
// (they filter), interleaved with a concurrent Call on the same client.
func TestStreamHeartbeat(t *testing.T) {
	b := startBackend(t, nil, "boot-1", 40) // 40ms heartbeat, real clock
	c := dialClient(t, b)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st, err := c.Stream(ctx, "stream.hold", nil)
	if err != nil {
		t.Fatal(err)
	}

	// A unary call while the stream is live: the demux must route both.
	if err := c.Call(context.Background(), "echo", map[string]string{"msg": "mid"}, nil); err != nil {
		t.Fatalf("call during stream: %v", err)
	}

	deadline := time.After(10 * time.Second)
	for {
		type recvResult struct {
			ev  v1.Event
			err error
		}
		got := make(chan recvResult, 1)
		go func() {
			ev, _, err := st.Recv()
			got <- recvResult{ev, err}
		}()
		select {
		case r := <-got:
			if r.err != nil {
				t.Fatalf("Recv during heartbeat wait: %v", r.err)
			}
			if r.ev.Event == "heartbeat" {
				return // surfaced as promised
			}
		case <-deadline:
			t.Fatal("no heartbeat surfaced")
		}
	}
}

// TestSecondStreamRefused pins the one-stream-per-client rule.
func TestSecondStreamRefused(t *testing.T) {
	b := startBackend(t, nil, "boot-1", 0)
	c := dialClient(t, b)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := c.Stream(ctx, "stream.hold", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Stream(ctx, "stream.hold", nil); !errors.Is(err, client.ErrStreamActive) {
		t.Fatalf("second stream = %v, want ErrStreamActive", err)
	}
}

// TestRedialBootChange pins the restart contract: Redial reports
// ErrBootChanged when the daemon incarnation changed, and the client is live
// afterwards; a same-boot Redial returns nil.
func TestRedialBootChange(t *testing.T) {
	b1 := startBackend(t, nil, "boot-1", 0)
	c := dialClient(t, b1)

	// Same daemon: Redial is clean.
	if err := c.Redial(context.Background()); err != nil {
		t.Fatalf("same-boot Redial = %v", err)
	}

	// Restart the daemon on the same socket with a new boot id.
	if err := b1.srv.Close(); err != nil {
		t.Fatal(err)
	}
	startBackend(t, &b1.spec, "boot-2", 0)

	err := c.Redial(context.Background())
	if !errors.Is(err, client.ErrBootChanged) {
		t.Fatalf("post-restart Redial = %v, want ErrBootChanged", err)
	}
	if c.BootID() != "boot-2" {
		t.Fatalf("BootID after redial = %q", c.BootID())
	}
	// The client must be usable despite ErrBootChanged.
	if err := c.Call(context.Background(), "echo", map[string]string{"msg": "back"}, nil); err != nil {
		t.Fatalf("call after redial = %v", err)
	}
}

// TestConnDropMidCall pins the no-auto-reconnect rule: a broken connection
// fails the in-flight call with a typed RETRYABLE error, and only an explicit
// Redial restores service.
func TestConnDropMidCall(t *testing.T) {
	b := startBackend(t, nil, "boot-1", 0)
	c := dialClient(t, b)

	callErr := make(chan error, 1)
	go func() { callErr <- c.Call(context.Background(), "hang", nil, nil) }()

	// Wait until the request is in the handler, then kill the daemon.
	waitFor(t, func() bool { return b.hangEntered.Load() > 0 })
	if err := b.srv.Close(); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-callErr:
		var typed *client.Error
		if !errors.As(err, &typed) || !typed.Retryable {
			t.Fatalf("mid-call drop error = %v, want typed retryable *client.Error", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("in-flight call never failed after connection drop")
	}

	// No automatic reconnect: the next call fails retryable too.
	err := c.Call(context.Background(), "echo", map[string]string{"msg": "x"}, nil)
	var typed *client.Error
	if !errors.As(err, &typed) || !typed.Retryable {
		t.Fatalf("post-drop call = %v, want typed retryable error", err)
	}

	// Explicit Redial against a fresh daemon restores service.
	startBackend(t, &b.spec, "boot-2", 0)
	if err := c.Redial(context.Background()); !errors.Is(err, client.ErrBootChanged) {
		t.Fatalf("Redial = %v, want ErrBootChanged", err)
	}
	if err := c.Call(context.Background(), "echo", map[string]string{"msg": "x"}, nil); err != nil {
		t.Fatalf("call after Redial = %v", err)
	}
}

// TestVersionRejectionSurfacesTyped pins the Dial-time negotiation error
// surface indirectly: closing before hello completes must not panic, and the
// typed error path is proven by dialing a server that always rejects (covered
// by the protocol tests); here we pin ctx-bounded Dial against a dead socket.
func TestDialFailsOnDeadEndpoint(t *testing.T) {
	spec := testSpec(t)
	_, err := client.Dial(context.Background(), local.New(), spec, "amux/test-client")
	if err == nil {
		t.Fatal("Dial against a nonexistent socket must fail")
	}
}

// TestClosedClient pins the post-Close surface.
func TestClosedClient(t *testing.T) {
	b := startBackend(t, nil, "boot-1", 0)
	c := dialClient(t, b)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second Close = %v", err)
	}
	if err := c.Call(context.Background(), "echo", nil, nil); !errors.Is(err, client.ErrClosed) {
		t.Fatalf("Call after Close = %v, want ErrClosed", err)
	}
	if err := c.Redial(context.Background()); !errors.Is(err, client.ErrClosed) {
		t.Fatalf("Redial after Close = %v, want ErrClosed", err)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condition never became true")
}
