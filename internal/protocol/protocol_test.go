package protocol_test

// Full in-process roundtrips over a real unix socket via internal/transport/
// local. PeerCredentials note: production Linux wires platform's real
// SO_PEERCRED implementation (platform.NewLinuxPeerCredentials); on darwin
// that constructor fails closed by design, so these tests inject a fake that
// returns the current UID — the STR-2 gate logic itself (mismatch/error =>
// zero frames) is exercised with hostile fakes below, and the real second-UID
// rejection is a Linux CI check (integration-second-uid).

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	v1 "github.com/amux-run/amux/api/v1"
	"github.com/amux-run/amux/internal/platform"
	"github.com/amux-run/amux/internal/protocol"
	"github.com/amux-run/amux/internal/transport/local"
)

type fakePeers struct {
	uid uint32
	err error
}

func (p fakePeers) PeerUID(uintptr) (uint32, error) { return p.uid, p.err }

func selfUID() uint32 { return uint32(os.Getuid()) }

// testSpec builds a short socket path (darwin sun_path limit) with the /var
// symlink resolved so the transport's STR-3 chain validation passes.
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
		OwnerUID:   selfUID(),
	}
}

// startServer boots a protocol.Server on a real unix socket. mutate may adjust
// Options before NewServer.
func startServer(t *testing.T, mutate func(*protocol.Options)) (platform.TransportSpec, *protocol.Server) {
	t.Helper()
	opts := protocol.Options{
		BootID:    "boot-test",
		ServerTag: "amuxd/test",
		Peers:     fakePeers{uid: selfUID()},
	}
	if mutate != nil {
		mutate(&opts)
	}
	srv := protocol.NewServer(opts)
	registerTestHandlers(t, srv)
	drain(testPeerCh)
	drain(slowSawCancel)
	drain(floodErrCh)
	spec := testSpec(t)
	ln, err := local.New().Listen(spec)
	if err != nil {
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- srv.Serve(ln) }()
	t.Cleanup(func() {
		srv.Close()
		if err := <-serveDone; err != nil {
			t.Errorf("Serve returned %v after Close, want nil", err)
		}
	})
	return spec, srv
}

// drain empties a shared signal channel so one test's leftovers can never
// satisfy another test's expectation.
func drain[T any](ch chan T) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

// testPeerCh receives the Peer observed by the echo handler.
var testPeerCh = make(chan protocol.Peer, 16)

// slowSawCancel receives a token when the slow handler's ctx is done.
var slowSawCancel = make(chan struct{}, 16)

// floodErrCh receives the terminal send error of stream.flood.
var floodErrCh = make(chan error, 16)

func registerTestHandlers(t *testing.T, srv *protocol.Server) {
	t.Helper()
	srv.HandleFunc("echo", func(ctx context.Context, req protocol.Request) (json.RawMessage, *v1.ErrorBody) {
		select {
		case testPeerCh <- req.Peer:
		default:
		}
		var p struct {
			Msg string `json:"msg"`
		}
		// STR-6: params are a durable boundary; unknown fields are rejected.
		if err := v1.DecodeStrict(req.Params, &p); err != nil {
			return nil, &v1.ErrorBody{Code: v1.ErrInvalidArgument, Message: err.Error()}
		}
		return json.RawMessage(fmt.Sprintf(`{"echo":%q}`, p.Msg)), nil
	})
	srv.HandleFunc("slow", func(ctx context.Context, req protocol.Request) (json.RawMessage, *v1.ErrorBody) {
		<-ctx.Done()
		select {
		case slowSawCancel <- struct{}{}:
		default:
		}
		return nil, nil
	})
	srv.HandleFunc("boom", func(ctx context.Context, req protocol.Request) (json.RawMessage, *v1.ErrorBody) {
		panic("kaboom")
	})
	srv.StreamFunc("stream.count", func(ctx context.Context, req protocol.Request, send protocol.SendFunc) *v1.ErrorBody {
		for i := 1; i <= 50; i++ {
			ev := v1.Event{Type: v1.TypeEvent, BootID: "boot-test", Session: "ses-1", Seq: uint64(i), Event: "tick"}
			if err := send(ev, nil); err != nil {
				return nil
			}
			time.Sleep(200 * time.Microsecond) // let unary requests interleave
		}
		return nil
	})
	srv.StreamFunc("stream.hold", func(ctx context.Context, req protocol.Request, send protocol.SendFunc) *v1.ErrorBody {
		<-ctx.Done()
		return nil
	})
	srv.StreamFunc("stream.raw", func(ctx context.Context, req protocol.Request, send protocol.SendFunc) *v1.ErrorBody {
		// A raw-body frame whose header the StreamFunc supplies.
		header := map[string]any{"type": "event", "event": "raw_output", "session": "ses-1", "seq": 1}
		if err := send(header, []byte{0x1b, '[', 'm', 0x00, 0xff}); err != nil {
			return nil
		}
		return nil
	})
	srv.StreamFunc("stream.fail", func(ctx context.Context, req protocol.Request, send protocol.SendFunc) *v1.ErrorBody {
		return &v1.ErrorBody{Code: v1.ErrConflict, Message: "stream failed"}
	})
	srv.StreamFunc("stream.flood", func(ctx context.Context, req protocol.Request, send protocol.SendFunc) *v1.ErrorBody {
		body := make([]byte, 64<<10)
		for {
			if err := send(v1.Event{Type: v1.TypeEvent, Event: "flood"}, body); err != nil {
				floodErrCh <- err
				return nil
			}
		}
	})
}

