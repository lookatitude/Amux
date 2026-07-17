package app

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amux-run/amux/internal/tui/a11y"
	"github.com/amux-run/amux/internal/tui/attachstate"
	"github.com/amux-run/amux/internal/tui/geometry"
	"github.com/amux-run/amux/internal/tui/keys"
	"github.com/amux-run/amux/internal/tui/model"
	"github.com/amux-run/amux/internal/tui/notify"
	"github.com/amux-run/amux/internal/tui/runtime"
)

var update = flag.Bool("update", false, "regenerate golden frames")

// fourPaneTree builds (a|b) over (c|d).
func fourPaneTree() *geometry.Node {
	return geometry.Split(geometry.Vertical,
		geometry.Split(geometry.Horizontal, geometry.Leaf("a"), geometry.Leaf("b")),
		geometry.Split(geometry.Horizontal, geometry.Leaf("c"), geometry.Leaf("d")),
	)
}

func fourPaneMsg() PaneTreeMsg {
	panes := []model.Pane{}
	for _, id := range []string{"a", "b", "c", "d"} {
		panes = append(panes, model.Pane{
			ID: id, Cwd: "/home/dev", Active: id + "-s1",
			Surfaces: []model.Surface{{ID: id + "-s1", Active: true, Class: model.ClassLive}},
		})
	}
	return PaneTreeMsg{Root: fourPaneTree(), Focused: "a", Panes: panes}
}

func mono() *Model {
	return New(60, 16, keys.DefaultKeymap(), a11y.Profile{Color: a11y.NoColor, MinCols: 20, MinRows: 5})
}

// assertGolden compares got to testdata/<name>.golden, updating with -update.
func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s (run -update to create): %v", path, err)
	}
	if string(want) != got {
		t.Errorf("golden %s mismatch:\n--- got ---\n%s\n--- want ---\n%s", name, got, string(want))
	}
}

func TestGoldenFourPaneFocus(t *testing.T) {
	m := mono()
	msgs := []runtime.Msg{
		fourPaneMsg(),
		PaneContentMsg{Pane: "a", Snapshot: contentSnap("alpha"), Class: model.ClassLive},
		PaneContentMsg{Pane: "b", Snapshot: contentSnap("bravo"), Class: model.ClassLive},
		HealthMsg{Health: model.Health{Version: "0.1.0", BootID: "boot1234abcd"}},
	}
	_, frames := runtime.Drive(m, msgs)
	assertGolden(t, "four_pane_focus", frames[len(frames)-1])
}

func TestGoldenStoppedAndRestarted(t *testing.T) {
	m := mono()
	msgs := []runtime.Msg{
		fourPaneMsg(),
		PaneContentMsg{Pane: "a", Snapshot: contentSnap("live"), Class: model.ClassLive},
		PaneContentMsg{Pane: "b", Snapshot: contentSnap("gone"), Class: model.ClassStopped, ExitReason: "exit 1"},
		PaneContentMsg{Pane: "c", Snapshot: contentSnap("back"), Class: model.ClassRestarted},
	}
	_, frames := runtime.Drive(m, msgs)
	assertGolden(t, "stopped_restarted", frames[len(frames)-1])
}

func TestGoldenConfirmationModal(t *testing.T) {
	m := mono()
	g := model.HookGrant{Project: "proj", HookID: "h1", Events: []string{"PreToolUse"}, Scope: "pane", Active: true}
	msgs := []runtime.Msg{
		fourPaneMsg(),
		TrustPromptMsg{Grant: g, Action: notify.TrustApprove},
	}
	_, frames := runtime.Drive(m, msgs)
	assertGolden(t, "trust_confirmation", frames[len(frames)-1])
}

func TestGoldenMinSizeFallback(t *testing.T) {
	m := New(10, 3, keys.DefaultKeymap(), a11y.DefaultProfile())
	assertGolden(t, "min_size", m.View())
}

func TestFocusMoveEmitsIntentAndIsDeterministic(t *testing.T) {
	m := mono()
	m.Update(fourPaneMsg())
	m.DrainOutbox()
	// Ctrl+b then l → focus right (a→b).
	m.Update(runtime.KeyMsg{Key: keys.Ctrl('b')})
	m.Update(runtime.KeyMsg{Key: keys.RuneKey('l')})
	if m.Focused() != "b" {
		t.Fatalf("focus should move to b, got %q", m.Focused())
	}
	out := m.DrainOutbox()
	if len(out) == 0 || out[len(out)-1].Kind != IntentFocus || out[len(out)-1].Pane != "b" {
		t.Fatalf("expected focus intent for b, got %+v", out)
	}
}

func TestPlainKeyGoesToPTYAsInput(t *testing.T) {
	m := mono()
	m.Update(fourPaneMsg())
	m.DrainOutbox()
	m.Update(runtime.KeyMsg{Key: keys.RuneKey('x')})
	if m.LastDisposition != keys.ToPTY {
		t.Fatalf("plain key should be ToPTY, got %s", m.LastDisposition)
	}
	out := m.DrainOutbox()
	if len(out) != 1 || out[0].Kind != IntentInput || string(out[0].Data) != "x" {
		t.Fatalf("expected input intent 'x', got %+v", out)
	}
}

