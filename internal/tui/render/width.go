package render

import "github.com/rivo/uniseg"

// This file isolates Unicode segmentation/width for STATUS and border text
// (titles, cwd, process, status glyphs). Grid CELL widths are authoritative
// from the backend snapshot (model.Cell.Width) and are never recomputed here,
// so client-side width logic can never corrupt the daemon-derived grid — it
// only measures the decoration strings the TUI itself composes.
//
// uniseg is a pinned, permissive (MIT), cgo-free dependency already in the
// module graph (grapheme widths behind x/ansi); using it directly avoids
// re-implementing UAX #29 segmentation and east-asian width tables.

// graphemes splits s into grapheme clusters (so combining marks stay attached
// to their base and a wide CJK glyph is one cluster).
func graphemes(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	rest := s
	state := -1
	for len(rest) > 0 {
		var cluster string
		cluster, rest, _, state = uniseg.FirstGraphemeClusterInString(rest, state)
		out = append(out, cluster)
	}
	return out
}

// displayWidth returns the monospace column width of a single grapheme cluster
// (0 for a pure combining sequence, 1 for a normal glyph, 2 for a wide glyph).
func displayWidth(cluster string) int {
	if cluster == "" {
		return 0
	}
	_, _, w, _ := uniseg.FirstGraphemeClusterInString(cluster, -1)
	return w
}

// stringWidth returns the total monospace width of s.
func stringWidth(s string) int { return uniseg.StringWidth(s) }

// truncate shortens s to at most w display columns, appending a single-column
// ellipsis "…" when it had to cut (so status text never overflows a border and
// corrupts geometry). w<=0 yields "".
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if stringWidth(s) <= w {
		return s
	}
	if w == 1 {
		return "…"
	}
	budget := w - 1 // reserve one column for the ellipsis
	var out []byte
	used := 0
	rest := s
	state := -1
	for len(rest) > 0 {
		var cluster string
		var cw int
		cluster, rest, cw, state = uniseg.FirstGraphemeClusterInString(rest, state)
		if used+cw > budget {
			break
		}
		out = append(out, cluster...)
		used += cw
	}
	return string(out) + "…"
}
