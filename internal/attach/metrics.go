package attach

import "github.com/amux-run/amux/internal/observability"

// metrics wires the four attach instruments (ADR-0004 observability seam). It
// is nil when no Registry is injected; every method is nil-safe so hot paths
// need no branch. The instruments are resolved by name from the registry, so
// several surfaces sharing one Registry share the same gauges/counters.
type metrics struct {
	observers   *observability.Gauge
	leases      *observability.Gauge
	slow        *observability.Counter
	replayBytes *observability.Counter
}

func newMetrics(reg *observability.Registry) *metrics {
	if reg == nil {
		return nil
	}
	return &metrics{
		observers:   reg.Gauge(observability.MetricAttachObservers),
		leases:      reg.Gauge(observability.MetricAttachInputLeases),
		slow:        reg.Counter(observability.MetricAttachSlowConsumerDisconnects),
		replayBytes: reg.Counter(observability.MetricAttachReplayBytesServed),
	}
}

func (m *metrics) addObservers(d int64) {
	if m != nil {
		m.observers.Add(d)
	}
}

func (m *metrics) addLeases(d int64) {
	if m != nil {
		m.leases.Add(d)
	}
}

func (m *metrics) incSlow() {
	if m != nil {
		m.slow.Inc()
	}
}

func (m *metrics) addReplayBytes(n int64) {
	if m != nil && n > 0 {
		m.replayBytes.Add(n)
	}
}