// wire is a raw protocol speaker: it writes frames (serialized) and reads them
// on a dedicated goroutine so tests can also assert "no frame arrives".
type frameRec struct {
	header []byte
	body   []byte
	err    error
}

type wire struct {
	t      *testing.T
	conn   platform.LocalConn
	frames chan frameRec
	wmu    sync.Mutex
}

func dialWire(t *testing.T, spec platform.TransportSpec) *wire {
	t.Helper()
	conn, err := local.New().Dial(spec)
	if err != nil {
		t.Fatal(err)
	}
	w := &wire{t: t, conn: conn, frames: make(chan frameRec, 1024)}
	go func() {
		for {
			h, b, err := v1.ReadFrame(conn)
			w.frames <- frameRec{h, b, err}
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() { conn.Close() })
	return w
}

func (w *wire) writeHeader(header any, body []byte) {
	w.t.Helper()
	hb, err := json.Marshal(header)
	if err != nil {
		w.t.Fatal(err)
	}
	w.wmu.Lock()
	defer w.wmu.Unlock()
	if err := v1.WriteFrame(w.conn, hb, body); err != nil {
		w.t.Fatalf("write frame: %v", err)
	}
}

// tryWriteHeader writes a frame but tolerates a write failure: used when the
// server may already have closed the connection (the peer gate closes before
// the first byte, so the client's hello write races the close).
func (w *wire) tryWriteHeader(header any) {
	w.t.Helper()
	hb, err := json.Marshal(header)
	if err != nil {
		w.t.Fatal(err)
	}
	w.wmu.Lock()
	defer w.wmu.Unlock()
	_ = v1.WriteFrame(w.conn, hb, nil)
}

func (w *wire) next(timeout time.Duration) frameRec {
	w.t.Helper()
	select {
	case rec := <-w.frames:
		return rec
	case <-time.After(timeout):
		w.t.Fatalf("no frame within %v", timeout)
		return frameRec{}
	}
}

func (w *wire) expectNone(d time.Duration) {
	w.t.Helper()
	select {
	case rec := <-w.frames:
		w.t.Fatalf("unexpected frame: header=%s err=%v", rec.header, rec.err)
	case <-time.After(d):
	}
}

// hello performs the negotiation and returns the decoded welcome.
func (w *wire) hello(t *testing.T, major, minor int) v1.Welcome {
	t.Helper()
	w.writeHeader(v1.Hello{Type: v1.TypeHello, Major: major, Minor: minor, Client: "amux/test"}, nil)
	rec := w.next(5 * time.Second)
	if rec.err != nil {
		t.Fatalf("handshake read: %v", rec.err)
	}
	var welcome v1.Welcome
	if err := v1.DecodeLenient(rec.header, &welcome); err != nil || welcome.Type != v1.TypeWelcome {
		t.Fatalf("expected welcome, got %s (err %v)", rec.header, err)
	}
	return welcome
}

func (w *wire) request(id, method string, deadlineMS int64, params string) {
	w.t.Helper()
	var raw json.RawMessage
	if params != "" {
		raw = json.RawMessage(params)
	}
	w.writeHeader(v1.Request{Type: v1.TypeRequest, ID: id, Method: method, DeadlineMS: deadlineMS, Params: raw}, nil)
}

func decodeResponse(t *testing.T, rec frameRec) v1.Response {
	t.Helper()
	if rec.err != nil {
		t.Fatalf("read response: %v", rec.err)
	}
	var resp v1.Response
	if err := v1.DecodeLenient(rec.header, &resp); err != nil || resp.Type != v1.TypeResponse {
		t.Fatalf("expected response, got %s (err %v)", rec.header, err)
	}
	return resp
}

// TestRoundtrip is the full happy path: negotiate, strict-decoded params,
// result-bearing response, verified Peer identity on the handler side.
func TestRoundtrip(t *testing.T) {
	spec, _ := startServer(t, nil)
	w := dialWire(t, spec)
	welcome := w.hello(t, v1.Major, v1.Minor)
	if welcome.Major != v1.Major || welcome.Minor != v1.Minor || welcome.BootID != "boot-test" {
		t.Fatalf("welcome = %+v", welcome)
	}

	w.request("req-1", "echo", 0, `{"msg":"hi"}`)
	resp := decodeResponse(t, w.next(5*time.Second))
	if resp.ID != "req-1" || resp.Error != nil {
		t.Fatalf("response = %+v", resp)
	}
	var result struct {
		Echo string `json:"echo"`
	}
	if err := v1.DecodeStrict(resp.Result, &result); err != nil || result.Echo != "hi" {
		t.Fatalf("result = %s (%v)", resp.Result, err)
	}
	select {
	case peer := <-testPeerCh:
		if peer.UID != selfUID() || peer.ConnID == "" {
			t.Fatalf("handler peer = %+v", peer)
		}
	case <-time.After(time.Second):
		t.Fatal("handler never observed a peer")
	}

	// STR-6: unknown param field is rejected by the handler's strict decode.
	w.request("req-2", "echo", 0, `{"msg":"hi","bogus":1}`)
	resp = decodeResponse(t, w.next(5*time.Second))
	if resp.Error == nil || resp.Error.Code != v1.ErrInvalidArgument {
		t.Fatalf("unknown param field response = %+v", resp)
	}
}

// TestVersionSkew pins the negotiation gate: a future major is rejected with
// unsupported_version BEFORE any request is accepted, and a newer client minor
// down-negotiates to the server's minor (welcome.Minor == min).
func TestVersionSkew(t *testing.T) {
	spec, _ := startServer(t, nil)

	t.Run("major rejected", func(t *testing.T) {
		w := dialWire(t, spec)
		w.writeHeader(v1.Hello{Type: v1.TypeHello, Major: v1.Major + 1, Minor: 0, Client: "amux/test"}, nil)
		resp := decodeResponse(t, w.next(5*time.Second))
		if resp.Error == nil || resp.Error.Code != v1.ErrUnsupportedVersion {
			t.Fatalf("response = %+v", resp)
		}
		if rec := w.next(5 * time.Second); rec.err == nil {
			t.Fatalf("connection must be closed after version rejection, got frame %s", rec.header)
		}
	})

	t.Run("minor down-negotiation", func(t *testing.T) {
		w := dialWire(t, spec)
		welcome := w.hello(t, v1.Major, v1.Minor+7)
		if welcome.Minor != v1.Minor {
			t.Fatalf("welcome.Minor = %d, want server minor %d (min)", welcome.Minor, v1.Minor)
		}
	})
}

// TestPeerGate pins STR-2: a UID mismatch or a credential error closes the
// connection with ZERO frames served — not even the hello is answered.
func TestPeerGate(t *testing.T) {
	cases := map[string]fakePeers{
		"uid mismatch":     {uid: selfUID() + 1},
		"credential error": {err: errors.New("peercred unavailable")},
	}
	for name, peers := range cases {
		t.Run(name, func(t *testing.T) {
			spec, _ := startServer(t, func(o *protocol.Options) { o.Peers = peers })
			w := dialWire(t, spec)
			// The gate closes the conn before reading anything, so this write
			// may fail with EPIPE — that failure is itself the expected outcome.
			w.tryWriteHeader(v1.Hello{Type: v1.TypeHello, Major: v1.Major, Minor: v1.Minor, Client: "amux/test"})
			rec := w.next(5 * time.Second)
			if rec.err == nil {
				t.Fatalf("peer-gated connection served a frame: %s", rec.header)
			}
		})
	}
}

// TestMalformedStreamClosesConnection pins the fail-closed framing path:
// garbage bytes after negotiation tear the connection down without a panic and
// without wedging the server.
func TestMalformedStreamClosesConnection(t *testing.T) {
	spec, _ := startServer(t, nil)
	w := dialWire(t, spec)
	w.hello(t, v1.Major, v1.Minor)

	// A hostile length prefix (0xdeadbeef bytes) trips the STR-5 bound before
	// allocation; the server answers best-effort and closes.
	if _, err := w.conn.Write([]byte{0xde, 0xad, 0xbe, 0xef, 0x00, 0x01}); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case rec := <-w.frames:
			if rec.err != nil {
				goto closed // connection torn down, no panic, no hang
			}
			resp := decodeResponse(t, rec)
			if resp.Error == nil || resp.Error.Code != v1.ErrResourceExhausted {
				t.Fatalf("pre-close frame = %+v", resp)
			}
		case <-deadline:
			t.Fatal("connection was not closed after malformed frame")
		}
	}
