// Package keys is the terminal UI's input model: a host-neutral Key type, the
// explicit interaction Modes, a conflict-checked configurable Keymap, and a
// Router that decides — deterministically — whether a keypress is consumed by
// the UI or passed through to the PTY (U4). The central invariant: when the UI
// is in any mode other than Passthrough, no key ever reaches the PTY; and in
// Passthrough the prefix key is consumed (it opens Prefix mode) rather than
// leaking to the process. The Router is pure, so this is unit-provable.
//
// The Key type is deliberately independent of any toolkit. The runtime (or a
// future Bubble Tea shell) maps its native key events onto keys.Key at the
// single input boundary; everything downstream routes on keys.Key.
package keys

import (
	"fmt"
	"strings"
)

// KeyType classifies a key event.
type KeyType int

const (
	KeyRune KeyType = iota // a printable rune (see Rune)
	KeyEnter
	KeyEsc
	KeyTab
	KeyBackspace
	KeySpace
	KeyUp
	KeyDown
	KeyLeft
	KeyRight
	KeyHome
	KeyEnd
	KeyPgUp
	KeyPgDn
	KeyDelete
)

// Key is one normalized key event. Ctrl/Alt are the modifiers the keymap binds
// on; Shift is captured for completeness but default bindings do not use it.
type Key struct {
	Type  KeyType
	Rune  rune
	Ctrl  bool
	Alt   bool
	Shift bool
}

// Rune builds a plain printable-rune key.
func RuneKey(r rune) Key { return Key{Type: KeyRune, Rune: r} }

// Ctrl builds a Ctrl+<rune> key (e.g. Ctrl('b')).
func Ctrl(r rune) Key { return Key{Type: KeyRune, Rune: r, Ctrl: true} }

// Canonical returns the stable string form used in keymap config and conflict
// reports, e.g. "ctrl+b", "alt+x", "left", "esc", "?".
func (k Key) Canonical() string {
	var b strings.Builder
	if k.Ctrl {
		b.WriteString("ctrl+")
	}
	if k.Alt {
		b.WriteString("alt+")
	}
	switch k.Type {
	case KeyRune:
		b.WriteRune(k.Rune)
	case KeyEnter:
		b.WriteString("enter")
	case KeyEsc:
		b.WriteString("esc")
	case KeyTab:
		b.WriteString("tab")
	case KeyBackspace:
		b.WriteString("backspace")
	case KeySpace:
		b.WriteString("space")
	case KeyUp:
		b.WriteString("up")
	case KeyDown:
		b.WriteString("down")
	case KeyLeft:
		b.WriteString("left")
	case KeyRight:
		b.WriteString("right")
	case KeyHome:
		b.WriteString("home")
	case KeyEnd:
		b.WriteString("end")
	case KeyPgUp:
		b.WriteString("pgup")
	case KeyPgDn:
		b.WriteString("pgdn")
	case KeyDelete:
		b.WriteString("delete")
	}
	return b.String()
}

// ParseKey parses a canonical key spec back into a Key. It is the inverse of
// Canonical for every form Canonical emits.
func ParseKey(spec string) (Key, error) {
	var k Key
	rest := spec
	for {
		switch {
		case strings.HasPrefix(rest, "ctrl+"):
			k.Ctrl = true
			rest = rest[len("ctrl+"):]
		case strings.HasPrefix(rest, "alt+"):
			k.Alt = true
			rest = rest[len("alt+"):]
		default:
			goto body
		}
	}
body:
	switch rest {
	case "":
		return Key{}, fmt.Errorf("keys: empty key spec %q", spec)
	case "enter":
		k.Type = KeyEnter
	case "esc":
		k.Type = KeyEsc
	case "tab":
		k.Type = KeyTab
	case "backspace":
		k.Type = KeyBackspace
	case "space":
		k.Type = KeySpace
	case "up":
		k.Type = KeyUp
	case "down":
		k.Type = KeyDown
	case "left":
		k.Type = KeyLeft
	case "right":
		k.Type = KeyRight
	case "home":
		k.Type = KeyHome
	case "end":
		k.Type = KeyEnd
	case "pgup":
		k.Type = KeyPgUp
	case "pgdn":
		k.Type = KeyPgDn
	case "delete":
		k.Type = KeyDelete
	default:
		r := []rune(rest)
		if len(r) != 1 {
			return Key{}, fmt.Errorf("keys: not a single rune or known key: %q", rest)
		}
		k.Type = KeyRune
		k.Rune = r[0]
	}
	return k, nil
}
