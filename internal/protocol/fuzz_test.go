package protocol_test

// FuzzServerHeader feeds arbitrary bytes through the hello path of a live
// in-memory server. The frame codec's limits are fuzzed in api/v1; this fuzz
// targets the layer above it: lenient header decode, negotiation, and the
// fail-closed close paths must never panic or hang on hostile input
// (ADR-0003 fail-closed framing; STR-5). Deadlines on the client half of the
// pipe bound every iteration.

import (
	"bytes"
	"errors"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	v1 "github.com/amux-run/amux/api/v1"
	"github.com/amux-run/amux/internal/platform"
	"github.com/amux-run/amux/internal/protocol"
)

// pipeConn adapts a net.Pipe end to platform.LocalConn. Control hands out a
// dummy descriptor: the fake PeerCredentials never dereferences it.
type pipeConn struct{ net.Conn }

func (p pipeConn) Control(f func(fd uintptr) error) error { return f(1) }

// pipeListener feeds pre-built conns to Server.Serve.
type pipeListener struct {
	ch     chan platform.LocalConn
	closed chan struct{}
	once   sync.Once
}

func newPipeListener() *pipeListener {
	return &pipeListener{ch: make(chan platform.LocalConn, 1), closed: make(chan struct{})}
}

func (l *pipeListener) Accept() (platform.LocalConn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.closed:
		return nil, errors.New("pipe listener closed")
	}
}

func (l *pipeListener) Path() string { return "(in-memory pipe)" }

func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func FuzzServerHeader(f *testing.F) {
	f.Add([]byte(`{"type":"hello","major":1,"minor":0,"client":"amux/fuzz"}`))
	f.Add([]byte(`{"type":"hello","major":9,"minor":9,"client":"future"}`))
	f.Add([]byte(`{"type":"request","id":"r","method":"m"}`))
	f.Add([]byte(`not json at all`))
	f.Add([]byte(`{}`))
	f.Add(bytes.Repeat([]byte{0xff}, 512))

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) == 0 {
			data = []byte("{")
		}
		if len(data) > v1.MaxHeaderBytes {
			data = data[:v1.MaxHeaderBytes]
		}

		srv := protocol.NewServer(protocol.Options{
			BootID:      "boot-fuzz",
			ServerTag:   "amuxd/fuzz",
			Peers:       fakePeers{uid: uint32(os.Getuid())},
			HeartbeatMS: 60_000,
		})
		ln := newPipeListener()
		serveDone := make(chan struct{})
		go func() {
			srv.Serve(ln)
			close(serveDone)
		}()

		cli, srvEnd := net.Pipe()
		ln.ch <- pipeConn{srvEnd}

		// Bound every pipe operation: no hang regardless of server behavior.
		cli.SetDeadline(time.Now().Add(2 * time.Second))
		_ = v1.WriteFrame(cli, data, nil)
		_, _, _ = v1.ReadFrame(cli) // welcome, error response, or close
		cli.Close()

		if err := srv.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		<-serveDone
	})
}