func TestPrefixKeyDoesNotLeakToInput(t *testing.T) {
	m := mono()
	m.Update(fourPaneMsg())
	m.DrainOutbox()
	m.Update(runtime.KeyMsg{Key: keys.Ctrl('b')})
	if m.Mode() != keys.Prefix {
		t.Fatalf("ctrl+b should enter prefix mode, got %s", m.Mode())
	}
	if out := m.DrainOutbox(); len(out) != 0 {
		t.Fatalf("prefix key must not emit input, got %+v", out)
	}
}

func TestTakeoverRequiresConfirmation(t *testing.T) {
	m := mono()
	m.Update(fourPaneMsg())
	m.DrainOutbox()
	// prefix + T opens the takeover confirmation; no takeover emitted yet.
	m.Update(runtime.KeyMsg{Key: keys.Ctrl('b')})
	m.Update(runtime.KeyMsg{Key: keys.RuneKey('T')})
	if m.Mode() != keys.Confirmation {
		t.Fatalf("takeover should open confirmation, got %s", m.Mode())
	}
	if out := m.DrainOutbox(); len(out) != 0 {
		t.Fatalf("no takeover before confirmation, got %+v", out)
	}
	// Deny → no takeover.
	m.Update(runtime.KeyMsg{Key: keys.RuneKey('n')})
	if out := m.DrainOutbox(); len(out) != 0 {
		t.Fatalf("denied takeover must emit nothing, got %+v", out)
	}
	// Re-open and confirm → takeover emitted.
	m.Update(runtime.KeyMsg{Key: keys.Ctrl('b')})
	m.Update(runtime.KeyMsg{Key: keys.RuneKey('T')})
	m.Update(runtime.KeyMsg{Key: keys.RuneKey('y')})
	out := m.DrainOutbox()
	if len(out) != 1 || out[0].Kind != IntentTakeover {
		t.Fatalf("confirmed takeover should emit IntentTakeover, got %+v", out)
	}
}

func TestGapRecoveryFlow(t *testing.T) {
	m := mono()
	m.Update(fourPaneMsg())
	m.Update(AttachEventMsg{Phase: model.PhaseConnecting})
	m.Update(AttachErrMsg{Kind: attachstate.ErrEventGap})
	if !m.AttachState().NeedsRecovery() {
		t.Fatal("event gap should require recovery")
	}
	m.DrainOutbox()
	// prefix + g → recover.
	m.Update(runtime.KeyMsg{Key: keys.Ctrl('b')})
	m.Update(runtime.KeyMsg{Key: keys.RuneKey('g')})
	out := m.DrainOutbox()
	if len(out) != 1 || out[0].Kind != IntentRecover {
		t.Fatalf("recover key should emit IntentRecover, got %+v", out)
	}
	if m.AttachState().NeedsRecovery() {
		t.Fatal("after recover, state should clear recovery")
	}
}

func TestPasteNeverParsedAsKeysOutsidePassthrough(t *testing.T) {
	m := mono()
	m.Update(fourPaneMsg())
	m.Update(runtime.KeyMsg{Key: keys.Ctrl('b')}) // enter prefix
	m.DrainOutbox()
	m.Update(runtime.PasteMsg{Text: "l%"}) // would be focus+split if parsed as keys
	if out := m.DrainOutbox(); len(out) != 0 {
		t.Fatalf("paste in non-passthrough must be ignored, got %+v", out)
	}
}

func TestNotificationRoutingEmitsFocus(t *testing.T) {
	m := mono()
	m.Update(fourPaneMsg())
	m.Update(NotificationsMsg{Items: []model.Notification{
		{ID: "n1", Kind: model.NotifyAttention, Title: "hey", CreatedMS: 10, Pane: "d"},
	}})
	m.DrainOutbox()
	// prefix + u → next unread, routes focus to pane d.
	m.Update(runtime.KeyMsg{Key: keys.Ctrl('b')})
	m.Update(runtime.KeyMsg{Key: keys.RuneKey('u')})
	if m.Focused() != "d" {
		t.Fatalf("latest-unread should route focus to d, got %q", m.Focused())
	}
}

// contentSnap makes a 1-line snapshot from text (helper).
func contentSnap(text string) model.CellSnapshot {
	runes := []rune(text)
	row := make([]model.Cell, len(runes))
	for i, r := range runes {
		row[i] = model.Cell{Content: string(r), Width: 1}
	}
	return model.CellSnapshot{Rows: 1, Cols: len(runes), Cells: [][]model.Cell{row}}
}

func TestDeterministicReplay(t *testing.T) {
	// Same message stream twice → identical frames (deterministic Update).
	build := func() (*Model, []runtime.Msg) {
		return mono(), []runtime.Msg{
			fourPaneMsg(),
			PaneContentMsg{Pane: "a", Snapshot: contentSnap("one"), Class: model.ClassLive},
			runtime.KeyMsg{Key: keys.Ctrl('b')},
			runtime.KeyMsg{Key: keys.RuneKey('l')},
			runtime.ResizeMsg{Cols: 80, Rows: 24},
		}
	}
	m1, msgs1 := build()
	_, f1 := runtime.Drive(m1, msgs1)
	m2, msgs2 := build()
	_, f2 := runtime.Drive(m2, msgs2)
	if strings.Join(f1, "\x00") != strings.Join(f2, "\x00") {
		t.Fatal("identical message streams produced different frames (non-deterministic)")
	}
}
