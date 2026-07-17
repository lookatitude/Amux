package observability

import (
	"context"
	"errors"
	"testing"
)

// TestNopTracerIsAFreeSpan asserts the default tracer returns the caller's
// context unchanged and an end func that is safe to call with and without an
// error — so call sites can instrument unconditionally today and gain real
// spans only if OTel wiring is ever approved (ADR-0007).
func TestNopTracerIsAFreeSpan(t *testing.T) {
	type key struct{}
	ctx := context.WithValue(context.Background(), key{}, "v")
	var tr Tracer = NopTracer{}
	spanCtx, end := tr.StartSpan(ctx, "test.span")
	if spanCtx != ctx {
		t.Error("NopTracer.StartSpan returned a different context")
	}
	if end == nil {
		t.Fatal("NopTracer.StartSpan returned a nil end func")
	}
	end(nil)
	end(errors.New("boom")) // idempotent and error-tolerant
}
