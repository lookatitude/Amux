package securitytest

import (
	"bytes"
	"encoding/json"

	v1 "github.com/amux-run/amux/api/v1"
)

// The trust matrix is the generated, machine-pinned decision table of the
// hook-authorization contract: for every security-relevant condition family it
// fixes the required decision and error code. It is GENERATED (never
// hand-edited) so the golden at testdata/security/trust-matrix.json cannot
// drift from this code, and this code cannot drift from the frozen taxonomy
// (TestTrustMatrixCoverage pins both). T4 backend implements to it; T6 QA
// replays it against the integrated daemon.

// MatrixRow is one pinned condition -> outcome cell. Condition fields are
// deviations from the allow baseline: project trusted at the current epoch,
// active fresh grant, scope "none" (scratch cwd), default bounds, empty env
// allowlist with nothing requested.
type MatrixRow struct {
	ID          string   `json:"id"`
	Family      string   `json:"family"`
	Requirement string   `json:"requirement"` // normative doc anchor (HA-*, RED-*)
	Condition   string   `json:"condition"`   // the single deviation this row pins
	Expect      Expected `json:"expect"`
}

// TrustMatrix is the serialized golden document.
type TrustMatrix struct {
	Schema string        `json:"schema"`
	Gates  GateConstants `json:"gates"`
	Rows   []MatrixRow   `json:"rows"`
}

const TrustMatrixSchema = "amux.security.trust-matrix.v1"

func deny(code v1.ErrorCode) Expected { return Expected{Decision: DecisionDeny, Code: code} }
func allow() Expected                 { return Expected{Decision: DecisionAllow} }
func allowEnforced(e string) Expected {
	return Expected{Decision: DecisionAllow, Enforcement: e}
}

