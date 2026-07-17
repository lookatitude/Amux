package keys

// DefaultConfig is the shipped keymap: a tmux-like Ctrl+b prefix, directional
// focus and splits from Prefix, sticky Navigation/Resize/Surface/Notification
// sub-modes, a help overlay, and confirm/deny for the fail-closed confirmation
// modal. It is deliberately conflict-free (Build returns no conflicts) and is
// the base a user config overlays.
func DefaultConfig() Config {
	return Config{
		Prefix: "ctrl+b",
		Modes: map[Mode]map[Action]string{
			// Passthrough intentionally binds nothing: all non-prefix keys are
			// real input for the process. The prefix key is handled by the router.
			Passthrough: {},
			Prefix: {
				ActFocusLeft:      "h",
				ActFocusDown:      "j",
				ActFocusUp:        "k",
				ActFocusRight:     "l",
				ActSplitHoriz:     "%",
				ActSplitVert:      "\"",
				ActEqualize:       "=",
				ActEnterResize:    "r",
				ActEnterNav:       "n",
				ActNextSurface:    "o",
				ActPrevSurface:    "i",
				ActEnterSurface:   "s",
				ActOpenNotifs:     "!",
				ActNextUnread:     "u",
				ActHelp:           "?",
				ActDetach:         "d",
				ActReleaseLease:   "x",
				ActRequestTakeove: "T",
				ActRecover:        "g",
				ActHookTrust:      "t",
				ActCancel:         "esc",
			},
			Navigation: {
				ActFocusLeft:  "left",
				ActFocusDown:  "down",
				ActFocusUp:    "up",
				ActFocusRight: "right",
				ActCancel:     "enter",
			},
			Resize: {
				ActGrow:   "right",
				ActShrink: "left",
				ActCancel: "enter",
			},
			Surface: {
				ActNextSurface:  "right",
				ActPrevSurface:  "left",
				ActEnterSurface: "enter",
			},
			Notification: {
				ActNextUnread: "j",
				ActMarkRead:   "enter",
				ActDismiss:    "d",
				ActCancel:     "esc",
			},
			Help: {
				ActCancel: "esc",
			},
			// Trust presents the hook.inspect projection; a/d/r only REQUEST a
			// confirmation card — the daemon call itself stays behind the
			// fail-closed Confirmation mode below.
			Trust: {
				ActTrustApprove: "a",
				ActTrustDeny:    "d",
				ActTrustRevoke:  "r",
				ActNextGrant:    "j",
				ActPrevGrant:    "k",
				ActCancel:       "esc",
			},
			Confirmation: {
				ActConfirm: "y",
				ActCancel:  "n",
			},
		},
	}
}

// DefaultKeymap builds the default keymap; it panics only if the shipped
// defaults are themselves malformed (a build-time invariant, tested).
func DefaultKeymap() Keymap {
	km, conflicts, err := Build(DefaultConfig())
	if err != nil {
		panic("keys: default config invalid: " + err.Error())
	}
	if len(conflicts) > 0 {
		panic("keys: default config has conflicts: " + conflicts[0].Error())
	}
	return km
}
