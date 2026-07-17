package attach

import (
	"sync"
	"testing"
	"time"

	v1 "github.com/amux-run/amux/api/v1"
	"github.com/amux-run/amux/internal/observability"
	"github.com/amux-run/amux/internal/ordering"
	"github.com/amux-run/amux/internal/terminal"
)

// --- helpers --------------------------------------------------------------

// watchdog panics if the test has not signalled completion within d, so a
// deadlock surfaces as a failure with a stack trace rather than a hung run.
func watchdog(t *testing.T, d time.Duration) func() {
	t.Helper()
	done := make(chan struct{})
	timer := time.AfterFunc(d, func() {
		select {
		case <-done:
		default:
			panic("attach test watchdog fired: " + t.Name())
		}
	})
	return func() {
		close(done)
		timer.Stop()
	}
}

func newRing(t *testing.T) *terminal.Ring {
	t.Helper()
	r, err := terminal.NewRing(terminal.MinRetentionBytes)
	if err != nil {
		t.Fatalf("NewRing: %v", err)
	}
	return r
}

// leaseLog records emitted lease notices for assertions.
type leaseLog struct {
	mu      sync.Mutex
	notices []LeaseNotice
}

func (l *leaseLog) cb(n LeaseNotice) {
	l.mu.Lock()
	l.notices = append(l.notices, n)
	l.mu.Unlock()
}

func (l *leaseLog) all() []LeaseNotice {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]LeaseNotice(nil), l.notices...)
}

// sinkLog records bytes that reached the input sink (i.e. reached the PTY).
type sinkLog struct {
	mu     sync.Mutex
	writes [][]byte
}

func (s *sinkLog) write(p []byte) error {
	s.mu.Lock()
	s.writes = append(s.writes, append([]byte(nil), p...))
	s.mu.Unlock()
	return nil
}

func (s *sinkLog) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.writes)
}

func recv(t *testing.T, ch <-chan Frame, d time.Duration) (Frame, bool) {
	t.Helper()
	select {
	case f, ok := <-ch:
		return f, ok
	case <-time.After(d):
		t.Fatalf("timeout waiting for frame")
		return Frame{}, false
	}
}

// drainSeqs reads frames until it has seen `until` (inclusive) or the channel
// closes, returning the ordered sequence numbers.
func drainSeqs(t *testing.T, att *Attachment, until uint64, d time.Duration) []uint64 {
	t.Helper()
	var seqs []uint64
	deadline := time.After(d)
	for {
		select {
		case f, ok := <-att.Frames():
			if !ok {
				return seqs
			}
			seqs = append(seqs, f.Seq)
			if f.Seq >= until {
				return seqs
			}
		case <-deadline:
			t.Fatalf("timeout draining frames (got %d, wanted up to %d)", len(seqs), until)
			return seqs
		}
	}
}

// --- tests ----------------------------------------------------------------