closed:
	// The server must still accept fresh connections.
	w2 := dialWire(t, spec)
	w2.hello(t, v1.Major, v1.Minor)
	w2.request("req-1", "echo", 0, `{"msg":"still alive"}`)
	if resp := decodeResponse(t, w2.next(5*time.Second)); resp.Error != nil {
		t.Fatalf("post-recovery echo = %+v", resp)
	}
}

// TestOversizedBodyPrefix pins STR-5 on the body bound: a body length prefix
// beyond MaxBodyBytes yields a typed resource_exhausted and a closed conn,
// with nothing allocated for the phantom body.
func TestOversizedBodyPrefix(t *testing.T) {
	spec, _ := startServer(t, nil)
	w := dialWire(t, spec)
	w.hello(t, v1.Major, v1.Minor)

	header := []byte(`{"type":"request","id":"req-1","method":"echo"}`)
	var buf []byte
	var prefix [4]byte
	binary.BigEndian.PutUint32(prefix[:], uint32(len(header)))
	buf = append(buf, prefix[:]...)
	buf = append(buf, header...)
	binary.BigEndian.PutUint32(prefix[:], uint32(v1.MaxBodyBytes+1))
	buf = append(buf, prefix[:]...)
	if _, err := w.conn.Write(buf); err != nil {
		t.Fatal(err)
	}

	resp := decodeResponse(t, w.next(5*time.Second))
	if resp.Error == nil || resp.Error.Code != v1.ErrResourceExhausted {
		t.Fatalf("response = %+v", resp)
	}
	if rec := w.next(5 * time.Second); rec.err == nil {
		t.Fatalf("connection must close after oversize prefix, got %s", rec.header)
	}
}

