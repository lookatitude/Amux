// Package a11y is the terminal UI's accessibility and capability layer (U7):
// color/motion/size capability negotiation, the minimum-size fallback frame,
// keyboard-only help/discovery, and the pointer to the noninteractive CLI
// alternative. It is pure and env-driven so behaviour is testable and honours
// the de-facto standards (NO_COLOR, TERM) without a live terminal.
package a11y

import (
	"fmt"
	"sort"
	"strings"

	"github.com/amux-run/amux/internal/tui/geometry"
	"github.com/amux-run/amux/internal/tui/keys"
	"github.com/amux-run/amux/internal/tui/model"
	"github.com/amux-run/amux/internal/tui/render"
)

// ColorLevel is the negotiated color capability.
type ColorLevel int

const (
	NoColor      ColorLevel = iota // NO_COLOR or a dumb terminal → monochrome focus path
	LimitedColor                   // 16-color terminal
	FullColor                      // 256/truecolor
)

// Profile is the resolved accessibility/capability profile.
type Profile struct {
	Color         ColorLevel
	ReducedMotion bool
	ASCIIOnly     bool // draw borders with ASCII (screen-reader / legacy terminals)
	MinCols       int
	MinRows       int
}

// DefaultProfile is the full-fidelity profile with a conventional 20×5 floor.
func DefaultProfile() Profile {
	return Profile{Color: FullColor, MinCols: 20, MinRows: 5}
}

// Env is the subset of environment the profile reads (injected for testability).
type Env struct {
	NoColor       bool   // NO_COLOR present (any value)
	Term          string // TERM
	ColorTerm     string // COLORTERM
	ReducedMotion bool   // AMUX_REDUCED_MOTION truthy
	ASCIIOnly     bool   // AMUX_ASCII truthy
}

// Resolve derives a Profile from env, applying the de-facto rules: NO_COLOR or
// TERM=dumb/"" forces the monochrome path; truecolor/256 TERMs unlock full
// color; everything else is limited color.
func Resolve(e Env) Profile {
	p := DefaultProfile()
	p.ReducedMotion = e.ReducedMotion
	p.ASCIIOnly = e.ASCIIOnly
	switch {
	case e.NoColor, e.Term == "", e.Term == "dumb":
		p.Color = NoColor
	case e.ColorTerm == "truecolor" || strings.Contains(e.Term, "256") || strings.Contains(e.Term, "truecolor"):
		p.Color = FullColor
	default:
		p.Color = LimitedColor
	}
	return p
}

// RenderOptions maps the profile onto render.Options so a single profile drives
// every frame consistently.
func (p Profile) RenderOptions() render.Options {
	return render.Options{
		Mono:          p.Color == NoColor,
		ASCIIBorders:  p.ASCIIOnly || p.Color == NoColor,
		ReducedMotion: p.ReducedMotion,
	}
}

// Fits reports whether a cols×rows terminal meets the profile's minimums.
func (p Profile) Fits(cols, rows int) bool {
	return cols >= p.MinCols && rows >= p.MinRows
}

// LayoutConfig maps the profile onto a geometry.Config. Borders stay enabled on
// every path (they are drawn with attributes on the monochrome path and carry
// focus/state affordances beyond color), with a 1×1 minimum content floor.
func (p Profile) LayoutConfig() geometry.Config {
	return geometry.Config{Border: true, MinContentW: 1, MinContentH: 1}
}

// MinSizeFrame renders the minimum-size fallback: a centered, plain message
// telling the operator the required size and the current size. It never draws
// panes (which would corrupt), and it is legible on a monochrome screen.
func (p Profile) MinSizeFrame(cols, rows int) string {
	msg := fmt.Sprintf("terminal too small: need %d×%d, have %d×%d", p.MinCols, p.MinRows, cols, rows)
	alt := "resize, or use the noninteractive CLI (amux --help)"
	sc := render.NewScreen(rows, cols)
	if rows >= 1 {
		sc.DrawText(0, 0, clip(msg, cols), model.Style{})
	}
	if rows >= 2 {
		sc.DrawText(0, 1, clip(alt, cols), model.Style{})
	}
	return sc.PlainString()
}

func clip(s string, w int) string {
	r := []rune(s)
	if len(r) <= w || w <= 0 {
		return s
	}
	return string(r[:w])
}

// HelpLines renders keyboard-only discovery for a mode: every binding as
// "key  action", sorted, so the help overlay is complete and navigable without
// a mouse. It also names the mode and the CLI alternative.
func HelpLines(km keys.Keymap, mode keys.Mode) []string {
	lines := []string{fmt.Sprintf("%s mode — key bindings", mode)}
	bindings := km.BindingsFor(mode)
	rows := make([]string, 0, len(bindings))
	for _, b := range bindings {
		rows = append(rows, fmt.Sprintf("  %-10s %s", b.Key.Canonical(), b.Action))
	}
	sort.Strings(rows)
	lines = append(lines, rows...)
	if mode == keys.Passthrough {
		lines = append(lines, fmt.Sprintf("  %-10s open command prefix", km.Prefix.Canonical()))
	}
	lines = append(lines, "", "Non-interactive alternative: every action maps to an `amux` subcommand (see docs/tui.md).")
	return lines
}
