package render

import "github.com/amux-run/amux/internal/tui/model"

// styleSet is the resolved palette for one render, derived from Options so the
// monochrome path expresses state with attributes only (no color).
type styleSet struct {
	border      model.Style
	borderFocus model.Style
	title       model.Style
	titleFocus  model.Style
	status      model.Style
	warn        model.Style
	cursor      model.Style
}

func resolveStyles(opts Options) styleSet {
	if opts.Mono {
		// Monochrome: focus/state via bold + reverse, never color.
		return styleSet{
			border:      model.Style{},
			borderFocus: model.Style{Attrs: model.AttrBold},
			title:       model.Style{},
			titleFocus:  model.Style{Attrs: model.AttrBold},
			status:      model.Style{Attrs: model.AttrFaint},
			warn:        model.Style{Attrs: model.AttrBold | model.AttrReverse},
			cursor:      model.Style{Attrs: model.AttrReverse},
		}
	}
	cyan := model.Color{Mode: model.ColorANSI, Index: 6}
	bright := model.Color{Mode: model.ColorANSI, Index: 14}
	gray := model.Color{Mode: model.ColorANSI, Index: 8}
	yellow := model.Color{Mode: model.ColorANSI, Index: 11}
	return styleSet{
		border:      model.Style{FG: gray},
		borderFocus: model.Style{FG: bright, Attrs: model.AttrBold},
		title:       model.Style{FG: cyan},
		titleFocus:  model.Style{FG: bright, Attrs: model.AttrBold},
		status:      model.Style{FG: gray, Attrs: model.AttrFaint},
		warn:        model.Style{FG: yellow, Attrs: model.AttrBold},
		cursor:      model.Style{Attrs: model.AttrReverse},
	}
}

// borderRuneSet is one box-drawing set.
type borderRuneSet struct {
	tl, tr, bl, br, h, v string
}

func borderRunes(ascii, focused bool) borderRuneSet {
	if ascii {
		return borderRuneSet{tl: "+", tr: "+", bl: "+", br: "+", h: "-", v: "|"}
	}
	if focused {
		// heavy box for the focused pane (extra affordance beyond color)
		return borderRuneSet{tl: "┏", tr: "┓", bl: "┗", br: "┛", h: "━", v: "┃"}
	}
	return borderRuneSet{tl: "┌", tr: "┐", bl: "└", br: "┘", h: "─", v: "│"}
}
