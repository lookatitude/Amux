package daemon

import (
	"encoding/base64"
	"errors"

	"github.com/amux-run/amux/internal/domain"
	"github.com/amux-run/amux/internal/session"
	"github.com/amux-run/amux/internal/terminal"
)

// sid converts a wire session id to the domain type.
func sid(s string) domain.SessionID { return domain.SessionID(s) }

// b64 encodes raw output bytes for the JSON wire (control frames carry replay
// as base64; live attach uses raw-body frames instead, ADR-0003).
func b64(p []byte) string { return base64.StdEncoding.EncodeToString(p) }

// isReplayGap reports whether err is a terminal.ReplayGapError and binds it.
func isReplayGap(err error, target **terminal.ReplayGapError) bool {
	return errors.As(err, target)
}

// isEventGap reports whether err is a session.EventGapError and binds it.
func isEventGap(err error, target **session.EventGapError) bool {
	return errors.As(err, target)
}
