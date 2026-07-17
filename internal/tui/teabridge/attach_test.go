// attach_test.go proves the REAL attach lifecycle through the production
// bridge: the stream opens over the typed AttachConn seam for the focused
// surface, the daemon's snapshot-at-N cells fold without any VT parsing,
// delivered sequences are tracked, detach closes the stream + releases the
// lease + quits, recovery re-attaches from the last delivered sequence (with
// the daemon's replay_gap boundary surfaced), focus changes move the single
// stream without duplicates or stale folds, and daemon loss / slow-consumer
// overflow / lease denial all present as honest recovery/read-only state.
package teabridge

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"

	v1 "github.com/amux-run/amux/api/v1"
	"github.com/amux-run/amux/internal/client"
	"github.com/amux-run/amux/internal/rpcapi"
	"github.com/amux-run/amux/internal/tui/a11y"
	tuiapp "github.com/amux-run/amux/internal/tui/app"
	"github.com/amux-run/amux/internal/tui/attachstate"
	"github.com/amux-run/amux/internal/tui/keys"
	"github.com/amux-run/amux/internal/tui/model"
)

// --- scripted attach transport fakes ------------------------------------------

type scriptItem struct {
	ev  v1.Event
	err error
}

// fakeStream replays a scripted frame sequence; when the script is exhausted it
// returns final (context.Canceled by default — the silent our-own-shutdown
// terminal, so a test that stops pumping leaves no visible state behind).
type fakeStream struct {
	mu     sync.Mutex
	script []scriptItem
	final  error
}

func (s *fakeStream) Recv() (v1.Event, []byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.script) == 0 {
		if s.final != nil {
			return v1.Event{}, nil, s.final
		}
		return v1.Event{}, nil, context.Canceled
	}
	it := s.script[0]
	s.script = s.script[1:]
	if it.err != nil {
		return v1.Event{}, nil, it.err
	}
	return it.ev, nil, nil
}

type fakeAttachConn struct {
	mu        sync.Mutex
	stream    *fakeStream
	params    rpcapi.AttachParams
	attachErr error
	closed    bool
}

func (c *fakeAttachConn) Attach(ctx context.Context, p rpcapi.AttachParams) (AttachStream, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.params = p
	if c.attachErr != nil {
		return nil, c.attachErr
	}
	return c.stream, nil
}

func (c *fakeAttachConn) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	return nil
}

func (c *fakeAttachConn) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *fakeAttachConn) attachParams() rpcapi.AttachParams {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.params
}

// fakeDialer hands out scripted dedicated connections in order.
type fakeDialer struct {
	mu    sync.Mutex
	conns []*fakeAttachConn
	dials int
	err   error
}

func (d *fakeDialer) dial(ctx context.Context) (AttachConn, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.dials++
	if d.err != nil {
		return nil, d.err
	}
	if len(d.conns) == 0 {
		return nil, errors.New("fake dialer: no scripted connection left")
	}
	c := d.conns[0]
	d.conns = d.conns[1:]
	return c, nil
}

func (d *fakeDialer) dialCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.dials
}

// snapshotEvent builds a flow-12 attach_snapshot header event with the opt-in
// cells payload (a 1×3 grid containing text) and an optional replay_gap.
func snapshotEvent(surface string, upToSeq uint64, text string, gap *attachstate.GapInfo) v1.Event {
	cells := [][]rpcapi.SurfaceCell{{}}
	for _, r := range text {
		cells[0] = append(cells[0], rpcapi.SurfaceCell{Text: string(r), Width: 1})
	}
	payload := map[string]any{
		"surface": surface, "rows": 1, "cols": len(cells[0]), "title": surface,
		"up_to_seq": upToSeq,
		"cells": rpcapi.AttachSnapshotCells{UpToSeq: upToSeq, Grid: rpcapi.CellGrid{
			Rows: 1, Cols: len(cells[0]), Cells: cells,
			Cursor: rpcapi.CellCursor{Row: 0, Col: 0, Visible: false},
		}},
	}
	if gap != nil {
		payload["replay_gap"] = map[string]any{
			"requested_from": gap.RequestedFrom, "oldest_retained": gap.OldestRetained,
			"latest_seq": gap.LatestSeq, "code": "replay_gap",
		}
	}
	b, _ := json.Marshal(payload)
	return v1.Event{Type: v1.TypeEvent, Event: "attach_snapshot", Seq: upToSeq, Payload: b}
}