func TestAttachDeliversSnapshotThenReplayThenLive(t *testing.T) {
	defer watchdog(t, 10*time.Second)()
	ring := newRing(t)
	s, err := NewSurface(SurfaceConfig{
		ID:   "s1",
		Ring: ring,
		Snapshot: func() terminal.CellSnapshot {
			return terminal.CellSnapshot{Rows: 24, Cols: 80, Title: "hi"}
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Pre-attach output becomes replay.
	for i := 0; i < 3; i++ {
		if _, err := s.OnOutput([]byte{byte('a' + i)}); err != nil {
			t.Fatal(err)
		}
	}
	att, err := s.Attach("c1", AttachOptions{FromSeq: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer att.Detach()

	snap := att.Snapshot()
	if snap.UpToSeq != 3 {
		t.Fatalf("UpToSeq = %d, want 3", snap.UpToSeq)
	}
	if snap.Pane.SurfaceID != "s1" || snap.Pane.Title != "hi" || snap.Pane.Rows != 24 || snap.Pane.Cols != 80 {
		t.Fatalf("pane meta = %+v", snap.Pane)
	}
	if snap.ReplayGap != nil {
		t.Fatalf("unexpected replay gap: %+v", snap.ReplayGap)
	}
	// Replay frames 1..3.
	for want := uint64(1); want <= 3; want++ {
		f, _ := recv(t, att.Frames(), time.Second)
		if f.Seq != want {
			t.Fatalf("replay seq = %d, want %d", f.Seq, want)
		}
	}
	// Live output strictly > N.
	if _, err := s.OnOutput([]byte("z")); err != nil {
		t.Fatal(err)
	}
	f, _ := recv(t, att.Frames(), time.Second)
	if f.Seq != 4 || string(f.Data) != "z" {
		t.Fatalf("live frame = %d %q, want 4 z", f.Seq, f.Data)
	}
}

func TestTwoAttachmentsSameOrderedStream(t *testing.T) {
	defer watchdog(t, 15*time.Second)()
	s, err := NewSurface(SurfaceConfig{ID: "s", Ring: newRing(t)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	const n = 500
	a, _ := s.Attach("A", AttachOptions{Buffer: n + 16})
	defer a.Detach()
	b, _ := s.Attach("B", AttachOptions{Buffer: n + 16})
	defer b.Detach()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			if _, err := s.OnOutput([]byte{byte(i)}); err != nil {
				t.Errorf("OnOutput: %v", err)
				return
			}
		}
	}()

	var sa, sb []uint64
	var dw sync.WaitGroup
	dw.Add(2)
	go func() { defer dw.Done(); sa = drainSeqs(t, a, n, 10*time.Second) }()
	go func() { defer dw.Done(); sb = drainSeqs(t, b, n, 10*time.Second) }()
	wg.Wait()
	dw.Wait()

	if len(sa) != n || len(sb) != n {
		t.Fatalf("lengths: A=%d B=%d want %d", len(sa), len(sb), n)
	}
	for i := 0; i < n; i++ {
		if sa[i] != uint64(i+1) || sb[i] != uint64(i+1) {
			t.Fatalf("at %d: A=%d B=%d want %d (identical, contiguous, no dup)", i, sa[i], sb[i], i+1)
		}
	}
}

func TestNonHolderWriteRejectedHolderReaches(t *testing.T) {
	defer watchdog(t, 10*time.Second)()
	sink := &sinkLog{}
	s, err := NewSurface(SurfaceConfig{ID: "s", Ring: newRing(t), InputSink: sink.write}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// No holder yet: everyone rejected.
	if err := s.Write("A", []byte("x")); err != ErrNotLeaseHolder {
		t.Fatalf("write with no lease = %v, want ErrNotLeaseHolder", err)
	}
	if err := s.AcquireInput("A"); err != nil {
		t.Fatal(err)
	}
	// Non-holder rejected with the mapped wire code, sink untouched.
	if err := s.Write("B", []byte("y")); err != ErrNotLeaseHolder {
		t.Fatalf("non-holder write = %v, want ErrNotLeaseHolder", err)
	}
	if code, ok := ErrorCode(ErrNotLeaseHolder); !ok || code != v1.ErrNotInputLeaseHolder {
		t.Fatalf("ErrorCode = %v %v, want %v", code, ok, v1.ErrNotInputLeaseHolder)
	}
	if sink.count() != 0 {
		t.Fatalf("sink received %d rejected writes", sink.count())
	}
	// Holder write reaches the sink.
	if err := s.Write("A", []byte("ok")); err != nil {
		t.Fatal(err)
	}
	if sink.count() != 1 {
		t.Fatalf("sink count = %d, want 1", sink.count())
	}
}

func TestAcquireDoesNotImplicitlyTakeover(t *testing.T) {
	defer watchdog(t, 10*time.Second)()
	s, _ := NewSurface(SurfaceConfig{ID: "s", Ring: newRing(t)}, nil)
	if err := s.AcquireInput("A"); err != nil {
		t.Fatal(err)
	}
	if err := s.AcquireInput("B"); err != ErrInputLeaseHeld {
		t.Fatalf("second acquire = %v, want ErrInputLeaseHeld", err)
	}
	if h, held := s.InputHolder(); !held || h != "A" {
		t.Fatalf("holder = %q held=%v, want A", h, held)
	}
}

func TestTakeoverIsEvented(t *testing.T) {
	defer watchdog(t, 10*time.Second)()
	log := &leaseLog{}
	sink := &sinkLog{}
	s, _ := NewSurface(SurfaceConfig{ID: "s", Ring: newRing(t), InputSink: sink.write, OnLease: log.cb}, nil)

	if err := s.AcquireInput("A"); err != nil {
		t.Fatal(err)
	}
	if err := s.TakeoverInput("B"); err != nil {
		t.Fatal(err)
	}
	// Old holder now rejected; new holder can write.
	if err := s.Write("A", []byte("x")); err != ErrNotLeaseHolder {
		t.Fatalf("old holder write = %v, want ErrNotLeaseHolder", err)
	}
	if err := s.Write("B", []byte("y")); err != nil {
		t.Fatalf("new holder write = %v", err)
	}

	notices := log.all()
	if len(notices) != 2 {
		t.Fatalf("notices = %d, want 2 (acquire, takeover)", len(notices))
	}
	if notices[0].Kind != ordering.LeaseAcquired || notices[0].Holder != "A" {
		t.Fatalf("notice[0] = %+v, want acquired by A", notices[0])
	}
	if notices[1].Kind != ordering.LeaseTakenOver || notices[1].Holder != "B" || notices[1].Prev != "A" {
		t.Fatalf("notice[1] = %+v, want taken_over B prev A", notices[1])
	}
}

func TestDetachReleasesLeaseButSurfaceAndSinkStayLive(t *testing.T) {
	defer watchdog(t, 15*time.Second)()
	log := &leaseLog{}
	sink := &sinkLog{}
	s, _ := NewSurface(SurfaceConfig{ID: "s", Ring: newRing(t), InputSink: sink.write, OnLease: log.cb}, nil)

	// Two attachments; A holds the lease.
	a, _ := s.Attach("A", AttachOptions{})
	b, _ := s.Attach("B", AttachOptions{})
	defer b.Detach()
	if err := s.AcquireInput("A"); err != nil {
		t.Fatal(err)
	}

	// Detach the holder: lease released, PTY/surface untouched.
	a.Detach()
	if _, held := s.InputHolder(); held {
		t.Fatalf("lease still held after detach of holder")
	}
	// Surface keeps accepting output; the other attachment still streams.
	if _, err := s.OnOutput([]byte("live")); err != nil {
		t.Fatalf("OnOutput after detach: %v", err)
	}
	f, _ := recv(t, b.Frames(), time.Second)
	if string(f.Data) != "live" {
		t.Fatalf("b frame = %q, want live", f.Data)
	}
	// The sink is still live: a fresh acquire+write reaches it.
	if err := s.AcquireInput("B"); err != nil {
		t.Fatal(err)
	}
	if err := s.Write("B", []byte("z")); err != nil {
		t.Fatal(err)
	}
	if sink.count() != 1 {
		t.Fatalf("sink count = %d, want 1 (sink never stopped)", sink.count())
	}
	// A re-attach works.
	a2, err := s.Attach("A", AttachOptions{})
	if err != nil {
		t.Fatalf("re-attach: %v", err)
	}
	defer a2.Detach()
	if _, err := s.OnOutput([]byte("again")); err != nil {
		t.Fatal(err)
	}
	f2, _ := recv(t, a2.Frames(), time.Second)
	if string(f2.Data) != "again" {
		t.Fatalf("re-attach frame = %q, want again", f2.Data)
	}

	// A detach of the holder emits a release event.
	sawRelease := false
	for _, n := range log.all() {
		if n.Kind == ordering.LeaseReleased && n.Prev == "A" {
			sawRelease = true
		}
	}
	if !sawRelease {
		t.Fatalf("no lease_released event for A on detach")
	}
}

func TestSlowConsumerDisconnectedWithReceipt(t *testing.T) {
	defer watchdog(t, 20*time.Second)()
	reg := observability.NewRegistry()
	s, _ := NewSurface(SurfaceConfig{ID: "s", Ring: newRing(t), Buffer: 4}, reg)

	slow, _ := s.Attach("slow", AttachOptions{Buffer: 4}) // never drained
	fast, _ := s.Attach("fast", AttachOptions{})
	defer fast.Detach()

	const n = 300
	// Drain the fast attachment concurrently so it stays healthy.
	fastSeqs := make(chan []uint64, 1)
	go func() { fastSeqs <- drainSeqs(t, fast, n, 15*time.Second) }()

	for i := 0; i < n; i++ {
		if _, err := s.OnOutput([]byte{byte(i)}); err != nil {
			t.Fatalf("OnOutput: %v", err)
		}
	}

	// The slow attachment is disconnected with the receipt. A wedged client
	// never reads, so wait for the disconnect signal WITHOUT consuming — any
	// consumption before the verdict correctly cancels the wedge.
	select {
	case <-slow.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("slow attachment never disconnected")
	}
	// Buffered frames stay readable after the disconnect; drain to closure.
	for range slow.Frames() {
	}
	if slow.Err() != ErrSlowConsumer {
		t.Fatalf("slow.Err = %v, want ErrSlowConsumer", slow.Err())
	}
	if slow.LastDelivered() == 0 {
		t.Fatalf("slow.LastDelivered = 0, want a nonzero receipt")
	}

	// The fast attachment and the surface stay healthy.
	got := <-fastSeqs
	if len(got) != n {
		t.Fatalf("fast got %d frames, want %d", len(got), n)
	}
	for i := 0; i < n; i++ {
		if got[i] != uint64(i+1) {
			t.Fatalf("fast seq at %d = %d, want %d", i, got[i], i+1)
		}
	}
	if c := reg.Snapshot()[observability.MetricAttachSlowConsumerDisconnects]; c != 1 {
		t.Fatalf("slow-consumer disconnect counter = %d, want 1", c)
	}
}

// TestDrainedConsumerNeverDisconnectedDespiteTinyBuffer pins the deterministic
// half of the slow-consumer policy: an actively-drained observer must NEVER be
// disconnected, no matter how far a hot producer momentarily outruns the
// scheduler. Buffer 1 makes the old volume-only window (one extra frame of
// production) essentially guaranteed to fire spuriously, so this test fails
// against any policy that measures producer volume without giving the
// consumer a bounded chance to run.
func TestDrainedConsumerNeverDisconnectedDespiteTinyBuffer(t *testing.T) {
	defer watchdog(t, 30*time.Second)()
	s, _ := NewSurface(SurfaceConfig{ID: "s", Ring: newRing(t)}, nil)

	const n = 2000
	att, err := s.Attach("c", AttachOptions{Buffer: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer att.Detach()

	seqsCh := make(chan []uint64, 1)
	go func() { seqsCh <- drainSeqs(t, att, n, 20*time.Second) }()
	for i := 0; i < n; i++ {
		if _, err := s.OnOutput([]byte{byte(i)}); err != nil {
			t.Fatalf("OnOutput: %v", err)
		}
	}
	seqs := <-seqsCh
	if att.Err() != nil {
		t.Fatalf("att.Err = %v, want nil (a drained consumer is never a timing casualty)", att.Err())
	}
	if len(seqs) != n {
		t.Fatalf("drained consumer got %d frames, want %d", len(seqs), n)
	}
	for i, sq := range seqs {
		if sq != uint64(i+1) {
			t.Fatalf("seq at %d = %d, want %d", i, sq, i+1)
		}
	}
}

// TestWedgedConsumerDisconnectedAfterProducerStops pins the confirmation path:
// a genuinely wedged observer is disconnected by the bounded grace re-check
// even when no further output arrives after the stall window opens — the
// disconnect must not require ongoing production to be detected.
func TestWedgedConsumerDisconnectedAfterProducerStops(t *testing.T) {
	defer watchdog(t, 20*time.Second)()
	reg := observability.NewRegistry()
	s, _ := NewSurface(SurfaceConfig{ID: "s", Ring: newRing(t)}, reg)

	wedged, _ := s.Attach("wedged", AttachOptions{Buffer: 2}) // never drained
	const n = 40
	for i := 0; i < n; i++ {
		if _, err := s.OnOutput([]byte{byte(i)}); err != nil {
			t.Fatalf("OnOutput: %v", err)
		}
	}
	// No further OnOutput: the armed grace check alone must disconnect. Wait
	// without consuming — a wedged client never reads.
	select {
	case <-wedged.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("wedged attachment never disconnected")
	}
	for range wedged.Frames() {
	}
	if wedged.Err() != ErrSlowConsumer {
		t.Fatalf("wedged.Err = %v, want ErrSlowConsumer", wedged.Err())
	}
	if wedged.LastDelivered() == 0 {
		t.Fatalf("wedged.LastDelivered = 0, want a nonzero receipt")
	}
	if c := reg.Snapshot()[observability.MetricAttachSlowConsumerDisconnects]; c != 1 {
		t.Fatalf("slow-consumer disconnect counter = %d, want 1", c)
	}
}

func TestAttachCutoverContiguousUnderRace(t *testing.T) {
	defer watchdog(t, 30*time.Second)()
	s, _ := NewSurface(SurfaceConfig{ID: "s", Ring: newRing(t)}, nil)

	const total = 2000
	started := make(chan struct{})
	var once sync.Once
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < total; i++ {
			if _, err := s.OnOutput([]byte{byte(i)}); err != nil {
				t.Errorf("OnOutput: %v", err)
				return
			}
			if i == 200 {
				once.Do(func() { close(started) })
			}
		}
	}()

	<-started // attach mid-stream, concurrently with the writer
	att, err := s.Attach("c", AttachOptions{FromSeq: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer att.Detach()
	n := att.Snapshot().UpToSeq

	// Drain concurrently so the bounded live buffer never overflows.
	seqsCh := make(chan []uint64, 1)
	go func() { seqsCh <- drainSeqs(t, att, total, 20*time.Second) }()
	wg.Wait()
	seqs := <-seqsCh

	if att.Err() != nil {
		t.Fatalf("att.Err = %v, want nil (drained)", att.Err())
	}
	// Split at the cutover boundary and let the proven model validate it.
	var replay, live []uint64
	for _, sq := range seqs {
		if sq <= n {
			replay = append(replay, sq)
		} else {
			live = append(live, sq)
		}
	}
	merged, err := ordering.AttachCutover(n, replay, live)
	if err != nil {
		t.Fatalf("AttachCutover(N=%d) reported %v: replay=%d live=%d", n, err, len(replay), len(live))
	}
	if len(merged) != total {
		t.Fatalf("merged len = %d, want %d (contiguous 1..%d)", len(merged), total, total)
	}
	for i, sq := range merged {
		if sq != uint64(i+1) {
			t.Fatalf("merged[%d] = %d, want %d (zero gap/dup across boundary)", i, sq, i+1)
		}
	}
}

func TestReplayGapSurfaced(t *testing.T) {
	defer watchdog(t, 15*time.Second)()
	ring := newRing(t)
	chunk := make([]byte, 1<<20) // 1 MiB; 17 of them overrun the 16 MiB floor
	const chunks = 17
	for i := 0; i < chunks; i++ {
		if _, err := ring.Append(chunk); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	oldest := ring.OldestRetainedSeq()
	if oldest <= 1 {
		t.Fatalf("expected eviction: oldest retained = %d", oldest)
	}
	s, _ := NewSurface(SurfaceConfig{ID: "s", Ring: ring}, nil)

	// Request history older than what is retained.
	att, err := s.Attach("c", AttachOptions{FromSeq: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer att.Detach()
	snap := att.Snapshot()
	if snap.ReplayGap == nil {
		t.Fatalf("expected a ReplayGap boundary, got nil")
	}
	if snap.ReplayGap.RequestedFrom != 1 || snap.ReplayGap.OldestRetained != oldest {
		t.Fatalf("ReplayGap = %+v, want RequestedFrom=1 OldestRetained=%d", snap.ReplayGap, oldest)
	}
	if snap.ReplayGap.Code() != v1.ErrReplayGap {
		t.Fatalf("ReplayGap.Code = %v, want %v", snap.ReplayGap.Code(), v1.ErrReplayGap)
	}
	// Replay starts mid-history at OldestRetained, not silently at 1.
	f, _ := recv(t, att.Frames(), 2*time.Second)
	if f.Seq != oldest {
		t.Fatalf("first replay frame = %d, want oldest retained %d", f.Seq, oldest)
	}
}

func TestMetricsGaugesTrackObserversAndLeases(t *testing.T) {
	defer watchdog(t, 10*time.Second)()
	reg := observability.NewRegistry()
	s, _ := NewSurface(SurfaceConfig{ID: "s", Ring: newRing(t)}, reg)

	a, _ := s.Attach("A", AttachOptions{})
	b, _ := s.Attach("B", AttachOptions{})
	if g := reg.Snapshot()[observability.MetricAttachObservers]; g != 2 {
		t.Fatalf("observers gauge = %d, want 2", g)
	}
	if err := s.AcquireInput("A"); err != nil {
		t.Fatal(err)
	}
	if g := reg.Snapshot()[observability.MetricAttachInputLeases]; g != 1 {
		t.Fatalf("leases gauge = %d, want 1", g)
	}
	// Takeover keeps the lease count at 1 (held -> held).
	if err := s.TakeoverInput("B"); err != nil {
		t.Fatal(err)
	}
	if g := reg.Snapshot()[observability.MetricAttachInputLeases]; g != 1 {
		t.Fatalf("leases gauge after takeover = %d, want 1", g)
	}
	a.Detach()
	b.Detach()
	snap := reg.Snapshot()
	if snap[observability.MetricAttachObservers] != 0 {
		t.Fatalf("observers gauge after detach = %d, want 0", snap[observability.MetricAttachObservers])
	}
	if snap[observability.MetricAttachInputLeases] != 0 {
		t.Fatalf("leases gauge after holder detach = %d, want 0", snap[observability.MetricAttachInputLeases])
	}
}

func TestReplayBytesServedCounted(t *testing.T) {
	defer watchdog(t, 10*time.Second)()
	reg := observability.NewRegistry()
	s, _ := NewSurface(SurfaceConfig{ID: "s", Ring: newRing(t)}, reg)
	payload := []byte("hello")
	for i := 0; i < 4; i++ {
		if _, err := s.OnOutput(payload); err != nil {
			t.Fatal(err)
		}
	}
	att, _ := s.Attach("c", AttachOptions{FromSeq: 1})
	defer att.Detach()
	if got := reg.Snapshot()[observability.MetricAttachReplayBytesServed]; got != int64(4*len(payload)) {
		t.Fatalf("replay bytes served = %d, want %d", got, 4*len(payload))
	}
}

func TestManagerAddSurfaceAndRemove(t *testing.T) {
	defer watchdog(t, 10*time.Second)()
	reg := observability.NewRegistry()
	m := NewManager(reg)
	s, err := m.AddSurface(SurfaceConfig{ID: "s1", Ring: newRing(t)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddSurface(SurfaceConfig{ID: "s1", Ring: newRing(t)}); err != ErrSurfaceExists {
		t.Fatalf("duplicate add = %v, want ErrSurfaceExists", err)
	}
	if got, ok := m.Surface("s1"); !ok || got != s {
		t.Fatalf("Surface lookup failed")
	}
	att, _ := s.Attach("c", AttachOptions{})
	m.RemoveSurface("s1")
	// Removing the surface closes attachments.
	for range att.Frames() {
	}
	if att.Err() != ErrSurfaceClosed {
		t.Fatalf("att.Err after RemoveSurface = %v, want ErrSurfaceClosed", att.Err())
	}
	if m.Surfaces() != 0 {
		t.Fatalf("surfaces = %d, want 0", m.Surfaces())
	}
}

func TestMissingRingAndIDRejected(t *testing.T) {
	if _, err := NewSurface(SurfaceConfig{ID: "s"}, nil); err != ErrNoRing {
		t.Fatalf("no ring = %v, want ErrNoRing", err)
	}
	if _, err := NewSurface(SurfaceConfig{Ring: newRing(t)}, nil); err != ErrNoID {
		t.Fatalf("no id = %v, want ErrNoID", err)
	}
}
