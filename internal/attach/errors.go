package attach

import (
	"errors"

	v1 "github.com/amux-run/amux/api/v1"
)

var (
	// ErrNotLeaseHolder rejects a Write from a client that does not hold the
	// surface's input lease, BEFORE any byte reaches the sink (ADR-0004). It
	// maps to the wire code v1.ErrNotInputLeaseHolder via ErrorCode.
	ErrNotLeaseHolder = errors.New("attach: not input lease holder")
	// ErrInputLeaseHeld rejects an AcquireInput while another client holds the
	// lease. Acquire never implicitly takes over — the caller must call
	// TakeoverInput deliberately (ADR-0004 §input-lease state machine).
	ErrInputLeaseHeld = errors.New("attach: input lease held by another client")
	// ErrSlowConsumer is the receipt error on an attachment whose bounded live
	// buffer overflowed; it is disconnected while the surface stays healthy
	// (ADR-0004 slow-consumer boundary). Read Attachment.LastDelivered to
	// resume via bounded replay or a fresh snapshot.
	ErrSlowConsumer = errors.New("attach: slow consumer disconnected")
	// ErrSurfaceClosed is returned once a surface has been closed.
	ErrSurfaceClosed = errors.New("attach: surface closed")
	// ErrSurfaceExists rejects a duplicate surface id in a Manager.
	ErrSurfaceExists = errors.New("attach: surface id already registered")
	// ErrNoRing rejects a SurfaceConfig without a ring (the raw authority).
	ErrNoRing = errors.New("attach: surface requires a ring")
	// ErrNoID rejects a SurfaceConfig without an id.
	ErrNoID = errors.New("attach: surface requires an id")
)

// ErrorCode maps an attach error to its stable wire ErrorCode (ADR-0003
// taxonomy). It reports ok=false for errors with no dedicated code, which the
// caller renders as v1.ErrInternal.
func ErrorCode(err error) (v1.ErrorCode, bool) {
	switch {
	case errors.Is(err, ErrNotLeaseHolder):
		return v1.ErrNotInputLeaseHolder, true
	case errors.Is(err, ErrSlowConsumer):
		return v1.ErrResourceExhausted, true
	case errors.Is(err, ErrInputLeaseHeld):
		return v1.ErrConflict, true
	default:
		return "", false
	}
}

// Code reports the wire error code for a replay-gap boundary: a client that
// receives a ReplayGap in its snapshot should treat it as a v1.ErrReplayGap
// recovery point (ADR-0004: an explicit, typed boundary).
func (g *ReplayGap) Code() v1.ErrorCode { return v1.ErrReplayGap }
