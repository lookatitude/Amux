package app

import (
	"fmt"
	"strings"

	"github.com/amux-run/amux/internal/tui/a11y"
	"github.com/amux-run/amux/internal/tui/geometry"
	"github.com/amux-run/amux/internal/tui/keys"
	"github.com/amux-run/amux/internal/tui/model"
	"github.com/amux-run/amux/internal/tui/render"
)

// View renders the current frame. Layout is deterministic given (size, tree,
// content, mode) so the whole UI is golden-testable. The bottom row is a status
// bar; overlays (help, notification inbox, confirmation/trust modal) are drawn
// over the pane area. This is the pure, plain (monochrome-equivalent) path the
// golden corpus and the deterministic Drive harness pin; the production Bubble
// Tea shell composes the SAME Screen with Lip Gloss chrome (see Screen()).
func (m *Model) View() string {
	if !m.Fits() {
		return m.profile.MinSizeFrame(m.cols, m.rows)
	}
	return m.Screen().PlainString() + "\n" + m.StatusText()
}

// Fits reports whether the terminal meets the profile's minimum size.
func (m *Model) Fits() bool { return m.profile.Fits(m.cols, m.rows) }

// Size returns the current terminal dimensions.
func (m *Model) Size() (cols, rows int) { return m.cols, m.rows }

// Profile returns the resolved accessibility/capability profile.
func (m *Model) Profile() a11y.Profile { return m.profile }

// MinSizeFrame returns the minimum-size fallback frame for the current size.
func (m *Model) MinSizeFrame() string { return m.profile.MinSizeFrame(m.cols, m.rows) }

// Screen composes the pane area into a Screen: deterministic layout, cell
// rendering from backend snapshots, and any active overlay (trust/confirm/
// notification/help) drawn over the panes. It reserves the bottom row for the
// status bar (see StatusText). Both View() and the production shell build on
// this single composition so the plain and styled paths never diverge.
func (m *Model) Screen() *render.Screen {
	paneRows := m.rows - 1
	if paneRows < 1 {
		paneRows = 1
	}
	opts := m.profile.RenderOptions()

	l := geometry.Compute(m.tree, m.cols, paneRows, m.profile.LayoutConfig())
	pvs := make([]render.PaneView, 0, len(l.Panes))
	for _, pl := range l.Panes {
		pvs = append(pvs, m.paneView(pl))
	}
	sc := render.Render(paneRows, m.cols, pvs, opts)

	// Overlays.
	switch {
	case m.trust != nil:
		drawPanel(sc, "confirm", m.trust.lines)
	case m.confirm != nil:
		drawPanel(sc, "confirm", m.confirm.prompt)
	case m.mode == keys.Trust:
		drawPanel(sc, "hook trust", m.trustInspectLines())
	case m.mode == keys.Notification:
		drawPanel(sc, "notifications", m.inboxLines())
	case m.mode == keys.Help:
		// Discovery lists the Prefix command vocabulary (Passthrough deliberately
		// binds nothing — every key there is real input for the process).
		drawPanel(sc, "help", a11y.HelpLines(m.keymap, keys.Prefix))
	}
	return sc
}

// StatusText is the plain status-bar text (clamped to width). The production
// shell styles this line with Lip Gloss; the pure View renders it verbatim.
func (m *Model) StatusText() string { return m.statusBar() }

func (m *Model) paneView(pl geometry.PaneLayout) render.PaneView {
	p := m.panes[pl.PaneID]
	c := m.content[pl.PaneID]
	snap := c.Snapshot
	if snap.Empty() {
		snap = model.EmptySnapshot(pl.Content.H, pl.Content.W)
	}
	title := c.Title
	if title == "" {
		title = p.ID
	}
	lease := model.LeaseNone
	if s, ok := p.ActiveSurface(); ok {
		lease = s.Lease
	}
	phase := model.PhaseIdle
	if pl.PaneID == m.focused {
		phase = m.attach.State().Phase
		if lease == model.LeaseNone {
			lease = m.attach.State().Lease
		}
	}
	return render.PaneView{
		Layout:        pl,
		Snapshot:      snap,
		Focused:       pl.PaneID == m.focused,
		Title:         title,
		Class:         c.Class,
		ExitReason:    c.ExitReason,
		Lease:         lease,
		Attach:        phase,
		Process:       p.ForegroundCmd,
		Cwd:           p.Cwd,
		GitBranch:     p.GitBranch,
		GitDirty:      p.GitDirty,
		ActiveSurface: surfaceCounter(p),
		Unread:        p.Unread,
		ShowCursor:    pl.PaneID == m.focused && c.Class != model.ClassStopped,
	}
}

func surfaceCounter(p model.Pane) string {
	if len(p.Surfaces) == 0 {
		return ""
	}
	idx := 1
	for i, s := range p.Surfaces {
		if s.ID == p.Active {
			idx = i + 1
		}
	}
	return fmt.Sprintf("%d/%d", idx, len(p.Surfaces))
}

