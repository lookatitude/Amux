package app

import "github.com/amux-run/amux/internal/tui/keys"

// EncodeKey turns a decoded key into the bytes to forward to a PTY when the
// router dispositions it ToPTY. This is INPUT encoding (keyboard → process
// bytes); it never parses surface OUTPUT. Prefix/mode/navigation keys never
// reach here — the router consumes them before ToPTY — so command keys can
// never leak into the process stream (U4 invariant, enforced upstream).
func EncodeKey(k keys.Key) []byte {
	if k.Ctrl && k.Type == keys.KeyRune {
		r := k.Rune
		switch {
		case r >= 'a' && r <= 'z':
			return []byte{byte(r - 'a' + 1)} // Ctrl-A..Ctrl-Z → 0x01..0x1a
		case r >= 'A' && r <= 'Z':
			return []byte{byte(r - 'A' + 1)}
		case r == ' ' || r == '@':
			return []byte{0}
		}
	}
	switch k.Type {
	case keys.KeyRune:
		b := []byte(string(k.Rune))
		if k.Alt {
			return append([]byte{0x1b}, b...)
		}
		return b
	case keys.KeyEnter:
		return []byte{'\r'}
	case keys.KeyTab:
		return []byte{'\t'}
	case keys.KeyBackspace:
		return []byte{0x7f}
	case keys.KeyEsc:
		return []byte{0x1b}
	case keys.KeySpace:
		return []byte{' '}
	case keys.KeyUp:
		return []byte("\x1b[A")
	case keys.KeyDown:
		return []byte("\x1b[B")
	case keys.KeyRight:
		return []byte("\x1b[C")
	case keys.KeyLeft:
		return []byte("\x1b[D")
	case keys.KeyHome:
		return []byte("\x1b[H")
	case keys.KeyEnd:
		return []byte("\x1b[F")
	case keys.KeyPgUp:
		return []byte("\x1b[5~")
	case keys.KeyPgDn:
		return []byte("\x1b[6~")
	case keys.KeyDelete:
		return []byte("\x1b[3~")
	}
	return nil
}