// GenerateTrustMatrix enumerates the matrix deterministically (fixed order,
// no clock, no randomness). Every required family from the lane contract is
// present: absent/stale/revoked project and hook grants, replaced/remounted
// roots, cross-project panes, all cwd scopes, digest/config changes,
// timeout/output bounds, and environment allowlists.
func GenerateTrustMatrix() TrustMatrix {
	rows := []MatrixRow{
		// --- project family: trust state gates everything (HA-3) -----------
		{"project.trusted-baseline", "project", "HA-3", "project trusted at current epoch; fresh active grant; scratch cwd; default bounds", allow()},
		{"project.unregistered", "project", "HA-2d, HA-3b", "cwd inside a directory never explicitly registered; cwd alone creates no trust boundary", deny(v1.ErrProjectTrustRequired)},
		{"project.registered-not-approved", "project", "HA-3a, HA-3b", "project registered but not opted in/approved; hooks.jsonc must not even be parsed", deny(v1.ErrProjectTrustRequired)},
		{"project.denied", "project", "HA-3b", "operator explicitly denied the project", deny(v1.ErrProjectTrustRequired)},
		{"project.revoked", "project", "HA-3b, HA-18", "project trust revoked; epoch bumped; grants inactive", deny(v1.ErrProjectTrustRequired)},
		{"project.replaced-root", "project", "HA-2c", "root directory replaced (new inode) under the same path; identity tuple changed", deny(v1.ErrProjectTrustRequired)},
		{"project.remounted-root", "project", "HA-2c", "root remounted (new st_dev); identity tuple changed", deny(v1.ErrProjectTrustRequired)},

		// --- grant family: per-hook grant gates (HA-5..HA-7) ---------------
		{"grant.absent", "grant", "HA-5", "project trusted, hook has no grant", deny(v1.ErrHookGrantRequired)},
		{"grant.inactive-after-revocation", "grant", "HA-18e", "project revoked then freshly reapproved; prior grant remains inactive and needs fresh approval", deny(v1.ErrHookGrantRequired)},
		{"grant.epoch-stale", "grant", "HA-4a, HA-7", "grant bound to an earlier trust epoch than the project's current epoch", deny(v1.ErrHookGrantStale)},
		{"grant.exec-digest-changed", "grant", "HA-7, HA-11", "executable bytes differ from the bound SHA-256 at descriptor validation", deny(v1.ErrHookGrantStale)},
		{"grant.config-digest-changed", "grant", "HA-7, HA-11", ".amux/hooks.jsonc bytes differ from the bound SHA-256", deny(v1.ErrHookGrantStale)},
		{"grant.event-set-changed", "grant", "HA-7", "config redefines the hook's event set after approval", deny(v1.ErrHookGrantStale)},
		{"grant.cwd-scope-changed", "grant", "HA-7", "config redefines the hook's cwd scope after approval", deny(v1.ErrHookGrantStale)},
		{"grant.env-allowlist-changed", "grant", "HA-7", "config redefines the hook's environment allowlist after approval", deny(v1.ErrHookGrantStale)},
		{"grant.timeout-changed", "grant", "HA-7", "config redefines the hook's timeout after approval", deny(v1.ErrHookGrantStale)},
		{"grant.output-cap-changed", "grant", "HA-7", "config redefines the hook's output cap after approval", deny(v1.ErrHookGrantStale)},

		// --- scope family: cwd containment (HA-9, HA-13) -------------------
		{"scope.none-scratch", "scope", "HA-9", "no cwd scope granted; hook runs in Amux-owned scratch directory", allow()},
		{"scope.fixed-valid", "scope", "HA-9", "fixed scope; resolved object identity equals the granted object", allow()},
		{"scope.fixed-identity-changed", "scope", "HA-13", "fixed scope; granted directory replaced (identity mismatch at pre-launch recheck)", deny(v1.ErrScopeDenied)},
		{"scope.wsprimary-valid", "scope", "HA-9", "workspace-primary; exactly one primary root whose current identity equals the grant's project", allow()},
		{"scope.wsprimary-absent", "scope", "HA-9", "workspace-primary; workspace has no configured primary root", deny(v1.ErrScopeDenied)},
		{"scope.wsprimary-ambiguous", "scope", "HA-9", "workspace-primary; more than one candidate primary root", deny(v1.ErrScopeDenied)},
		{"scope.wsprimary-replaced", "scope", "HA-9", "workspace-primary; primary root replaced since grant (identity mismatch)", deny(v1.ErrScopeDenied)},
		{"scope.wsprimary-foreign-project", "scope", "HA-9", "workspace-primary; primary root belongs to a different project identity", deny(v1.ErrScopeDenied)},
		{"scope.pane-same-project", "scope", "HA-9", "pane scope; target pane resolves to the grant's project identity", allow()},
		{"scope.pane-cross-project", "scope", "HA-9", "pane scope; target pane resolves to a different project identity", deny(v1.ErrScopeDenied)},
		{"scope.pane-unregistered", "scope", "HA-9", "pane scope; target pane has no registered project", deny(v1.ErrScopeDenied)},
		{"scope.event-not-granted", "scope", "HA-9", "activation event outside the grant's allowed event set", deny(v1.ErrScopeDenied)},

		// --- bounds family (HA-8, HA-12) ------------------------------------
		{"bounds.timeout-default", "bounds", "HA-8", "no timeout configured; 2000 ms default applies", allowEnforced("kill_at_timeout")},
		{"bounds.timeout-max", "bounds", "HA-8", "timeout granted at the 30000 ms maximum", allowEnforced("kill_at_timeout")},
		{"bounds.timeout-exceeds-max", "bounds", "HA-8", "grant/config requests a timeout above 30000 ms; never clamped silently", deny(v1.ErrInvalidArgument)},
		{"bounds.output-within-cap", "bounds", "HA-8", "output below 1 MiB cap", allow()},
		{"bounds.output-cap-exceeds-max", "bounds", "HA-8", "grant/config requests an output cap above 1 MiB", deny(v1.ErrInvalidArgument)},
		{"bounds.output-cap-enforced", "bounds", "HA-8, RED-5", "running hook exceeds the cap; truncated redaction-safely and audited", allowEnforced("truncate_at_output_cap")},
		{"bounds.exec-not-regular-file", "bounds", "HA-12", "granted executable path resolves to a FIFO/device/directory at open", deny(v1.ErrInvalidArgument)},

		// --- environment family (HA-8, RED-3) -------------------------------
		{"env.empty-allowlist", "env", "HA-8", "empty allowlist, hook input requests no environment", allowEnforced("no_ambient_env")},
		{"env.allowlisted-keys-only", "env", "HA-8", "only allowlisted keys are injected, values from the explicit non-secret store", allowEnforced("no_ambient_env")},
		{"env.non-allowlisted-requested", "env", "HA-8", "activation requires a key outside the grant's allowlist", deny(v1.ErrScopeDenied)},

		// --- load family: local DoS controls (STR-5..STR-8) -----------------
		{"load.queue-exhausted", "load", "STR-8", "activation queue at bound; excess submissions rejected, never unbounded buffering", deny(v1.ErrResourceExhausted)},

		// --- internal invariant (fail closed, never grant) -------------------
		{"internal.invariant-violation", "internal", "hook-authorization.md §8", "trust-engine invariant violated during evaluation; deny and audit, never allow", deny(v1.ErrInternal)},
	}
	return TrustMatrix{Schema: TrustMatrixSchema, Gates: Gates, Rows: rows}
}

// MarshalTrustMatrix renders the canonical golden bytes (two-space indent,
// trailing newline) compared verbatim by TestTrustMatrixGoldenIsCurrent.
func MarshalTrustMatrix(m TrustMatrix) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		panic(err) // structurally impossible: static struct of scalars
	}
	return buf.Bytes()
}
