package observability

import "context"

// Tracer is the package's tracing seam: exactly the surface a subsystem needs
// to bracket an operation, and nothing OpenTelemetry-shaped leaks through it.
//
// This interface exists so call sites can be instrumented today with zero
// dependencies. Real OpenTelemetry wiring is an optional adapter that lands
// only if/when the go.opentelemetry.io dependency is approved under the
// ADR-0007 pin policy — adding the dependency now would violate that policy,
// so the MVP ships NopTracer as the sole implementation. An approved adapter
// would live behind this same interface (StartSpan opening an OTel span,
// the returned func recording the error and ending it) with no call-site
// changes.
type Tracer interface {
	// StartSpan begins a span named name. It returns the (possibly derived)
	// context to propagate and an end func the caller must invoke exactly once
	// when the operation completes, passing the operation's error (nil on
	// success).
	StartSpan(ctx context.Context, name string) (context.Context, func(err error))
}

// NopTracer is the default Tracer: free, allocation-light, and safe
// everywhere. It returns the caller's context unchanged and an end func that
// does nothing.
type NopTracer struct{}

// StartSpan implements Tracer as a no-op.
func (NopTracer) StartSpan(ctx context.Context, _ string) (context.Context, func(err error)) {
	return ctx, nopEnd
}

// nopEnd is shared so NopTracer spans do not allocate a closure per call.
func nopEnd(error) {}
