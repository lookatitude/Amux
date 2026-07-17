package observability

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime"

	"github.com/amux-run/amux/internal/platform"
)

// DumpMaxBytes caps one encoded diagnostic dump at 256 KiB. The cap is a
// bound, not a truncation point: a dump that would exceed it fails closed with
// *DumpTooLargeError and writes nothing, so a consumer never sees a JSON
// document cut off mid-stream.
const DumpMaxBytes = 256 * 1024

// DumpTooLargeError reports that the fully encoded dump exceeded DumpMaxBytes.
type DumpTooLargeError struct {
	// Size is the encoded size in bytes of the rejected dump.
	Size int
	// Max is the cap that was exceeded (DumpMaxBytes).
	Max int
}

func (e *DumpTooLargeError) Error() string {
	return fmt.Sprintf("observability: diagnostic dump is %d bytes, above the %d-byte cap (nothing written)", e.Size, e.Max)
}

// DumpInput carries everything a diagnostic dump needs. Clock is mandatory —
// the timestamp is always injected (deterministic in tests), never read from
// the ambient wall clock. Metrics and Extra are optional.
type DumpInput struct {
	// BootID identifies the daemon boot producing the dump.
	BootID string
	// Version is the daemon version string.
	Version string
	// Clock supplies the dump timestamp. Required.
	Clock platform.Clock
	// Metrics, when non-nil, contributes its Snapshot as the "metrics" section.
	Metrics *Registry
	// Extra, when non-nil, is emitted verbatim as the "extra" section. Values
	// must be json-encodable; map keys are sorted by encoding/json, so a dump
	// of identical input is byte-deterministic.
	Extra map[string]any
}

// dumpDoc is the single JSON document Dump emits. Struct fields encode in
// declaration order and map keys encode sorted, so the layout is deterministic.
type dumpDoc struct {
	TimestampUnixMs int64            `json:"timestamp_unix_ms"`
	BootID          string           `json:"boot_id"`
	Version         string           `json:"version"`
	Runtime         dumpRuntimeStats `json:"runtime"`
	Metrics         map[string]int64 `json:"metrics"`
	Extra           map[string]any   `json:"extra,omitempty"`
}

// dumpRuntimeStats is the bounded Go runtime subset worth having in every
// dump: goroutine count plus the runtime.MemStats fields that explain memory
// posture without dragging in the full (huge) struct.
type dumpRuntimeStats struct {
	NumGoroutine    int    `json:"num_goroutine"`
	HeapAllocBytes  uint64 `json:"heap_alloc_bytes"`
	HeapSysBytes    uint64 `json:"heap_sys_bytes"`
	HeapObjects     uint64 `json:"heap_objects"`
	StackInuseBytes uint64 `json:"stack_inuse_bytes"`
	TotalAllocBytes uint64 `json:"total_alloc_bytes"`
	SysBytes        uint64 `json:"sys_bytes"`
	NumGC           uint32 `json:"num_gc"`
	LastGCUnixNs    uint64 `json:"last_gc_unix_ns"`
}

// Dump writes one bounded JSON document (terminated by a newline) describing
// the daemon's current diagnostic state: injected timestamp, boot identity,
// version, Go runtime stats, the metrics snapshot, and caller-supplied extra
// sections. The document is fully encoded and size-checked before a single
// byte reaches w; an over-cap dump returns *DumpTooLargeError and writes
// nothing.
func Dump(w io.Writer, in DumpInput) error {
	if in.Clock == nil {
		return errors.New("observability: Dump requires an injected platform.Clock")
	}
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	doc := dumpDoc{
		TimestampUnixMs: in.Clock.NowUnixMilli(),
		BootID:          in.BootID,
		Version:         in.Version,
		Runtime: dumpRuntimeStats{
			NumGoroutine:    runtime.NumGoroutine(),
			HeapAllocBytes:  mem.HeapAlloc,
			HeapSysBytes:    mem.HeapSys,
			HeapObjects:     mem.HeapObjects,
			StackInuseBytes: mem.StackInuse,
			TotalAllocBytes: mem.TotalAlloc,
			SysBytes:        mem.Sys,
			NumGC:           mem.NumGC,
			LastGCUnixNs:    mem.LastGC,
		},
		Metrics: map[string]int64{},
		Extra:   in.Extra,
	}
	if in.Metrics != nil {
		doc.Metrics = in.Metrics.Snapshot()
	}
	encoded, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("observability: encoding diagnostic dump: %w", err)
	}
	encoded = append(encoded, '\n')
	if len(encoded) > DumpMaxBytes {
		return &DumpTooLargeError{Size: len(encoded), Max: DumpMaxBytes}
	}
	if _, err := w.Write(encoded); err != nil {
		return fmt.Errorf("observability: writing diagnostic dump: %w", err)
	}
	return nil
}
