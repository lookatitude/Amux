package snapshot

import (
	"github.com/amux-run/amux/internal/persist"
)

// SurfaceClassification is one surface's restore verdict: the exact-one-of
// live|restarted|stopped class plus the reason no UI may omit (ADR-0005
// restore classification; spec success criterion 5).
type SurfaceClassification struct {
	// Surface is the SurfaceRuntime.Surface ID the verdict applies to.
	Surface string
	Class   persist.SurfaceClass
	Reason  string
}

// ClassifySurfaces classifies every surface recorded in a loaded checkpoint by
// delegating each to persist.Classify — the frozen ADR-0005 precedence
// (validation error -> stopped; live only for an in-daemon restore still
// owning the identical PTY identity; restarted only under an explicit
// automatic policy with a launchable executable and cwd; otherwise stopped).
//
// probe gathers the per-surface runtime evidence (does argv[0] resolve, does
// the cwd exist, is the PTY identity still owned). A nil probe supplies
// zero-valued evidence, so every surface fails closed to stopped. Because
// persist.Classify structurally excludes a FreshDaemon context from ClassLive,
// no probe result — honest or lying — can make a fresh daemon claim a process
// survived; the classify tests pin that here against regression.
func ClassifySurfaces(ctx persist.RestoreContext, doc *GraphDoc, probe func(SurfaceRuntime) persist.SurfaceRestoreInput) []SurfaceClassification {
	if doc == nil {
		return nil
	}
	out := make([]SurfaceClassification, 0, len(doc.Surfaces))
	for _, sr := range doc.Surfaces {
		var in persist.SurfaceRestoreInput
		if probe != nil {
			in = probe(sr)
		}
		class, reason := persist.Classify(ctx, in)
		out = append(out, SurfaceClassification{Surface: sr.Surface, Class: class, Reason: reason})
	}
	return out
}