func (m *Model) statusBar() string {
	phase := m.attach.State()
	parts := []string{
		"[" + m.mode.String() + "]",
		"focus=" + orDash(m.focused),
		"attach=" + string(phase.Phase),
		"lease=" + string(phase.Lease),
	}
	if phase.NeedsRecovery() {
		parts = append(parts, "recover="+phase.Recovery.String())
	}
	if g := phase.Gap; g != nil {
		// Replay-gap boundary straight from the attach snapshot: requested cursor
		// vs the oldest retained sequence (presentation of daemon numbers only).
		parts = append(parts, fmt.Sprintf("gap=%d<%d", g.RequestedFrom, g.OldestRetained))
	}
	if u := m.inbox.Unread(); u > 0 {
		parts = append(parts, fmt.Sprintf("unread=%d", u))
	}
	if m.health.Version != "" {
		parts = append(parts, "v"+m.health.Version)
	}
	if m.health.BootID != "" {
		parts = append(parts, "boot="+shortID(m.health.BootID))
	}
	line := strings.Join(parts, " ")
	if len([]rune(line)) > m.cols {
		line = string([]rune(line)[:m.cols])
	}
	return line
}

func (m *Model) inboxLines() []string {
	lines := []string{"Notifications (j next · enter read · d dismiss · esc close)"}
	vis := m.inbox.Visible()
	if len(vis) == 0 {
		return append(lines, "  (empty)")
	}
	cur, _ := m.inbox.Cursor()
	for _, n := range vis {
		mark := " "
		if !n.Read {
			mark = "•"
		}
		sel := " "
		if n.ID == cur.ID {
			sel = ">"
		}
		fail := ""
		if n.Delivery == model.DeliveryFailed {
			fail = " (delivery failed)"
		}
		lines = append(lines, fmt.Sprintf("%s%s [%s] %s%s", sel, mark, n.Kind, n.Title, fail))
	}
	return lines
}

// trustInspectLines renders the open hook-trust inspection: the project's
// trust state/epoch from hook.inspect plus every grant's frozen detail
// (executable, digest, events, scope, env keys, timeout). Pure display of the
// daemon projection — absent data is stated, never guessed.
func (m *Model) trustInspectLines() []string {
	const hint = "a approve · d deny · r revoke · j/k select · esc close"
	ti := m.inspect
	if ti == nil {
		return []string{"(no trust inspection open)", hint}
	}
	state := ti.state
	if state == "" {
		state = "untrusted"
	}
	lines := []string{fmt.Sprintf("Project %s — trust: %s (epoch %d)", orDash(ti.project), state, ti.epoch)}
	switch {
	case ti.err != "":
		lines = append(lines, "  ! "+ti.err)
	case !ti.loaded:
		lines = append(lines, "  (inspecting…)")
	case len(ti.grants) == 0:
		lines = append(lines, "  (no grants recorded for this project)")
	default:
		for i, g := range ti.grants {
			sel := " "
			if i == ti.cursor {
				sel = ">"
			}
			lines = append(lines,
				fmt.Sprintf("%s %s exec=%s sha256=%s active=%v", sel, orDash(g.HookID), orDash(g.Executable), orDash(g.Digest), g.Active),
				fmt.Sprintf("    events=%s scope=%s env=%s timeout=%dms",
					orDash(strings.Join(g.Events, ",")), orDash(g.CwdScope), orDash(strings.Join(g.EnvKeys, ",")), g.TimeoutMS),
			)
		}
	}
	return append(lines, "", hint)
}

// drawPanel overlays a bordered box of lines at the top-left of the screen,
// clipped to the screen. Deterministic and legible on the monochrome path.
func drawPanel(sc *render.Screen, title string, lines []string) {
	w := len(title) + 4
	for _, l := range lines {
		if n := len([]rune(l)) + 4; n > w {
			w = n
		}
	}
	if w > sc.Cols {
		w = sc.Cols
	}
	h := len(lines) + 2
	if h > sc.Rows {
		h = sc.Rows
	}
	st := model.Style{Attrs: model.AttrBold}
	body := model.Style{}
	// top border with title
	sc.DrawText(0, 0, padBox("┌ "+title+" ", w, "─")+"┐", st)
	for i := 0; i < h-2 && i < len(lines); i++ {
		text := clipRunes(lines[i], w-2)
		sc.DrawText(0, i+1, "│"+padRight(text, w-2)+"│", body)
	}
	sc.DrawText(0, h-1, "└"+strings.Repeat("─", max0(w-2))+"┘", st)
}

func padBox(prefix string, w int, fill string) string {
	cur := len([]rune(prefix))
	for cur < w-1 {
		prefix += fill
		cur++
	}
	return prefix
}

func padRight(s string, w int) string {
	r := []rune(s)
	for len(r) < w {
		r = append(r, ' ')
	}
	return string(r)
}

func clipRunes(s string, w int) string {
	r := []rune(s)
	if w < 0 {
		w = 0
	}
	if len(r) <= w {
		return s
	}
	return string(r[:w])
}

func max0(x int) int {
	if x < 0 {
		return 0
	}
	return x
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func shortID(s string) string {
	if len(s) <= 8 {
		return s
	}
	return s[:8]
}
