package ordering

import "errors"

// ErrGap signals a detected discontinuity in the delivered stream — the typed
// recovery boundary a subscriber turns into a snapshot-refresh + cursor reset
// (ADR-0004). In the model it lets tests assert that a gap is reported, never
// silently bridged.
var ErrGap = errors.New("event_gap")

// AttachCutover models the attach contract: a client receives an atomic
// metadata snapshot taken at output sequence N, a bounded raw replay ending
// exactly at N, and then live output strictly after N. This function proves the
// concatenation is contiguous (no gap) and duplicate-free (no overlap) across
// the boundary — the property the real attach manager must preserve.
//
// snapshotSeq is N. replay is the sequence numbers delivered in the replay
// phase (must end at N). live is the sequence numbers delivered after cutover
// (must all be > N). It returns the fully ordered delivered sequence or ErrGap
// if the boundary is not contiguous.
func AttachCutover(snapshotSeq uint64, replay, live []uint64) ([]uint64, error) {
	out := make([]uint64, 0, len(replay)+len(live))
	var last uint64
	haveLast := false
	for _, s := range replay {
		if haveLast && s != last+1 {
			return nil, ErrGap
		}
		out = append(out, s)
		last, haveLast = s, true
	}
	// Replay must end exactly at the snapshot boundary.
	if haveLast && last != snapshotSeq {
		return nil, ErrGap
	}
	for _, s := range live {
		if s <= snapshotSeq {
			// A duplicate or out-of-order live frame at/under the boundary.
			return nil, ErrGap
		}
		if haveLast && s != last+1 {
			return nil, ErrGap
		}
		out = append(out, s)
		last, haveLast = s, true
	}
	return out, nil
}