// TestPartialFrameDisconnect pins clean teardown when a peer vanishes mid-body.
func TestPartialFrameDisconnect(t *testing.T) {
	spec, _ := startServer(t, nil)
	w := dialWire(t, spec)
	w.hello(t, v1.Major, v1.Minor)

	// Promise a 64-byte header, deliver 10 bytes, disconnect.
	var prefix [4]byte
	binary.BigEndian.PutUint32(prefix[:], 64)
	if _, err := w.conn.Write(append(prefix[:], []byte("0123456789")...)); err != nil {
		t.Fatal(err)
	}
	w.conn.Close()

	// The server must survive and keep serving.
	w2 := dialWire(t, spec)
	w2.hello(t, v1.Major, v1.Minor)
	w2.request("req-1", "echo", 0, `{"msg":"ok"}`)
	if resp := decodeResponse(t, w2.next(5*time.Second)); resp.Error != nil {
		t.Fatalf("post-disconnect echo = %+v", resp)
	}
}

// TestDeadlineExpiry pins deadline_ms: the handler ctx is done at the deadline
// and the client receives a typed, retryable error at that moment.
func TestDeadlineExpiry(t *testing.T) {
	spec, _ := startServer(t, nil)
	w := dialWire(t, spec)
	w.hello(t, v1.Major, v1.Minor)

	start := time.Now()
	w.request("req-1", "slow", 50, "")
	resp := decodeResponse(t, w.next(5*time.Second))
	if resp.Error == nil || resp.Error.Code != v1.ErrResourceExhausted || !resp.Error.Retryable {
		t.Fatalf("deadline response = %+v", resp)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("deadline response took %v", elapsed)
	}
	select {
	case <-slowSawCancel:
	case <-time.After(2 * time.Second):
		t.Fatal("handler ctx was never cancelled")
	}
}

