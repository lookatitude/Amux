package v1

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Protocol version. Major is breaking; minor is additive. This build implements
// exactly major 1 and up to minor 1. ADR-0003 owns the evolution rules.
//
// Minor 1 (additive, T4 contract completion for T5): the read-only projection
// methods surface.cells, hook.inspect, pane.context, and workspace.tree, plus
// the opt-in attach param "cells" (the attach_snapshot event payload carries a
// "cells" grid only when requested). Minor-0 peers are unaffected: they never
// send the new param or call the new methods, and no existing payload shape
// changed.
const (
	Major = 1
	Minor = 1
)

// Frame limits. Validated before allocation on every read.
const (
	// MaxHeaderBytes bounds the JSON header of any frame (1 MiB). Headers are
	// small; this only exists to fail closed on a hostile length prefix.
	MaxHeaderBytes = 1 << 20
	// MaxBodyBytes bounds a single frame's raw body (8 MiB). Terminal output is
	// chunked into frames under this bound; it is independent of the per-surface
	// 16 MiB replay floor, which spans many frames.
	MaxBodyBytes = 8 << 20
)

// MessageType is the header "type" discriminator.
type MessageType string

const (
	TypeHello    MessageType = "hello"
	TypeWelcome  MessageType = "welcome"
	TypeRequest  MessageType = "request"
	TypeResponse MessageType = "response"
	TypeEvent    MessageType = "event"
)

// ErrorCode is the stable, machine-readable error taxonomy (ADR-0003). It
// mirrors internal/domain.ErrorCode plus the transport/trust codes the domain
// layer never sees. Human messages are diagnostics, never automation contracts.
type ErrorCode string

const (
	ErrInvalidArgument      ErrorCode = "invalid_argument"
	ErrNotFound             ErrorCode = "not_found"
	ErrConflict             ErrorCode = "conflict"
	ErrUnsupportedVersion   ErrorCode = "unsupported_version"
	ErrNotInputLeaseHolder  ErrorCode = "not_input_lease_holder"
	ErrEventGap             ErrorCode = "event_gap"
	ErrReplayGap            ErrorCode = "replay_gap"
	ErrProjectTrustRequired ErrorCode = "project_trust_required"
	ErrHookGrantRequired    ErrorCode = "hook_grant_required"
	ErrHookGrantStale       ErrorCode = "hook_grant_stale"
	ErrScopeDenied          ErrorCode = "scope_denied"
	ErrResourceExhausted    ErrorCode = "resource_exhausted"
	ErrInternal             ErrorCode = "internal"
)

// AllErrorCodes is the frozen, exhaustive taxonomy. The golden test pins it so a
// code can never be silently added or removed without updating the contract.
var AllErrorCodes = []ErrorCode{
	ErrInvalidArgument, ErrNotFound, ErrConflict, ErrUnsupportedVersion,
	ErrNotInputLeaseHolder, ErrEventGap, ErrReplayGap, ErrProjectTrustRequired,
	ErrHookGrantRequired, ErrHookGrantStale, ErrScopeDenied, ErrResourceExhausted,
	ErrInternal,
}

// Hello is the client's opening frame. It advertises the highest protocol
// version the client speaks and its capability set.
type Hello struct {
	Type         MessageType `json:"type"`
	Major        int         `json:"major"`
	Minor        int         `json:"minor"`
	Client       string      `json:"client"`
	Capabilities []string    `json:"capabilities,omitempty"`
}

// Welcome is the server's negotiated response. Major/Minor are the negotiated
// version (server minor is min(server, client) within a shared major); BootID
// identifies this daemon incarnation so clients detect restarts.
type Welcome struct {
	Type   MessageType `json:"type"`
	Major  int         `json:"major"`
	Minor  int         `json:"minor"`
	BootID string      `json:"boot_id"`
	Server string      `json:"server"`
}

// Request is a bounded command invocation. Params is a raw JSON message decoded
// strictly by the command layer (DecodeStrict). DeadlineMS is an optional client
// deadline hint in milliseconds.
type Request struct {
	Type       MessageType     `json:"type"`
	ID         string          `json:"id"`
	Method     string          `json:"method"`
	DeadlineMS int64           `json:"deadline_ms,omitempty"`
	Params     json.RawMessage `json:"params,omitempty"`
}

// Response references its Request ID. Exactly one of Result or Error is set;
// Result carries the committed revision-bearing payload, Error the typed failure.
type Response struct {
	Type   MessageType     `json:"type"`
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *ErrorBody      `json:"error,omitempty"`
}

// Event is a committed, sequence-numbered notification. BootID + Session + Seq
// form the total order a subscriber tracks; a gap is a typed event_gap boundary
// requiring a fresh snapshot and cursor (ADR-0004).
type Event struct {
	Type    MessageType     `json:"type"`
	BootID  string          `json:"boot_id"`
	Session string          `json:"session"`
	Seq     uint64          `json:"seq"`
	Event   string          `json:"event"`
	TimeMS  int64           `json:"time_ms"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// ErrorBody is the wire error. Retryable tells a client whether a bare retry
// could succeed; Details is optional structured context.
type ErrorBody struct {
	Code      ErrorCode       `json:"code"`
	Message   string          `json:"message"`
	Retryable bool            `json:"retryable"`
	Details   json.RawMessage `json:"details,omitempty"`
}

// Negotiate implements the ADR-0003 version handshake. server is this build's
// (Major, Minor); client is what the peer advertised. It returns the negotiated
// (major, minor) or an ErrorBody with code unsupported_version.
func Negotiate(serverMajor, serverMinor, clientMajor, clientMinor int) (int, int, *ErrorBody) {
	if clientMajor != serverMajor {
		return 0, 0, &ErrorBody{
			Code:      ErrUnsupportedVersion,
			Message:   fmt.Sprintf("unsupported protocol major: server=%d client=%d", serverMajor, clientMajor),
			Retryable: false,
		}
	}
	minor := serverMinor
	if clientMinor < minor {
		minor = clientMinor
	}
	if minor < 0 {
		return 0, 0, &ErrorBody{Code: ErrInvalidArgument, Message: "negative minor version", Retryable: false}
	}
	return clientMajor, minor, nil
}

// DecodeStrict decodes a durable/contract payload and REJECTS unknown fields. Use
// it for command Params and any persisted structure (PRD F5/F10 unknown-field
// policy). It also rejects trailing data after the JSON value.
func DecodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return fmt.Errorf("unexpected trailing data after JSON value")
	}
	return nil
}

// DecodeLenient decodes an envelope header, IGNORING unknown additive fields so
// an older peer tolerates a newer peer's minor-version extensions.
func DecodeLenient(data []byte, v any) error {
	return json.Unmarshal(data, v)
}