func frameEvent(seq uint64) v1.Event {
	return v1.Event{Type: v1.TypeEvent, Event: "raw_output", Seq: seq}
}

// attachBridge builds a bridge with the attach transport configured.
func attachBridge(f *fakeClient, d *fakeDialer) *Model {
	appModel := tuiapp.New(100, 40, keys.DefaultKeymap(), a11y.DefaultProfile())
	return New(Config{
		App: appModel, Client: f, Ctx: context.Background(),
		Session: "s1", Workspace: "w1", AttachDial: d.dial,
	})
}

// pumpAttach runs the bridge's own receive step (the production attachRecv
// command) and feeds each resulting message through Update, executing follow-up
// commands one level, until the stream terminates or steps run out.
func pumpAttach(m *Model, steps int) {
	for i := 0; i < steps; i++ {
		cmd := m.attachRecv()
		if cmd == nil {
			return
		}
		msg := cmd()
		feed(m, msg)
		if _, done := msg.(attachClosedMsg); done {
			return
		}
	}
}

func feed(m *Model, msg tea.Msg) {
	_, next := m.Update(msg)
	for _, r := range exec(next) {
		if _, isTick := r.(tickMsg); isTick {
			continue
		}
		m.Update(r)
	}
}

// --- tests ---------------------------------------------------------------------

// TestAttachOpensRealStreamForFocusedSurface is the F1 headline: folding the
// authoritative tree makes the production bridge open the daemon attach stream
// (dedicated connection, Cells=true, live-only cursor) for the focused pane's
// active surface, adopt the exact snapshot-at-N cell grid, and present live —
// no fabricated state, no VT parsing.
func TestAttachOpensRealStreamForFocusedSurface(t *testing.T) {
	f := eightPaneFake()
	conn := &fakeAttachConn{stream: &fakeStream{script: []scriptItem{
		{ev: snapshotEvent("pane-0-s1", 3, "ATT", nil)},
	}}}
	d := &fakeDialer{conns: []*fakeAttachConn{conn}}
	m := attachBridge(f, d)
	drive(m, treeMsg{res: f.tree})

	if d.dialCount() != 1 {
		t.Fatalf("expected exactly one dedicated attach dial, got %d", d.dialCount())
	}
	p := conn.attachParams()
	if p.Session != "s1" || p.Surface != "pane-0-s1" || p.FromSeq != 0 || !p.Cells {
		t.Fatalf("attach params wrong: %+v", p)
	}
	if m.att == nil || m.att.surface != "pane-0-s1" {
		t.Fatal("bridge should hold the live attach session for the focused surface")
	}
	st := m.App().AttachState()
	if st.Phase != model.PhaseLive {
		t.Fatalf("live-only attach should present live after the snapshot, got %s", st.Phase)
	}
	if st.UpToSeq != 3 {
		t.Fatalf("cutover sequence should be the daemon's 3, got %d", st.UpToSeq)
	}
	if m.attSeqs["pane-0-s1"] != 3 {
		t.Fatalf("last delivered sequence should start at the cutover, got %d", m.attSeqs["pane-0-s1"])
	}
	if frame := Frame(m.App()); !strings.Contains(frame, "ATT") {
		t.Errorf("attach snapshot cells should reach the frame\n%s", frame)
	}
}

// TestAttachFramesTrackSequenceAndReproject proves delivered raw_output frames
// are folded as sequence + change signal: the last delivered sequence advances
// and the daemon-owned cells re-project via surface.cells (delta-gated) — the
// raw body is never interpreted.
func TestAttachFramesTrackSequenceAndReproject(t *testing.T) {
	f := eightPaneFake()
	conn := &fakeAttachConn{stream: &fakeStream{script: []scriptItem{
		{ev: snapshotEvent("pane-0-s1", 3, "ATT", nil)},
		{ev: frameEvent(4)},
		{ev: frameEvent(5)},
	}}}
	d := &fakeDialer{conns: []*fakeAttachConn{conn}}
	m := attachBridge(f, d)
	drive(m, treeMsg{res: f.tree})
	before := f.countCalls(rpcapi.MethodSurfaceCells)

	pumpAttach(m, 4)
	if got := m.attSeqs["pane-0-s1"]; got != 5 {
		t.Fatalf("last delivered sequence should track daemon frames (want 5), got %d", got)
	}
	if f.countCalls(rpcapi.MethodSurfaceCells) <= before {
		t.Fatal("a delivered frame should trigger a surface.cells re-projection")
	}
}

