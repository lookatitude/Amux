package keys

import "testing"

func TestDefaultKeymapIsConflictFree(t *testing.T) {
	_, conflicts, err := Build(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("default keymap has conflicts: %v", conflicts)
	}
}

func TestPrefixKeyNeverLeaksToPTY(t *testing.T) {
	r := NewRouter(DefaultKeymap())
	res := r.Resolve(Passthrough, Ctrl('b'))
	if res.Disposition != ToUI || res.Action != ActEnterPrefix || res.NextMode != Prefix {
		t.Fatalf("prefix key mis-routed: %+v", res)
	}
}

func TestPassthroughForwardsPlainInput(t *testing.T) {
	r := NewRouter(DefaultKeymap())
	for _, k := range []Key{RuneKey('a'), RuneKey('Z'), RuneKey(' '), {Type: KeyEnter}, {Type: KeyLeft}} {
		res := r.Resolve(Passthrough, k)
		if res.Disposition != ToPTY {
			t.Fatalf("key %s should pass to PTY, got %+v", k.Canonical(), res)
		}
	}
}

func TestModeKeysNeverReachPTY(t *testing.T) {
	r := NewRouter(DefaultKeymap())
	// In every non-passthrough mode, NO key resolves to ToPTY — not bound keys,
	// not arbitrary runes, not control keys.
	modes := []Mode{Prefix, Navigation, Resize, Surface, Notification, Help, Confirmation}
	probe := []Key{RuneKey('a'), RuneKey('h'), RuneKey('q'), Ctrl('c'), {Type: KeyEnter}, {Type: KeyLeft}, {Type: KeyEsc}, {Type: KeyTab}}
	for _, m := range modes {
		for _, k := range probe {
			res := r.Resolve(m, k)
			if res.Disposition == ToPTY {
				t.Fatalf("mode %s leaked key %s to PTY: %+v", m, k.Canonical(), res)
			}
		}
	}
}

func TestNavigationModeIsSticky(t *testing.T) {
	r := NewRouter(DefaultKeymap())
	res := r.Resolve(Navigation, Key{Type: KeyLeft})
	if res.Action != ActFocusLeft || res.NextMode != Navigation {
		t.Fatalf("navigation move should stay in navigation: %+v", res)
	}
	// enter exits back to passthrough
	res = r.Resolve(Navigation, Key{Type: KeyEnter})
	if res.NextMode != Passthrough {
		t.Fatalf("enter should exit navigation: %+v", res)
	}
}

func TestPrefixIsOneShot(t *testing.T) {
	r := NewRouter(DefaultKeymap())
	// A bound command returns to passthrough.
	res := r.Resolve(Prefix, RuneKey('%'))
	if res.Action != ActSplitHoriz || res.NextMode != Passthrough {
		t.Fatalf("split should run and exit prefix: %+v", res)
	}
	// An unknown key aborts prefix (does not leak, does not stay).
	res = r.Resolve(Prefix, RuneKey('q'))
	if res.Disposition != Ignored || res.NextMode != Passthrough {
		t.Fatalf("unknown prefix key should abort to passthrough: %+v", res)
	}
}

func TestConfirmationFailsClosed(t *testing.T) {
	r := NewRouter(DefaultKeymap())
	// Only the explicit confirm key confirms.
	if res := r.Resolve(Confirmation, RuneKey('y')); res.Action != ActConfirm {
		t.Fatalf("y should confirm: %+v", res)
	}
	// 'n' denies.
	if res := r.Resolve(Confirmation, RuneKey('n')); res.Action != ActCancel {
		t.Fatalf("n should cancel: %+v", res)
	}
	// Esc denies (fail-closed), never confirms.
	if res := r.Resolve(Confirmation, Key{Type: KeyEsc}); res.Action != ActCancel {
		t.Fatalf("esc should cancel confirmation: %+v", res)
	}
	// Enter is NOT consent — swallowed, stays pending.
	res := r.Resolve(Confirmation, Key{Type: KeyEnter})
	if res.Action == ActConfirm || res.Disposition == ToPTY {
		t.Fatalf("enter must not confirm or leak: %+v", res)
	}
}

func TestConflictDetection(t *testing.T) {
	cfg := Config{
		Prefix: "ctrl+b",
		Modes: map[Mode]map[Action]string{
			Prefix: {ActFocusLeft: "h", ActFocusRight: "h"}, // both on 'h'
		},
	}
	_, conflicts, err := Build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 1 {
		t.Fatalf("want 1 conflict, got %d: %v", len(conflicts), conflicts)
	}
}

func TestPrefixKeyReservedInPassthrough(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Modes[Passthrough] = map[Action]string{ActDetach: "ctrl+b"} // collides with prefix
	_, conflicts, err := Build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range conflicts {
		if c.Mode == Passthrough && c.Key == "ctrl+b" {
			found = true
		}
	}
	if !found {
		t.Fatalf("binding the prefix key in passthrough must conflict: %v", conflicts)
	}
}

func TestParseKeyRoundTrip(t *testing.T) {
	for _, spec := range []string{"ctrl+b", "alt+x", "left", "esc", "?", "%", "enter", "space", "ctrl+alt+d"} {
		k, err := ParseKey(spec)
		if err != nil {
			t.Fatalf("parse %q: %v", spec, err)
		}
		if got := k.Canonical(); got != spec {
			t.Errorf("round-trip %q → %q", spec, got)
		}
	}
}
