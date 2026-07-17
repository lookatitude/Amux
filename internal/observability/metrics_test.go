package observability

import (
	"sync"
	"testing"
)

func TestCounterAddIncLoad(t *testing.T) {
	var c Counter
	if got := c.Load(); got != 0 {
		t.Fatalf("zero-value Counter.Load() = %d, want 0", got)
	}
	c.Add(5)
	c.Inc()
	if got := c.Load(); got != 6 {
		t.Fatalf("Counter.Load() = %d, want 6", got)
	}
}

func TestGaugeSetAddLoad(t *testing.T) {
	var g Gauge
	g.Set(10)
	g.Add(-3)
	if got := g.Load(); got != 7 {
		t.Fatalf("Gauge.Load() = %d, want 7", got)
	}
	g.Set(0)
	if got := g.Load(); got != 0 {
		t.Fatalf("Gauge.Load() after Set(0) = %d, want 0", got)
	}
}

// TestRegistryGetOrCreateStable asserts that repeated lookups of the same name
// return the same instrument instance, so increments are never lost to a
// duplicate registration.
func TestRegistryGetOrCreateStable(t *testing.T) {
	r := NewRegistry()
	c1 := r.Counter(MetricPTYSpawns)
	c2 := r.Counter(MetricPTYSpawns)
	if c1 != c2 {
		t.Fatal("Registry.Counter returned distinct instances for the same name")
	}
	g1 := r.Gauge(MetricPTYLive)
	g2 := r.Gauge(MetricPTYLive)
	if g1 != g2 {
		t.Fatal("Registry.Gauge returned distinct instances for the same name")
	}
}

// TestRegistryNameKindCollisionPanics asserts the single-namespace rule: one
// name is one instrument kind. Registering the same name as both a counter and
// a gauge is a programmer error and must fail loudly, not merge silently.
func TestRegistryNameKindCollisionPanics(t *testing.T) {
	r := NewRegistry()
	r.Counter("m")
	defer func() {
		if recover() == nil {
			t.Fatal("Gauge(name) on a counter-registered name did not panic")
		}
	}()
	r.Gauge("m")
}

func TestRegistrySnapshot(t *testing.T) {
	r := NewRegistry()
	r.Counter(MetricEventRingAppends).Add(3)
	r.Gauge(MetricAttachObservers).Set(2)
	snap := r.Snapshot()
	if snap[MetricEventRingAppends] != 3 {
		t.Errorf("snapshot[%q] = %d, want 3", MetricEventRingAppends, snap[MetricEventRingAppends])
	}
	if snap[MetricAttachObservers] != 2 {
		t.Errorf("snapshot[%q] = %d, want 2", MetricAttachObservers, snap[MetricAttachObservers])
	}
	if len(snap) != 2 {
		t.Errorf("snapshot has %d entries, want 2: %v", len(snap), snap)
	}
	// The snapshot is a copy: mutating it must not touch the registry.
	snap[MetricEventRingAppends] = 99
	if got := r.Counter(MetricEventRingAppends).Load(); got != 3 {
		t.Errorf("registry counter mutated through snapshot copy: %d", got)
	}
}

// TestMetricNamesFrozenAndDistinct pins the pre-declared metric name constants
// (dump/diagnostic consumers key on them) and asserts no two constants collide.
func TestMetricNamesFrozenAndDistinct(t *testing.T) {
	want := map[string]string{
		"MetricControlMailboxDepth":           "control_mailbox_depth",
		"MetricControlMailboxRejections":      "control_mailbox_rejections",
		"MetricSessionMailboxDepth":           "session_mailbox_depth",
		"MetricSessionMailboxRejections":      "session_mailbox_rejections",
		"MetricEventRingAppends":              "event_ring_appends",
		"MetricEventRingEvictions":            "event_ring_evictions",
		"MetricAttachSlowConsumerDisconnects": "attach_slow_consumer_disconnects",
		"MetricAttachReplayBytesServed":       "attach_replay_bytes_served",
		"MetricPTYSpawns":                     "pty_spawns",
		"MetricPTYLive":                       "pty_live",
		"MetricPTYExits":                      "pty_exits",
		"MetricAttachObservers":               "attach_observers",
		"MetricAttachInputLeases":             "attach_input_leases",
		"MetricHookActivationsAllowed":        "hook_activations_allowed",
		"MetricHookActivationsDenied":         "hook_activations_denied",
		"MetricNotifyPublishes":               "notify_publishes",
		"MetricNotifyDeliveryFailures":        "notify_delivery_failures",
	}
	got := map[string]string{
		"MetricControlMailboxDepth":           MetricControlMailboxDepth,
		"MetricControlMailboxRejections":      MetricControlMailboxRejections,
		"MetricSessionMailboxDepth":           MetricSessionMailboxDepth,
		"MetricSessionMailboxRejections":      MetricSessionMailboxRejections,
		"MetricEventRingAppends":              MetricEventRingAppends,
		"MetricEventRingEvictions":            MetricEventRingEvictions,
		"MetricAttachSlowConsumerDisconnects": MetricAttachSlowConsumerDisconnects,
		"MetricAttachReplayBytesServed":       MetricAttachReplayBytesServed,
		"MetricPTYSpawns":                     MetricPTYSpawns,
		"MetricPTYLive":                       MetricPTYLive,
		"MetricPTYExits":                      MetricPTYExits,
		"MetricAttachObservers":               MetricAttachObservers,
		"MetricAttachInputLeases":             MetricAttachInputLeases,
		"MetricHookActivationsAllowed":        MetricHookActivationsAllowed,
		"MetricHookActivationsDenied":         MetricHookActivationsDenied,
		"MetricNotifyPublishes":               MetricNotifyPublishes,
		"MetricNotifyDeliveryFailures":        MetricNotifyDeliveryFailures,
	}
	seen := make(map[string]string, len(got))
	for constName, value := range got {
		if value != want[constName] {
			t.Errorf("%s = %q, want %q (frozen name)", constName, value, want[constName])
		}
		if prev, dup := seen[value]; dup {
			t.Errorf("metric name %q declared by both %s and %s", value, prev, constName)
		}
		seen[value] = constName
	}
}

// TestRegistryConcurrent hammers get-or-create, add/set, and Snapshot from many
// goroutines. Run under -race; correctness is exact counter totals afterwards.
func TestRegistryConcurrent(t *testing.T) {
	r := NewRegistry()
	const workers = 16
	const perWorker = 1000
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				r.Counter(MetricEventRingAppends).Inc()
				r.Gauge(MetricSessionMailboxDepth).Set(int64(i))
				if i%100 == 0 {
					_ = r.Snapshot()
				}
			}
		}()
	}
	wg.Wait()
	if got := r.Counter(MetricEventRingAppends).Load(); got != workers*perWorker {
		t.Fatalf("concurrent counter total = %d, want %d", got, workers*perWorker)
	}
}
