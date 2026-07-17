package v1

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// FrameError wraps a framing failure with a protocol ErrorCode so the transport
// can map it directly onto an ErrorBody without re-classifying.
type FrameError struct {
	Code ErrorCode
	msg  string
}

func (e *FrameError) Error() string { return string(e.Code) + ": " + e.msg }

func frameErr(code ErrorCode, format string, args ...any) *FrameError {
	return &FrameError{Code: code, msg: fmt.Sprintf(format, args...)}
}

// WriteFrame writes one length-prefixed frame: headerLen||header||bodyLen||body.
// header must be non-empty JSON within MaxHeaderBytes; body may be nil/empty and
// must be within MaxBodyBytes. It performs a single buffered write per section.
func WriteFrame(w io.Writer, header, body []byte) error {
	if len(header) == 0 {
		return frameErr(ErrInvalidArgument, "empty frame header")
	}
	if len(header) > MaxHeaderBytes {
		return frameErr(ErrResourceExhausted, "header %d exceeds MaxHeaderBytes %d", len(header), MaxHeaderBytes)
	}
	if len(body) > MaxBodyBytes {
		return frameErr(ErrResourceExhausted, "body %d exceeds MaxBodyBytes %d", len(body), MaxBodyBytes)
	}
	var prefix [4]byte
	binary.BigEndian.PutUint32(prefix[:], uint32(len(header)))
	if _, err := w.Write(prefix[:]); err != nil {
		return err
	}
	if _, err := w.Write(header); err != nil {
		return err
	}
	binary.BigEndian.PutUint32(prefix[:], uint32(len(body)))
	if _, err := w.Write(prefix[:]); err != nil {
		return err
	}
	if len(body) > 0 {
		if _, err := w.Write(body); err != nil {
			return err
		}
	}
	return nil
}

// ReadFrame reads one frame, validating both length prefixes against the limits
// BEFORE allocating. A truncated stream returns io.ErrUnexpectedEOF; an
// oversize prefix returns a resource_exhausted FrameError; a zero header length
// returns invalid_argument. Returns (header, body, err); body is nil when empty.
func ReadFrame(r io.Reader) (header, body []byte, err error) {
	var prefix [4]byte

	if _, err = io.ReadFull(r, prefix[:]); err != nil {
		// A clean io.EOF on the very first read means the peer closed the stream
		// between frames; pass it through so callers can distinguish that from a
		// truncated frame (mid-frame reads normalize to ErrUnexpectedEOF below).
		return nil, nil, err
	}
	hlen := binary.BigEndian.Uint32(prefix[:])
	if hlen == 0 {
		return nil, nil, frameErr(ErrInvalidArgument, "zero-length frame header")
	}
	if hlen > MaxHeaderBytes {
		return nil, nil, frameErr(ErrResourceExhausted, "header length %d exceeds MaxHeaderBytes %d", hlen, MaxHeaderBytes)
	}
	header = make([]byte, hlen)
	if _, err = io.ReadFull(r, header); err != nil {
		return nil, nil, normalizeEOF(err)
	}

	if _, err = io.ReadFull(r, prefix[:]); err != nil {
		return nil, nil, normalizeEOF(err)
	}
	blen := binary.BigEndian.Uint32(prefix[:])
	if blen > MaxBodyBytes {
		return nil, nil, frameErr(ErrResourceExhausted, "body length %d exceeds MaxBodyBytes %d", blen, MaxBodyBytes)
	}
	if blen == 0 {
		return header, nil, nil
	}
	body = make([]byte, blen)
	if _, err = io.ReadFull(r, body); err != nil {
		return nil, nil, normalizeEOF(err)
	}
	return header, body, nil
}

// normalizeEOF maps a mid-frame EOF to ErrUnexpectedEOF so callers can tell a
// clean connection close (io.EOF before any bytes) from a truncated frame.
func normalizeEOF(err error) error {
	if errors.Is(err, io.EOF) {
		return io.ErrUnexpectedEOF
	}
	return err
}
