// chrome.go composes the production frame with Lip Gloss v2. It is the ONE
// place the TUI uses Lip Gloss, and it uses it only for geometry-safe *chrome*
// — the status bar and the minimum-size fallback — never for the authoritative
// cell grid (whose widths are backend-owned; re-measuring them through a styler
// would risk disagreeing with the daemon and would blow the redraw budget). The
// deliberate split is: the pure renderer projects backend cells with their
// authoritative widths; Lip Gloss styles the surrounding chrome with width-
// clamped styles that degrade to plain output under monochrome/no-color.
package teabridge

import (
	"fmt"
	"strings"

	lipgloss "charm.land/lipgloss/v2"

	"github.com/amux-run/amux/internal/tui/a11y"
	tuiapp "github.com/amux-run/amux/internal/tui/app"
)

// Frame renders the full production frame for the model: either the Lip Gloss
// minimum-size fallback, or the composed pane Screen (styled cells on the color
// path, plain on the monochrome/no-color path) plus the Lip Gloss status bar.
// It is pure given the model state, so the styled path is deterministically
// testable alongside the plain golden path.
func Frame(m *tuiapp.Model) string {
	cols, rows := m.Size()
	p := m.Profile()
	if !m.Fits() {
		return MinSizeFrame(cols, rows, p)
	}
	var content string
	if p.Color == a11y.NoColor {
		// Strict NO_COLOR / monochrome: no SGR at all (parity with the pure path).
		content = m.Screen().PlainString()
	} else {
		content = m.Screen().StyledString()
	}
	return content + "\n" + StatusBar(m.StatusText(), cols, p)
}

// StatusBar styles the status line with Lip Gloss, clamped to exactly cols
// display columns (padded and truncated — geometry-safe, never overflows the
// terminal width). On the color path it is a reverse-video bar; on the
// monochrome/no-color path it renders as plain padded text (no color added).
func StatusBar(text string, cols int, p a11y.Profile) string {
	if cols <= 0 {
		return ""
	}
	st := lipgloss.NewStyle().Width(cols).MaxWidth(cols)
	if p.Color != a11y.NoColor {
		// Reverse video is a color-independent affordance that reads on every
		// palette; it keeps the bar visible without asserting a specific color.
		st = st.Reverse(true)
	}
	return st.Render(text)
}

// MinSizeFrame renders the minimum-size fallback as a cols×rows block: each
// message line is truncated to at most cols display columns and centered with
// Lip Gloss (geometry-safe — never overflows the terminal even when the frame
// is only a few cells), then padded/clipped to exactly rows lines. It never
// draws panes (which would corrupt a tiny frame) and points at the
// noninteractive CLI alternative.
func MinSizeFrame(cols, rows int, p a11y.Profile) string {
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	style := lipgloss.NewStyle().MaxWidth(cols)
	if p.Color != a11y.NoColor {
		style = style.Bold(true)
	}
	// "too small" leads so it stays legible even when truncated to a few columns.
	msgs := []string{
		fmt.Sprintf("too small: need %d×%d", p.MinCols, p.MinRows),
		fmt.Sprintf("have %d×%d — run: amux --help", cols, rows),
	}
	lines := make([]string, 0, rows)
	for _, msg := range msgs {
		lines = append(lines, lipgloss.PlaceHorizontal(cols, lipgloss.Center, style.Render(msg)))
	}
	for len(lines) < rows {
		lines = append(lines, "")
	}
	if len(lines) > rows {
		lines = lines[:rows]
	}
	return strings.Join(lines, "\n")
}