// TestDetachClosesStreamReleasesLeaseAndQuits proves Ctrl+b d is a REAL
// detach: the attach stream's dedicated connection closes, the UI-owned input
// lease is explicitly released, and the program quits — never a no-op.
func TestDetachClosesStreamReleasesLeaseAndQuits(t *testing.T) {
	f := eightPaneFake()
	conn := &fakeAttachConn{stream: &fakeStream{script: []scriptItem{
		{ev: snapshotEvent("pane-0-s1", 3, "ATT", nil)},
	}}}
	d := &fakeDialer{conns: []*fakeAttachConn{conn}}
	m := attachBridge(f, d)
	drive(m, treeMsg{res: f.tree})

	drive(m, tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	msgs := exec(cmd)
	quit := false
	for _, r := range msgs {
		if _, ok := r.(tea.QuitMsg); ok {
			quit = true
		}
		m.Update(r)
	}
	if !quit {
		t.Fatal("detach must quit the client (spec: detach closes the stream, never the process)")
	}
	if !conn.isClosed() {
		t.Fatal("detach must close the attach stream's dedicated connection")
	}
	if m.att != nil {
		t.Fatal("detach must drop the attach session")
	}
	if !f.called(rpcapi.MethodInputRelease) {
		t.Fatal("detach must release the UI-owned input lease")
	}
	if p := f.lastRelease; p.Surface != "pane-0-s1" || p.LeaseID != "amux-tui" {
		t.Fatalf("input.release must target the attached surface with our lease id, got %+v", p)
	}
}

// TestDaemonLossThenRecoverReattachesFromLastSeq proves the reconnect/resume
// contract: a lost stream presents disconnected (recovery=redial, no
// auto-retry), and the operator's recover action re-dials a FRESH connection
// re-attaching strictly after the last delivered sequence; the daemon's
// replay_gap boundary surfaces verbatim and replay transitions to live at the
// new cutover.
func TestDaemonLossThenRecoverReattachesFromLastSeq(t *testing.T) {
	f := eightPaneFake()
	conn1 := &fakeAttachConn{stream: &fakeStream{script: []scriptItem{
		{ev: snapshotEvent("pane-0-s1", 3, "ATT", nil)},
		{ev: frameEvent(4)},
		{err: &client.Error{Code: v1.ErrInternal, Message: "connection lost", Retryable: true}},
	}}}
	conn2 := &fakeAttachConn{stream: &fakeStream{script: []scriptItem{
		{ev: snapshotEvent("pane-0-s1", 9, "NEW", &attachstate.GapInfo{RequestedFrom: 5, OldestRetained: 7, LatestSeq: 9})},
		{ev: frameEvent(10)},
	}}}
	d := &fakeDialer{conns: []*fakeAttachConn{conn1, conn2}}
	m := attachBridge(f, d)
	drive(m, treeMsg{res: f.tree})
	pumpAttach(m, 3)

	st := m.App().AttachState()
	if st.Phase != model.PhaseDisconnected || st.Recovery != attachstate.RecRedial {
		t.Fatalf("stream loss should present disconnected/redial, got %s/%s", st.Phase, st.Recovery)
	}
	if d.dialCount() != 1 {
		t.Fatalf("no auto-reconnect: the machine never recovers on its own (dials=%d)", d.dialCount())
	}

	// Operator recovery: prefix + g re-attaches from last delivered sequence.
	drive(m, tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	drive(m, tea.KeyPressMsg{Code: 'g', Text: "g"})
	if d.dialCount() != 2 {
		t.Fatalf("recover should dial a fresh attach connection, dials=%d", d.dialCount())
	}
	if p := conn2.attachParams(); p.FromSeq != 5 {
		t.Fatalf("recover must resume strictly after the last delivered sequence (want FromSeq=5), got %d", p.FromSeq)
	}
	st = m.App().AttachState()
	if st.Phase != model.PhaseReplaying {
		t.Fatalf("resume with pending replay should present replaying, got %s", st.Phase)
	}
	if st.Gap == nil || st.Gap.RequestedFrom != 5 || st.Gap.OldestRetained != 7 {
		t.Fatalf("the daemon's replay_gap boundary must surface verbatim, got %+v", st.Gap)
	}
	if !strings.Contains(m.App().StatusText(), "gap=5<7") {
		t.Errorf("gap boundary should be visible in the status bar: %q", m.App().StatusText())
	}
	pumpAttach(m, 1) // frame 10 ≥ cutover 9 → live
	if st = m.App().AttachState(); st.Phase != model.PhaseLive {
		t.Fatalf("reaching the cutover should present live, got %s", st.Phase)
	}
	if m.attSeqs["pane-0-s1"] != 10 {
		t.Fatalf("delivered sequence should advance to 10, got %d", m.attSeqs["pane-0-s1"])
	}
}

// TestSlowConsumerOverflowSurfacesRecovery proves the daemon's slow-consumer
// disconnect (resource_exhausted) presents as the slow_detached recovery state
// recommending reattach — overflow is never hidden or stitched.
func TestSlowConsumerOverflowSurfacesRecovery(t *testing.T) {
	f := eightPaneFake()
	conn := &fakeAttachConn{stream: &fakeStream{script: []scriptItem{
		{ev: snapshotEvent("pane-0-s1", 3, "ATT", nil)},
		{err: &client.Error{Code: v1.ErrResourceExhausted, Message: "slow consumer", Retryable: true}},
	}}}
	d := &fakeDialer{conns: []*fakeAttachConn{conn}}
	m := attachBridge(f, d)
	drive(m, treeMsg{res: f.tree})
	pumpAttach(m, 2)

	st := m.App().AttachState()
	if st.Phase != model.PhaseSlowDetached || st.Recovery != attachstate.RecReattach {
		t.Fatalf("slow-consumer overflow should present slow_detached/reattach, got %s/%s", st.Phase, st.Recovery)
	}
	if !conn.isClosed() {
		t.Fatal("a terminated session must close its dedicated connection")
	}
}

// TestAttachDialFailurePresentsDisconnected proves daemon loss at open time
// (dial failure) surfaces as the disconnected recovery state, never a crash or
// a fabricated live phase.
func TestAttachDialFailurePresentsDisconnected(t *testing.T) {
	f := eightPaneFake()
	d := &fakeDialer{err: errors.New("dial unix: connection refused")}
	m := attachBridge(f, d)
	drive(m, treeMsg{res: f.tree})

	st := m.App().AttachState()
	if st.Phase != model.PhaseDisconnected || st.Recovery != attachstate.RecRedial {
		t.Fatalf("attach dial failure should present disconnected/redial, got %s/%s", st.Phase, st.Recovery)
	}
}

// TestFocusChangeMovesAttachWithoutDuplicates proves the attach lifecycle moves
// with focus: the old session's connection closes BEFORE a new stream opens for
// the newly focused surface, and only one session ever exists.
func TestFocusChangeMovesAttachWithoutDuplicates(t *testing.T) {
	f := eightPaneFake()
	conn1 := &fakeAttachConn{stream: &fakeStream{script: []scriptItem{
		{ev: snapshotEvent("pane-0-s1", 3, "ATT", nil)},
	}}}
	conn2 := &fakeAttachConn{stream: &fakeStream{script: []scriptItem{
		{ev: snapshotEvent("pane-4-s1", 7, "P4!", nil)},
	}}}
	d := &fakeDialer{conns: []*fakeAttachConn{conn1, conn2}}
	m := attachBridge(f, d)
	drive(m, treeMsg{res: f.tree})

	// prefix + l focuses pane-4 (daemon pane.focus; the fake reflects it in the
	// re-fetched tree — daemon authority, not a local guess). The re-fetched
	// tree reconciles the attach session onto the new focused surface.
	drive(m, tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'l', Text: "l"})
	for _, r := range exec(cmd) {
		if _, isTick := r.(tickMsg); isTick {
			continue
		}
		feed(m, r)
	}
	if !conn1.isClosed() {
		t.Fatal("focus change must close the previous attach session")
	}
	if d.dialCount() != 2 {
		t.Fatalf("expected the second dedicated dial for the new surface, dials=%d", d.dialCount())
	}
	if p := conn2.attachParams(); p.Surface != "pane-4-s1" {
		t.Fatalf("attach should follow focus to pane-4-s1, got %q", p.Surface)
	}
	if m.att == nil || m.att.surface != "pane-4-s1" || m.att.conn != conn2 {
		t.Fatal("exactly one live session for the newly focused surface must remain")
	}
}

// TestStaleAttachMessagesAreDiscarded is the race-safety proof: results of a
// superseded open and frames from a replaced session are dropped (the stale
// session is closed, its sequences never fold) — no duplicate streams, no
// stale writes.
func TestStaleAttachMessagesAreDiscarded(t *testing.T) {
	f := eightPaneFake()
	connA := &fakeAttachConn{stream: &fakeStream{script: []scriptItem{
		{ev: snapshotEvent("pane-0-s1", 3, "OLD", nil)},
	}}}
	connB := &fakeAttachConn{stream: &fakeStream{script: []scriptItem{
		{ev: snapshotEvent("pane-0-s1", 8, "NEW", nil)},
	}}}
	connC := &fakeAttachConn{stream: &fakeStream{script: []scriptItem{
		{ev: snapshotEvent("pane-0-s1", 9, "CUR", nil)},
	}}}
	d := &fakeDialer{conns: []*fakeAttachConn{connA, connB, connC}}
	m := attachBridge(f, d)
	drive(m, treeMsg{res: f.tree}) // seeds panes; opens session A
	oldGen := m.att.gen

	// Two supersessions race: B's open starts, then C replaces it before B's
	// result lands. B must be discarded and closed; C becomes the session.
	m.closeAttach()
	cmdB := m.openAttach("pane-0", "pane-0-s1", 0)
	m.closeAttach() // supersede the in-flight B
	cmdC := m.openAttach("pane-0", "pane-0-s1", 0)
	m.Update(cmdB()) // lost the race → discarded
	if !connB.isClosed() {
		t.Fatal("an open that lost the generation race must close its connection")
	}
	if m.att != nil {
		t.Fatal("the stale open must not be adopted")
	}
	m.Update(cmdC())
	if m.att == nil || m.att.conn != connC {
		t.Fatal("the current-generation open must be adopted")
	}

	// A stale frame from the replaced session A must not advance sequences.
	before := m.attSeqs["pane-0-s1"]
	m.Update(attachFrameMsg{gen: oldGen, surface: "pane-0-s1", seq: 99})
	if m.attSeqs["pane-0-s1"] != before {
		t.Fatal("a stale session's frame must never fold into delivered sequences")
	}
	// A stale close must neither clobber presentation nor kill session C.
	m.Update(attachClosedMsg{gen: oldGen, surface: "pane-0-s1", err: errors.New("boom")})
	if ph := m.App().AttachState().Phase; ph == model.PhaseDisconnected {
		t.Fatal("a stale session's failure must not surface as current state")
	}
	if m.att == nil || connC.isClosed() {
		t.Fatal("a stale close must not tear down the current session")
	}
}

// TestLeaseDeniedPresentsReadOnly proves the read-only path: the daemon's
// typed not_input_lease_holder rejection folds as lease=other and the attach
// presentation drops from live to read_only — daemon truth, never invented.
func TestLeaseDeniedPresentsReadOnly(t *testing.T) {
	f := eightPaneFake()
	f.inputErr = &client.Error{Code: v1.ErrNotInputLeaseHolder, Message: "lease held by amux-cli"}
	conn := &fakeAttachConn{stream: &fakeStream{script: []scriptItem{
		{ev: snapshotEvent("pane-0-s1", 3, "ATT", nil)},
	}}}
	d := &fakeDialer{conns: []*fakeAttachConn{conn}}
	m := attachBridge(f, d)
	drive(m, treeMsg{res: f.tree})

	drive(m, tea.KeyPressMsg{Code: 'x', Text: "x"}) // passthrough write → denied
	st := m.App().AttachState()
	if st.Lease != model.LeaseOther {
		t.Fatalf("lease denial should present lease=other, got %s", st.Lease)
	}
	if st.Phase != model.PhaseReadOnly {
		t.Fatalf("without the lease the attach presents read_only, got %s", st.Phase)
	}
}

// TestQuitCancelsAttachSession proves program exit tears the session down (no
// leaked stream/goroutine owner survives the model).
func TestQuitCancelsAttachSession(t *testing.T) {
	f := eightPaneFake()
	conn := &fakeAttachConn{stream: &fakeStream{script: []scriptItem{
		{ev: snapshotEvent("pane-0-s1", 3, "ATT", nil)},
	}}}
	d := &fakeDialer{conns: []*fakeAttachConn{conn}}
	m := attachBridge(f, d)
	drive(m, treeMsg{res: f.tree})

	m.Update(tea.QuitMsg{})
	if !conn.isClosed() || m.att != nil {
		t.Fatal("quit must close the attach session")
	}
}
