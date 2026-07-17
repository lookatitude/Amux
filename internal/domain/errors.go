package domain

// ErrorCode is the stable machine-readable code carried by every domain and
// protocol error. The set here is the domain-relevant subset of the frozen
// protocol error taxonomy (ADR-0003 / PRD "Error families"). Human messages are
// diagnostics, never automation contracts; callers branch on Code only.
type ErrorCode string

const (
	// CodeInvalidArgument: a command field is malformed or out of range.
	CodeInvalidArgument ErrorCode = "invalid_argument"
	// CodeNotFound: a referenced workspace, pane, or surface does not exist.
	CodeNotFound ErrorCode = "not_found"
	// CodeConflict: the command is well-formed but violates a graph invariant
	// in the current state (e.g. closing the last pane of a workspace).
	CodeConflict ErrorCode = "conflict"
	// CodeInternal: an invariant the domain guarantees was violated. This must
	// never be returned to a well-behaved caller; it signals a domain bug and is
	// the code Check() uses. Surfacing it fails closed.
	CodeInternal ErrorCode = "internal"
)

// Error is the domain's typed error. It maps directly onto the protocol error
// envelope (ADR-0003): Code -> code, Message -> message. Retryability and
// structured details are added at the protocol boundary, not here.
type Error struct {
	Code    ErrorCode
	Message string
}

func (e *Error) Error() string { return string(e.Code) + ": " + e.Message }

func newError(code ErrorCode, msg string) *Error { return &Error{Code: code, Message: msg} }

// AsError reports whether err is a *Error and returns it. It lets callers and
// tests assert on Code without string matching.
func AsError(err error) (*Error, bool) {
	de, ok := err.(*Error)
	return de, ok
}

// CodeOf returns the ErrorCode of err, or "" if err is not a domain *Error.
func CodeOf(err error) ErrorCode {
	if de, ok := err.(*Error); ok {
		return de.Code
	}
	return ""
}
