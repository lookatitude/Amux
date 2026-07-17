package observability

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/amux-run/amux/internal/platform"
)

func testDumpInput() DumpInput {
	r := NewRegistry()
	r.Counter(MetricPTYSpawns).Add(4)
	r.Gauge(MetricPTYLive).Set(2)
	return DumpInput{
		BootID:  "boot-abc",
		Version: "0.1.0-test",
		Clock:   platform.NewFakeClock(1_752_500_000_000),
		Metrics: r,
		Extra: map[string]any{
			"sessions": 3,
			"attach":   map[string]any{"observers": 1},
		},
	}
}

// TestDumpWritesSingleBoundedJSONDocument asserts Dump emits exactly one JSON
// object carrying the injected timestamp, identity, runtime stats, the metrics
// snapshot, and the extra sections.
func TestDumpWritesSingleBoundedJSONDocument(t *testing.T) {
	var buf bytes.Buffer
	if err := Dump(&buf, testDumpInput()); err != nil {
		t.Fatalf("Dump: %v", err)
	}
	if buf.Len() > DumpMaxBytes {
		t.Fatalf("dump is %d bytes, above the %d cap", buf.Len(), DumpMaxBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(buf.Bytes()))
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("dump is not valid JSON: %v", err)
	}
	if dec.More() {
		t.Fatal("dump contains more than one JSON document")
	}
	if got := doc["timestamp_unix_ms"]; got != float64(1_752_500_000_000) {
		t.Errorf("timestamp_unix_ms = %v, want injected fake-clock value", got)
	}
	if got := doc["boot_id"]; got != "boot-abc" {
		t.Errorf("boot_id = %v, want %q", got, "boot-abc")
	}
	if got := doc["version"]; got != "0.1.0-test" {
		t.Errorf("version = %v, want %q", got, "0.1.0-test")
	}
	rt, ok := doc["runtime"].(map[string]any)
	if !ok {
		t.Fatalf("runtime section missing or wrong shape: %v", doc["runtime"])
	}
	if n, ok := rt["num_goroutine"].(float64); !ok || n < 1 {
		t.Errorf("runtime.num_goroutine = %v, want >= 1", rt["num_goroutine"])
	}
	if _, ok := rt["heap_alloc_bytes"]; !ok {
		t.Errorf("runtime.heap_alloc_bytes missing: %v", rt)
	}
	metrics, ok := doc["metrics"].(map[string]any)
	if !ok {
		t.Fatalf("metrics section missing or wrong shape: %v", doc["metrics"])
	}
	if got := metrics[MetricPTYSpawns]; got != float64(4) {
		t.Errorf("metrics[%q] = %v, want 4", MetricPTYSpawns, got)
	}
	if got := metrics[MetricPTYLive]; got != float64(2) {
		t.Errorf("metrics[%q] = %v, want 2", MetricPTYLive, got)
	}
	extra, ok := doc["extra"].(map[string]any)
	if !ok {
		t.Fatalf("extra section missing or wrong shape: %v", doc["extra"])
	}
	if got := extra["sessions"]; got != float64(3) {
		t.Errorf("extra.sessions = %v, want 3", got)
	}
}

// TestDumpDeterministic asserts two dumps of identical input are byte-identical
// (deterministic key order), modulo the live runtime section — so the runtime
// stats are pinned by comparing the surrounding structure via re-marshal.
func TestDumpDeterministic(t *testing.T) {
	strip := func(raw []byte) string {
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		delete(doc, "runtime") // live process stats legitimately vary
		out, err := json.Marshal(doc)
		if err != nil {
			t.Fatalf("re-marshal: %v", err)
		}
		return string(out)
	}
	var a, b bytes.Buffer
	if err := Dump(&a, testDumpInput()); err != nil {
		t.Fatalf("first Dump: %v", err)
	}
	if err := Dump(&b, testDumpInput()); err != nil {
		t.Fatalf("second Dump: %v", err)
	}
	if strip(a.Bytes()) != strip(b.Bytes()) {
		t.Errorf("dumps of identical input differ:\n%s\n%s", a.String(), b.String())
	}
}

// TestDumpTooLargeFailsClosed asserts the 256 KiB cap: an over-cap dump returns
// the typed error and writes NOTHING — the document is never truncated mid-JSON.
func TestDumpTooLargeFailsClosed(t *testing.T) {
	in := testDumpInput()
	in.Extra = map[string]any{"blob": strings.Repeat("x", DumpMaxBytes)}
	var buf bytes.Buffer
	err := Dump(&buf, in)
	if err == nil {
		t.Fatal("Dump above the cap succeeded, want typed error")
	}
	var tooLarge *DumpTooLargeError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("error is %T (%v), want *DumpTooLargeError", err, err)
	}
	if tooLarge.Size <= tooLarge.Max || tooLarge.Max != DumpMaxBytes {
		t.Errorf("DumpTooLargeError{Size: %d, Max: %d} inconsistent with cap %d",
			tooLarge.Size, tooLarge.Max, DumpMaxBytes)
	}
	if buf.Len() != 0 {
		t.Errorf("over-cap Dump wrote %d bytes, want 0 (fail closed, no partial JSON)", buf.Len())
	}
}

// TestDumpRequiresClock asserts the clock is injected, never defaulted: a nil
// Clock fails closed so no dump silently reads the ambient wall clock.
func TestDumpRequiresClock(t *testing.T) {
	in := testDumpInput()
	in.Clock = nil
	var buf bytes.Buffer
	if err := Dump(&buf, in); err == nil {
		t.Fatal("Dump with nil Clock succeeded, want error")
	}
	if buf.Len() != 0 {
		t.Errorf("failed Dump wrote %d bytes, want 0", buf.Len())
	}
}

// TestDumpNilRegistryAndExtra asserts the optional sections degrade to empty,
// not to a panic or an invalid document.
func TestDumpNilRegistryAndExtra(t *testing.T) {
	in := testDumpInput()
	in.Metrics = nil
	in.Extra = nil
	var buf bytes.Buffer
	if err := Dump(&buf, in); err != nil {
		t.Fatalf("Dump: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("dump is not valid JSON: %v", err)
	}
}
