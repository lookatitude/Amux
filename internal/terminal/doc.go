// Package terminal implements the per-surface raw replay ring and the derived
// VT cell engine (T4 B6; package map ADR-0001 — nothing inward of terminal may
// import it).
//
// Authority: the raw output byte stream is canonical (ADR-0005). Ring retains
// a bounded tail of immutable, sequence-numbered chunks; when a requested
// replay cursor has been evicted the boundary is an explicit typed
// ReplayGapError (ADR-0004 — gaps are never bridged silently; the caller
// snapshots instead). Replay always ends exactly at the newest retained
// sequence, matching the attach contract's snapshot-at-N → replay-to-N → live
// >N cutover.
//
// The Engine's cell grid is DERIVED state (ADR-0005): it is rebuilt
// deterministically from raw bytes and is replaceable — it never becomes an
// authority. Decoding uses github.com/charmbracelet/x/ansi v0.11.7 — the exact
// version Bubble Tea v2 / Lip Gloss v2 require, deliberately updated from the
// original v0.4.5 pin under ADR-0007 Decision 3 to unblock the T5 toolkit —
// as the pinned decoder behind this package's seam (ADR-0006/ADR-0007); no
// other package may see that library. Unknown or unsupported sequences never
// crash or desync the engine: they are counted and sampled for diagnostics and
// the stream resumes at the next token.
//
// Determinism contract: feeding the same raw bytes in any chunking produces
// the same grid, with two documented, bounded exceptions: (1) a string
// sequence (OSC/DCS/APC/PM/SOS) whose payload exceeds 64 KiB is discarded as
// unsupported under the same size rule on every path, and (2) a grapheme
// cluster split across Feed calls has its cell width fixed when its first
// rune is written, so a later width-changing extension (e.g. VS16) joins the
// cell's content without widening it.
package terminal
