// attach.go is the bridge's REAL attach lifecycle (flow 12): the production
// TUI is itself an attached client. For the focused pane's active surface it
// opens the daemon attach stream on a DEDICATED connection (the shared client
// multiplexes one stream per connection, and a lagging stream reader must never
// stall the unary command path), folds the daemon's exact snapshot-at-N cell
// grid, and then treats every delivered raw_output frame as (a) the delivered
// sequence to track and (b) a change signal that re-projects the daemon-owned
// derived cells via the surface.cells delta gate. The raw frame BODY is never
// parsed — the TUI never interprets VT bytes and never owns sequence authority.
//
// Lifecycle safety: exactly one attach session exists at a time; a monotonic
// generation stamps every stream message, so a session replaced by a focus/
// surface change (or closed by detach) can never fold stale state into the
// core. Recovery is operator-driven (attachstate never auto-recovers): the
// recover action re-opens the stream FROM THE LAST DELIVERED SEQUENCE, and an
// evicted cursor surfaces the daemon's typed replay_gap boundary — the client
// never stitches gaps locally.
package teabridge

import (
	"context"
	"errors"
	"io"

	tea "charm.land/bubbletea/v2"

	v1 "github.com/amux-run/amux/api/v1"
	"github.com/amux-run/amux/internal/rpcapi"
	"github.com/amux-run/amux/internal/tui/app"
	"github.com/amux-run/amux/internal/tui/attachstate"
	"github.com/amux-run/amux/internal/tui/clientadapter"
	"github.com/amux-run/amux/internal/tui/model"
)

// AttachStream is one open attach event stream (satisfied by *client.Stream).
type AttachStream interface {
	Recv() (v1.Event, []byte, error)
}

// AttachConn is a dedicated daemon connection that can open the attach stream
// (flow 12). Close tears the connection down, which is also how the daemon
// observes the detach (ctx done → lease release + DetachClient server-side).
type AttachConn interface {
	Attach(ctx context.Context, p rpcapi.AttachParams) (AttachStream, error)
	Close() error
}

// AttachDialFunc dials a fresh dedicated connection for one attach session.
type AttachDialFunc func(ctx context.Context) (AttachConn, error)

// attachSession is one live attach stream: its dedicated connection, the
// stream, the cancel that ends both, and the identity of what it watches.
type attachSession struct {
	gen     int
	pane    string
	surface string
	conn    AttachConn
	stream  AttachStream
	cancel  context.CancelFunc
}

func (s *attachSession) close() {
	s.cancel()
	_ = s.conn.Close()
}

// --- attach stream messages ----------------------------------------------------

type attachOpenedMsg struct {
	gen     int
	sess    *attachSession
	snap    clientadapter.AttachSnapshot
	fromSeq uint64
}

type attachFrameMsg struct {
	gen     int
	surface string
	seq     uint64
}

type attachClosedMsg struct {
	gen     int
	surface string
	err     error
}

// --- lifecycle ------------------------------------------------------------------

// ensureAttach reconciles the single attach session with the focused pane's
// active surface: it opens the stream when none exists and MOVES it when focus
// or the active surface changed (closing the old session first, so there is
// never a duplicate stream). A surface seen before resumes from its last
// delivered sequence. Idempotent — the poll tick calls it every round.
func (m *Model) ensureAttach() tea.Cmd {
	if m.attachDial == nil {
		return nil // no attach transport configured (tests/preview); poll-only
	}
	pane := m.app.Focused()
	surf := m.activeSurface(pane)
	if surf == "" {
		return nil
	}
	if m.att != nil && m.att.surface == surf {
		return nil
	}
	if m.attPending && m.attPendingSurface == surf {
		return nil // an open for this surface is already in flight
	}
	m.closeAttach()
	return m.openAttach(pane, surf, m.resumeFrom(surf))
}

// resumeFrom picks the replay cursor for (re)attaching to surface: strictly
// after the last delivered sequence when one was seen, else live-only from the
// daemon's cutover (FromSeq 0).
func (m *Model) resumeFrom(surface string) uint64 {
	if last := m.attSeqs[surface]; last > 0 {
		return last + 1
	}
	return 0
}

// closeAttach ends the current session (if any) and invalidates every message
// still in flight — including a not-yet-adopted open — by bumping the
// generation. An orphaned open closes its own connection when its result
// arrives stale.
func (m *Model) closeAttach() {
	if m.att != nil {
		m.att.close()
		m.att = nil
		m.attGen++
	}
	if m.attPending {
		m.attPending = false
		m.attPendingSurface = ""
		m.attGen++
	}
}

// openAttach starts one attach session as a single tea command: dial the
// dedicated connection, open the attach stream (Cells=true for the exact
// snapshot-at-N grid), and read the attach_snapshot header. Everything runs in
// the command (never in Update); the resulting session is handed back via
// attachOpenedMsg and adopted only if its generation is still current.
func (m *Model) openAttach(pane, surface string, fromSeq uint64) tea.Cmd {
	m.attGen++
	gen := m.attGen
	m.attPending = true
	m.attPendingSurface = surface
	dial, parent, session := m.attachDial, m.ctx, m.session
	m.fold(app.AttachEventMsg{Phase: model.PhaseConnecting})
	return func() tea.Msg {
		ctx, cancel := context.WithCancel(parent)
		conn, err := dial(ctx)
		if err != nil {
			cancel()
			return attachClosedMsg{gen: gen, surface: surface, err: err}
		}
		st, err := conn.Attach(ctx, rpcapi.AttachParams{
			Session: session, Surface: surface, FromSeq: fromSeq, Cells: true,
		})
		if err != nil {
			cancel()
			_ = conn.Close()
			return attachClosedMsg{gen: gen, surface: surface, err: err}
		}
		for {
			ev, _, rerr := st.Recv()
			if rerr != nil {
				cancel()
				_ = conn.Close()
				return attachClosedMsg{gen: gen, surface: surface, err: rerr}
			}
			if ev.Event != "attach_snapshot" {
				continue // heartbeats (or unknown lenient events) before the header
			}
			snap, derr := clientadapter.AttachSnapshotFromPayload(ev.Payload)
			if derr != nil {
				cancel()
				_ = conn.Close()
				return attachClosedMsg{gen: gen, surface: surface, err: derr}
			}
			return attachOpenedMsg{
				gen:     gen,
				sess:    &attachSession{gen: gen, pane: pane, surface: surface, conn: conn, stream: st, cancel: cancel},
				snap:    snap,
				fromSeq: fromSeq,
			}
		}
	}
}