// TestUnknownMethod pins the not_found mapping.
func TestUnknownMethod(t *testing.T) {
	spec, _ := startServer(t, nil)
	w := dialWire(t, spec)
	w.hello(t, v1.Major, v1.Minor)
	w.request("req-1", "no.such.method", 0, "")
	resp := decodeResponse(t, w.next(5*time.Second))
	if resp.Error == nil || resp.Error.Code != v1.ErrNotFound {
		t.Fatalf("response = %+v", resp)
	}
}

// TestPanicRecovery pins fail-closed panic handling: internal error response,
// connection stays up.
func TestPanicRecovery(t *testing.T) {
	spec, _ := startServer(t, nil)
	w := dialWire(t, spec)
	w.hello(t, v1.Major, v1.Minor)

	w.request("req-1", "boom", 0, "")
	resp := decodeResponse(t, w.next(5*time.Second))
	if resp.Error == nil || resp.Error.Code != v1.ErrInternal {
		t.Fatalf("panic response = %+v", resp)
	}

	w.request("req-2", "echo", 0, `{"msg":"still up"}`)
	resp = decodeResponse(t, w.next(5*time.Second))
	if resp.Error != nil {
		t.Fatalf("post-panic echo = %+v", resp)
	}
}

// TestNonRequestFrameKeepsConnection pins the envelope-level error path: a
// non-request type gets invalid_argument and the connection stays usable.
func TestNonRequestFrameKeepsConnection(t *testing.T) {
	spec, _ := startServer(t, nil)
	w := dialWire(t, spec)
	w.hello(t, v1.Major, v1.Minor)

	w.writeHeader(v1.Event{Type: v1.TypeEvent, Event: "rogue"}, nil)
	resp := decodeResponse(t, w.next(5*time.Second))
	if resp.Error == nil || resp.Error.Code != v1.ErrInvalidArgument {
		t.Fatalf("response = %+v", resp)
	}

	w.request("req-1", "echo", 0, `{"msg":"ok"}`)
	resp = decodeResponse(t, w.next(5*time.Second))
	if resp.Error != nil {
		t.Fatalf("follow-up echo = %+v", resp)
	}
}

// TestStreamInterleaving runs a stream and concurrent unary requests on one
// connection: every frame must arrive intact (the serialized writer is what
// this pins; run under -race for the data-race gate).
func TestStreamInterleaving(t *testing.T) {
	spec, _ := startServer(t, nil)
	w := dialWire(t, spec)
	w.hello(t, v1.Major, v1.Minor)

	w.request("stream-1", "stream.count", 0, "")
	go func() {
		for i := 0; i < 20; i++ {
			w.request(fmt.Sprintf("ping-%d", i), "echo", 0, `{"msg":"ping"}`)
		}
	}()

	ticks, pings, streamDone := 0, 0, false
	deadline := time.After(15 * time.Second)
	for ticks < 50 || pings < 20 || !streamDone {
		var rec frameRec
		select {
		case rec = <-w.frames:
		case <-deadline:
			t.Fatalf("timeout: ticks=%d pings=%d streamDone=%v", ticks, pings, streamDone)
		}
		if rec.err != nil {
			t.Fatalf("read: %v (ticks=%d pings=%d)", rec.err, ticks, pings)
		}
		var probe struct {
			Type  v1.MessageType `json:"type"`
			ID    string         `json:"id"`
			Event string         `json:"event"`
		}
		if err := v1.DecodeLenient(rec.header, &probe); err != nil {
			t.Fatalf("undecodable frame %q: %v", rec.header, err)
		}
		switch {
		case probe.Type == v1.TypeEvent && probe.Event == "tick":
			ticks++
		case probe.Type == v1.TypeResponse && probe.ID == "stream-1":
			var resp v1.Response
			if err := v1.DecodeLenient(rec.header, &resp); err != nil || resp.Error != nil {
				t.Fatalf("stream final response = %s (%v)", rec.header, err)
			}
			var done struct {
				Done bool `json:"done"`
			}
			if err := v1.DecodeStrict(resp.Result, &done); err != nil || !done.Done {
				t.Fatalf("stream final result = %s", resp.Result)
			}
			streamDone = true
		case probe.Type == v1.TypeResponse:
			pings++
		default:
			t.Fatalf("unexpected frame: %s", rec.header)
		}
	}
}

