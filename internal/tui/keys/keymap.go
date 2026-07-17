package keys

import (
	"fmt"
	"sort"
)

// Mode is the explicit interaction mode. Only Passthrough forwards keys to the
// PTY; every other mode fully consumes input (U4 invariant).
type Mode int

const (
	Passthrough  Mode = iota // typing goes to the focused surface's PTY
	Prefix                   // after the prefix key: awaiting a command key
	Navigation               // directional focus movement stays engaged
	Resize                   // arrow/hjkl adjust the focused split
	Surface                  // surface selection (next/prev/pick)
	Notification             // notification inbox open
	Help                     // help/discovery overlay
	Confirmation             // a destructive/takeover confirmation is pending
	Trust                    // hook trust inspection (hook.inspect projection) open
)

func (m Mode) String() string {
	switch m {
	case Passthrough:
		return "passthrough"
	case Prefix:
		return "prefix"
	case Navigation:
		return "navigation"
	case Resize:
		return "resize"
	case Surface:
		return "surface"
	case Notification:
		return "notification"
	case Help:
		return "help"
	case Confirmation:
		return "confirmation"
	case Trust:
		return "trust"
	default:
		return "unknown"
	}
}

// Action is a UI command a binding maps to. Actions are stable strings so a
// user keymap config and the help overlay share one vocabulary.
type Action string

const (
	ActNone           Action = ""
	ActEnterPrefix    Action = "enter_prefix"
	ActFocusLeft      Action = "focus_left"
	ActFocusRight     Action = "focus_right"
	ActFocusUp        Action = "focus_up"
	ActFocusDown      Action = "focus_down"
	ActSplitHoriz     Action = "split_horizontal"
	ActSplitVert      Action = "split_vertical"
	ActEqualize       Action = "equalize"
	ActEnterResize    Action = "enter_resize"
	ActEnterNav       Action = "enter_navigation"
	ActGrow           Action = "grow"
	ActShrink         Action = "shrink"
	ActNextSurface    Action = "next_surface"
	ActPrevSurface    Action = "prev_surface"
	ActEnterSurface   Action = "enter_surface"
	ActOpenNotifs     Action = "open_notifications"
	ActNextUnread     Action = "next_unread"
	ActMarkRead       Action = "mark_read"
	ActDismiss        Action = "dismiss"
	ActHelp           Action = "help"
	ActConfirm        Action = "confirm"
	ActCancel         Action = "cancel"
	ActDetach         Action = "detach"
	ActReleaseLease   Action = "release_lease"
	ActRequestTakeove Action = "request_takeover"
	ActRecover        Action = "recover" // re-snapshot/re-subscribe after a gap

	// Hook trust workflow (U6): open the hook.inspect projection for the focused
	// pane's project, then request an approve/deny/revoke confirmation. Every
	// mutation stays a daemon call behind the fail-closed confirmation card.
	ActHookTrust    Action = "hook_trust"
	ActTrustApprove Action = "trust_approve"
	ActTrustDeny    Action = "trust_deny"
	ActTrustRevoke  Action = "trust_revoke"
	ActNextGrant    Action = "next_grant"
	ActPrevGrant    Action = "prev_grant"
)

// Keymap maps, per mode, a Key to an Action. It is built from a Config and
// validated for conflicts before use.
type Keymap struct {
	// Prefix is the single key that opens Prefix mode from Passthrough. It is
	// reserved: it never reaches the PTY.
	Prefix   Key
	bindings map[Mode]map[Key]Action
}

// Lookup returns the action bound to key in mode, or ActNone.
func (km Keymap) Lookup(mode Mode, key Key) Action {
	if m, ok := km.bindings[mode]; ok {
		if a, ok := m[key]; ok {
			return a
		}
	}
	return ActNone
}

// BindingsFor returns the (key,action) pairs for a mode, sorted by canonical
// key, for the help overlay and golden output.
func (km Keymap) BindingsFor(mode Mode) []Binding {
	var out []Binding
	for k, a := range km.bindings[mode] {
		out = append(out, Binding{Key: k, Action: a})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key.Canonical() < out[j].Key.Canonical() })
	return out
}

// Binding is one key→action pair.
type Binding struct {
	Key    Key
	Action Action
}

// Config is a user-facing keymap: the prefix key plus, per mode, a map of
// action→key-spec. It is parsed and validated into a Keymap.
type Config struct {
	Prefix string
	Modes  map[Mode]map[Action]string
}

// Conflict describes two actions competing for one key in a mode.
type Conflict struct {
	Mode    Mode
	Key     string
	ActionA Action
	ActionB Action
}

func (c Conflict) Error() string {
	return fmt.Sprintf("keymap conflict in %s mode: key %q bound to both %q and %q",
		c.Mode, c.Key, c.ActionA, c.ActionB)
}

// Build parses cfg into a Keymap, returning every conflict found (a key bound
// to two actions in the same mode, or a mode binding that collides with the
// reserved prefix key in Passthrough). A non-empty conflict slice means the
// keymap is rejected — fail closed rather than silently pick a winner.
func Build(cfg Config) (Keymap, []Conflict, error) {
	prefix, err := ParseKey(cfg.Prefix)
	if err != nil {
		return Keymap{}, nil, fmt.Errorf("keymap: prefix: %w", err)
	}
	km := Keymap{Prefix: prefix, bindings: map[Mode]map[Key]Action{}}
	var conflicts []Conflict
	for mode, actions := range cfg.Modes {
		km.bindings[mode] = map[Key]Action{}
		// deterministic action order for stable conflict reporting
		names := make([]Action, 0, len(actions))
		for a := range actions {
			names = append(names, a)
		}
		sort.Slice(names, func(i, j int) bool { return names[i] < names[j] })
		for _, a := range names {
			spec := actions[a]
			k, perr := ParseKey(spec)
			if perr != nil {
				return Keymap{}, nil, fmt.Errorf("keymap: %s/%s: %w", mode, a, perr)
			}
			if existing, dup := km.bindings[mode][k]; dup && existing != a {
				conflicts = append(conflicts, Conflict{Mode: mode, Key: k.Canonical(), ActionA: existing, ActionB: a})
				continue
			}
			// The prefix key is reserved in Passthrough: a Passthrough binding on
			// it would either shadow prefix entry or leak to the PTY.
			if mode == Passthrough && k == prefix {
				conflicts = append(conflicts, Conflict{Mode: mode, Key: k.Canonical(), ActionA: ActEnterPrefix, ActionB: a})
				continue
			}
			km.bindings[mode][k] = a
		}
	}
	return km, conflicts, nil
}
