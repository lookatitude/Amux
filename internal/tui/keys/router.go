package keys

// Disposition is what the router decided to do with a key.
type Disposition int

const (
	// ToPTY forwards the key's bytes to the focused surface. Returned ONLY in
	// Passthrough mode and never for the prefix key — the single place input
	// reaches a process.
	ToPTY Disposition = iota
	// ToUI means the UI consumes the key and runs Action (possibly changing
	// mode). Never forwarded to the PTY.
	ToUI
	// Ignored means the key is swallowed with no action (e.g. an unbound key in
	// a modal, or a non-confirming key while a confirmation is pending). Never
	// forwarded to the PTY.
	Ignored
)

func (d Disposition) String() string {
	switch d {
	case ToPTY:
		return "to_pty"
	case ToUI:
		return "to_ui"
	default:
		return "ignored"
	}
}

// Resolution is the router's decision for one key.
type Resolution struct {
	Disposition Disposition
	Action      Action
	NextMode    Mode
}

// Router resolves keys against a keymap and the current mode. It is pure: the
// caller owns the mode variable and applies Resolution.NextMode. The core
// safety invariant is enforced structurally here — Resolve only ever returns
// ToPTY when mode == Passthrough and the key is not the prefix key.
type Router struct {
	km Keymap
}

// NewRouter builds a router over a validated keymap.
func NewRouter(km Keymap) Router { return Router{km: km} }

// Resolve decides the disposition of key while in mode.
func (r Router) Resolve(mode Mode, key Key) Resolution {
	if mode == Passthrough {
		if key == r.km.Prefix {
			return Resolution{Disposition: ToUI, Action: ActEnterPrefix, NextMode: Prefix}
		}
		if a := r.km.Lookup(Passthrough, key); a != ActNone {
			return Resolution{Disposition: ToUI, Action: a, NextMode: nextMode(Passthrough, a)}
		}
		// Everything else is real input for the process.
		return Resolution{Disposition: ToPTY, NextMode: Passthrough}
	}

	// Non-passthrough modes NEVER forward to the PTY. Esc always cancels back to
	// passthrough (except we still honour an explicit binding first).
	if a := r.km.Lookup(mode, key); a != ActNone {
		return Resolution{Disposition: ToUI, Action: a, NextMode: nextMode(mode, a)}
	}
	if key.Type == KeyEsc {
		// Confirmation fails closed: Esc cancels (denies), never confirms.
		return Resolution{Disposition: ToUI, Action: ActCancel, NextMode: Passthrough}
	}
	// Unbound key inside a mode: swallow it. It must not leak to the PTY, and in
	// Confirmation it must not be treated as consent.
	stay := mode
	if mode == Prefix {
		stay = Passthrough // prefix is one-shot; an unknown command aborts it
	}
	return Resolution{Disposition: Ignored, NextMode: stay}
}

// nextMode computes the mode after running action a from mode cur.
func nextMode(cur Mode, a Action) Mode {
	switch a {
	case ActEnterPrefix:
		return Prefix
	case ActEnterResize:
		return Resize
	case ActEnterNav:
		return Navigation
	case ActOpenNotifs:
		return Notification
	case ActHelp:
		return Help
	case ActHookTrust:
		return Trust
	case ActCancel:
		return Passthrough
	}
	switch cur {
	case Navigation, Resize, Surface, Notification, Help, Trust:
		// Persistent modes keep themselves unless the action commits/exits.
		if a == ActConfirm || a == ActEnterSurface {
			return Passthrough
		}
		return cur
	default: // Prefix, Confirmation, Passthrough one-shots
		return Passthrough
	}
}