// TestStreamRawBodyAndErrorEnd pins raw-body stream frames and the error-
// carrying final response.
func TestStreamRawBodyAndErrorEnd(t *testing.T) {
	spec, _ := startServer(t, nil)
	w := dialWire(t, spec)
	w.hello(t, v1.Major, v1.Minor)

	w.request("s-raw", "stream.raw", 0, "")
	rec := w.next(5 * time.Second)
	var ev v1.Event
	if err := v1.DecodeLenient(rec.header, &ev); err != nil || ev.Event != "raw_output" {
		t.Fatalf("raw frame header = %s (%v)", rec.header, err)
	}
	if want := []byte{0x1b, '[', 'm', 0x00, 0xff}; string(rec.body) != string(want) {
		t.Fatalf("raw body = %v, want %v", rec.body, want)
	}
	resp := decodeResponse(t, w.next(5*time.Second))
	if resp.ID != "s-raw" || resp.Error != nil {
		t.Fatalf("raw stream final = %+v", resp)
	}

	w.request("s-fail", "stream.fail", 0, "")
	resp = decodeResponse(t, w.next(5*time.Second))
	if resp.ID != "s-fail" || resp.Error == nil || resp.Error.Code != v1.ErrConflict {
		t.Fatalf("failing stream final = %+v", resp)
	}
}

// TestHeartbeat drives heartbeat emission deterministically with a FakeClock:
// no beat while the clock is frozen, one beat per HeartbeatMS advance.
func TestHeartbeat(t *testing.T) {
	clock := platform.NewFakeClock(0)
	spec, _ := startServer(t, func(o *protocol.Options) {
		o.Clock = clock
		o.HeartbeatMS = 1000
	})
	w := dialWire(t, spec)
	w.hello(t, v1.Major, v1.Minor)
	w.request("s-hold", "stream.hold", 0, "")

	// Clock frozen: the real-time poller runs but must emit nothing.
	w.expectNone(150 * time.Millisecond)

	expectBeat := func(wantMS int64) {
		t.Helper()
		rec := w.next(5 * time.Second)
		var ev v1.Event
		if err := v1.DecodeLenient(rec.header, &ev); err != nil {
			t.Fatalf("heartbeat decode: %s (%v)", rec.header, err)
		}
		if ev.Type != v1.TypeEvent || ev.Event != "heartbeat" || ev.Session != "" || ev.Seq != 0 {
			t.Fatalf("heartbeat frame = %+v", ev)
		}
		if ev.BootID != "boot-test" || ev.TimeMS != wantMS {
			t.Fatalf("heartbeat identity = %+v, want time %d", ev, wantMS)
		}
	}

	clock.Advance(time.Second)
	expectBeat(1000)
	clock.Advance(time.Second)
	expectBeat(2000)

	// Exactly one beat per interval: nothing further while the clock is frozen.
	w.expectNone(150 * time.Millisecond)
}

// TestSlowReaderTeardown pins the write-error policy: a client that stops
// reading and disconnects is ITS problem — the server's write fails, the
// stream ctx ends, and the server keeps serving everyone else.
func TestSlowReaderTeardown(t *testing.T) {
	spec, _ := startServer(t, nil)
	w := dialWire(t, spec)
	w.hello(t, v1.Major, v1.Minor)
	w.request("s-flood", "stream.flood", 0, "")

	// Read nothing; drop the connection while the server floods.
	time.Sleep(20 * time.Millisecond)
	w.conn.Close()

	select {
	case err := <-floodErrCh:
		if err == nil {
			t.Fatal("flood send must fail after disconnect")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("flood stream never observed the write failure")
	}

	w2 := dialWire(t, spec)
	w2.hello(t, v1.Major, v1.Minor)
	w2.request("req-1", "echo", 0, `{"msg":"healthy"}`)
	if resp := decodeResponse(t, w2.next(5*time.Second)); resp.Error != nil {
		t.Fatalf("post-teardown echo = %+v", resp)
	}
}

// TestCloseCancelsInFlight pins teardown: Close cancels every in-flight
// handler ctx and Serve returns nil.
func TestCloseCancelsInFlight(t *testing.T) {
	spec, srv := startServer(t, nil)
	w := dialWire(t, spec)
	w.hello(t, v1.Major, v1.Minor)
	w.request("req-1", "slow", 0, "")

	time.Sleep(20 * time.Millisecond) // let the request reach the handler
	if err := srv.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-slowSawCancel:
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight handler ctx was not cancelled by Close")
	}
	if err := srv.Close(); err != nil {
		t.Fatalf("second Close = %v, want nil", err)
	}
}