// attachRecv reads the next meaningful stream frame as one blocking command.
// Heartbeats are liveness only; raw_output frames deliver a sequence number.
// The frame BODY is deliberately discarded unparsed — cell content re-enters
// only through the daemon's derived surface.cells projection.
func (m *Model) attachRecv() tea.Cmd {
	sess := m.att
	if sess == nil {
		return nil
	}
	return func() tea.Msg {
		for {
			ev, _, err := sess.stream.Recv()
			if err != nil {
				return attachClosedMsg{gen: sess.gen, surface: sess.surface, err: err}
			}
			switch ev.Event {
			case "heartbeat", "attach_snapshot":
				continue
			default: // raw_output: the delivered sequence + a change signal
				return attachFrameMsg{gen: sess.gen, surface: sess.surface, seq: ev.Seq}
			}
		}
	}
}

// applyAttachOpened adopts a freshly opened session (or discards a stale one),
// folds the snapshot's attach phase + gap boundary + exact cell grid, and
// starts the receive loop.
func (m *Model) applyAttachOpened(t attachOpenedMsg) tea.Cmd {
	if t.gen != m.attGen || m.att != nil {
		// A focus change, detach, or newer attach superseded this open while it
		// was in flight: close it — never two live streams, never stale folds.
		t.sess.close()
		return nil
	}
	m.att = t.sess
	m.attPending = false
	m.attPendingSurface = ""

	if t.snap.UpToSeq > m.attSeqs[t.sess.surface] {
		m.attSeqs[t.sess.surface] = t.snap.UpToSeq
	}
	m.fold(app.AttachEventMsg{Phase: model.PhaseReplaying, Gap: t.snap.Gap, UpToSeq: t.snap.UpToSeq})
	if t.fromSeq == 0 || t.fromSeq > t.snap.UpToSeq {
		// Nothing to replay (live-only attach, or resume already at the cutover).
		m.fold(app.AttachEventMsg{Phase: model.PhaseLive})
	}
	if t.snap.Cells != nil {
		if ref := m.panesBySurface[t.sess.surface]; ref.pane != "" {
			if t.snap.UpToSeq > m.seqs[t.sess.surface] {
				m.seqs[t.sess.surface] = t.snap.UpToSeq
			}
			m.fold(app.PaneContentMsg{
				Pane: ref.pane, Snapshot: *t.snap.Cells,
				Class: ref.class, ExitReason: ref.exit, Title: firstNonEmpty(t.snap.Title, ref.title),
			})
		}
	}
	return m.attachRecv()
}

// applyAttachFrame tracks the delivered sequence, transitions replay→live at
// the daemon-declared cutover, and re-projects the derived cells (delta-gated).
func (m *Model) applyAttachFrame(t attachFrameMsg) tea.Cmd {
	if m.att == nil || t.gen != m.att.gen {
		return nil // stale frame from a replaced session
	}
	if t.seq > m.attSeqs[t.surface] {
		m.attSeqs[t.surface] = t.seq
	}
	if st := m.app.AttachState(); st.Phase == model.PhaseReplaying && t.seq >= st.UpToSeq {
		m.fold(app.AttachEventMsg{Phase: model.PhaseLive})
	}
	return tea.Batch(m.requestCells(t.surface, m.seqs[t.surface]), m.attachRecv())
}

// applyAttachClosed handles stream termination. Terminations of the CURRENT
// session classify into the visible recovery state (slow-consumer overflow,
// connection loss, typed gaps); a clean daemon end presents as stopped; our own
// cancellations and any termination of an already-replaced session are silent.
func (m *Model) applyAttachClosed(t attachClosedMsg) tea.Cmd {
	if t.gen != m.attGen {
		return nil // stale: a newer session (or an explicit close) superseded it
	}
	m.attPending = false
	m.attPendingSurface = ""
	if m.att != nil && m.att.gen == t.gen {
		m.att.close()
		m.att = nil
		m.attGen++
	}

	switch {
	case errors.Is(t.err, io.EOF):
		// The daemon ended the stream cleanly (surface ended / attach retired).
		m.fold(app.AttachEventMsg{Phase: model.PhaseStopped})
	case errors.Is(t.err, context.Canceled):
		// Our own shutdown path; nothing to present.
	default:
		kind := clientadapter.ClassifyErr(t.err)
		if kind == attachstate.ErrNone {
			// Untyped failure establishing/holding the stream: present it as the
			// disconnected boundary — recovery re-dials and re-attaches.
			kind = attachstate.ErrConnLost
		}
		m.fold(app.AttachErrMsg{Kind: kind})
	}
	return nil
}
