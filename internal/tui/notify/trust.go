package notify

import (
	"fmt"
	"strings"

	"github.com/amux-run/amux/internal/tui/model"
)

// TrustAction is a hook trust decision the operator may take. The UI PRESENTS
// the decision and, on confirmation, emits it as an intent to the daemon —
// it never decides authorization itself (the daemon owns trust, ADR-0005).
type TrustAction string

const (
	TrustApprove TrustAction = "approve" // grant project trust (rpcapi.HookApproveParams)
	TrustDeny    TrustAction = "deny"    // record an explicit denial (rpcapi.HookDenyParams)
	TrustRevoke  TrustAction = "revoke"  // revoke project trust (rpcapi.HookRevokeParams)
)

// NeedsConfirm reports whether the action is destructive/trust-granting and so
// requires the confirmation token (mirrors the frozen matrix: approve and
// revoke carry Confirm; deny does not). A missing confirmation fails closed.
func (a TrustAction) NeedsConfirm() bool { return a == TrustApprove || a == TrustRevoke }

// TrustDecision is the intent the caller sends to the daemon after the operator
// confirms (or, for deny, chooses). Confirm is set only when the operator
// actually confirmed; the daemon rejects a NeedsConfirm action without it.
type TrustDecision struct {
	Project string
	Action  TrustAction
	Confirm bool
}

// unavailable is the explicit marker for a frozen trust field the current
// hook.list wire result does not deliver. It is shown verbatim so an operator
// never mistakes a missing field for an empty allowlist / zero timeout.
const unavailable = "UNAVAILABLE (not on hook.list wire result — see T4 contract request)"

// TrustCard renders the hook-trust confirmation as display lines: every frozen
// trust field the confirmation matrix requires (project identity, executable +
// digest, events, cwd scope, env-key allowlist, timeout, output cap), plus the
// grant status and the pending action with its confirmation requirement. Fields
// the wire did not supply are marked UNAVAILABLE, never guessed. Pure: it makes
// no decision.
func TrustCard(g model.HookGrant, action TrustAction) []string {
	line := func(label, val string) string {
		if val == "" {
			val = unavailable
		}
		return fmt.Sprintf("  %-12s %s", label+":", val)
	}
	lines := []string{
		fmt.Sprintf("Hook trust — %s", strings.ToUpper(string(action))),
		line("Project", g.Project),
		line("Hook", g.HookID),
		line("Executable", g.Executable),
		line("Digest", g.Digest),
		line("Events", strings.Join(g.Events, ", ")),
		line("Cwd scope", firstNonEmpty(g.CwdScope, g.Scope)),
		line("Env keys", strings.Join(g.EnvKeys, ", ")),
		line("Timeout", durMS(g.TimeoutMS)),
		line("Output cap", bytesCap(g.OutputCapB)),
		fmt.Sprintf("  %-12s active=%v bound_epoch=%d", "Status:", g.Active, g.BoundEpoch),
	}
	if !g.TrustComplete() {
		lines = append(lines, "  ! Some trust fields are UNAVAILABLE from the current wire contract.")
	}
	if action.NeedsConfirm() {
		lines = append(lines, fmt.Sprintf("Confirm %s? This is a %s change — [y] confirm  [n] cancel (fail-closed).",
			action, riskWord(action)))
	} else {
		lines = append(lines, fmt.Sprintf("Record %s? [y] confirm  [n] cancel.", action))
	}
	return lines
}

func riskWord(a TrustAction) string {
	if a == TrustRevoke {
		return "destructive trust"
	}
	return "trust-granting"
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func durMS(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return fmt.Sprintf("%dms", ms)
}

func bytesCap(b int64) string {
	if b <= 0 {
		return ""
	}
	return fmt.Sprintf("%d bytes", b)
}
