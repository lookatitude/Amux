package securitytest

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/amux-run/amux/api/v1"
)

// RunConformance executes the full adversarial fixture suite against the
// SystemUnderTest built by factory. T4 backend calls this from its own test
// package with a real factory (real trust engine, spy launcher behind
// platform.DescriptorLaunch — NO fixture ever executes a real hook — and a
// fake platform.Clock). T6 QA executes the same entry against the integrated
// candidate. With factory == nil the suite SKIPS with an explicit
// prerequisite message; it never silently passes (readiness-manifest check
// "security-conformance").
func RunConformance(t *testing.T, factory Factory) {
	t.Helper()
	fixtures, err := LoadFixtures(FixtureDir())
	if err != nil {
		t.Fatalf("loading fixture vectors: %v", err)
	}
	if err := ValidateFixtures(fixtures); err != nil {
		t.Fatalf("fixture vectors are ill-formed: %v", err)
	}
	if factory == nil {
		t.Skip("PREREQUISITE MISSING: no SystemUnderTest registered. " +
			"T4 backend must invoke securitytest.RunConformance with a real Factory " +
			"(docs/security/security-readiness.md check security-conformance). " +
			"Skipping is a deferral, not a pass.")
	}
	for _, f := range fixtures.Timing {
		f := f
		t.Run(f.ID, func(t *testing.T) { runTiming(t, factory, f) })
	}
	for _, f := range fixtures.Races {
		f := f
		t.Run(f.ID, func(t *testing.T) { runRace(t, factory, f) })
	}
	for _, f := range fixtures.Restore {
		f := f
		t.Run(f.ID, func(t *testing.T) { runRestore(t, factory, f) })
	}
	for _, f := range fixtures.Redaction {
		f := f
		t.Run(f.ID, func(t *testing.T) { runRedaction(t, factory, f) })
	}
}

// approvedExecBytes / approvedConfigBytes are what the operator approves;
// attackerExecBytes / attackerConfigBytes are what the malicious repository
// (P3) substitutes during the check-to-exec window. Digests of the attacker
// bytes must never appear in the child ledger.
var (
	approvedExecBytes   = []byte("#!/bin/sh\n# amux securitytest approved hook object (never executed by fixtures)\nexit 0\n")
	approvedConfigBytes = []byte("{\n  // amux securitytest approved hooks.jsonc\n  \"hooks\": [{\"id\": \"h1\", \"events\": [\"pane_exit\"]}]\n}\n")
	attackerExecBytes   = []byte("#!/bin/sh\n# SUBSTITUTED OBJECT - executing this is a conformance failure\nexit 66\n")
	attackerConfigBytes = []byte("{\n  \"hooks\": [{\"id\": \"h1\", \"events\": [\"pane_exit\", \"widened\"]}]\n}\n")
)

func sha256hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

type projectObjects struct {
	Root         string
	ExecPath     string
	ConfigPath   string
	ExecSHA256   string
	ConfigSHA256 string
}

// buildProject materializes a real project root with the approved hook
// executable and .amux/hooks.jsonc, so descriptor-bound validation has real
// objects to open and the mutation helpers have real objects to attack.
func buildProject(t *testing.T) projectObjects {
	t.Helper()
	root := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(filepath.Join(root, ".amux"), 0o755); err != nil {
		t.Fatal(err)
	}
	obj := projectObjects{
		Root:         root,
		ExecPath:     filepath.Join(root, "hook.sh"),
		ConfigPath:   filepath.Join(root, ".amux", "hooks.jsonc"),
		ExecSHA256:   sha256hex(approvedExecBytes),
		ConfigSHA256: sha256hex(approvedConfigBytes),
	}
	if err := os.WriteFile(obj.ExecPath, approvedExecBytes, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(obj.ConfigPath, approvedConfigBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	return obj
}

func defaultGrant(obj projectObjects) GrantSpec {
	return GrantSpec{
		HookID:         "h1",
		ExecutablePath: obj.ExecPath,
		AllowedEvents:  []string{"pane_exit"},
		ScopeKind:      ScopeNone,
		EnvAllowlist:   nil,
		TimeoutMS:      Gates.DefaultTimeoutMS,
		OutputCapBytes: Gates.OutputCapBytes,
	}
}

func activation(p ProjectID, session string) ActivationSpec {
	return ActivationSpec{Project: p, Hook: "h1", Event: "pane_exit", Session: session}
}

// trustedProject registers, approves, and grants in one step (session s1).
func trustedProject(t *testing.T, sut SystemUnderTest, obj projectObjects) ProjectID {
	t.Helper()
	p, err := sut.Trust().RegisterProject(obj.Root)
	if err != nil {
		t.Fatalf("RegisterProject: %v", err)
	}
	if _, err := sut.Trust().ApproveProject("s1", p); err != nil {
		t.Fatalf("ApproveProject: %v", err)
	}
	if _, err := sut.Hooks().ApproveHook("s1", p, defaultGrant(obj)); err != nil {
		t.Fatalf("ApproveHook: %v", err)
	}
	return p
}

func mustAwait(t *testing.T, sut SystemUnderTest, a ActivationID) ActivationResult {
	t.Helper()
	res, err := sut.Hooks().Await(a)
	if err != nil {
		t.Fatalf("Await(%s): %v", a, err)
	}
	return res
}

func assertDeny(t *testing.T, res ActivationResult, code v1.ErrorCode) {
	t.Helper()
	if res.Decision != DecisionDeny || res.Code != code {
		t.Fatalf("decision=%s code=%s, want deny %s (fail-closed)", res.Decision, res.Code, code)
	}
}

func assertZeroChildren(t *testing.T, sut SystemUnderTest) {
	t.Helper()
	if kids := sut.Observe().Children(); len(kids) != 0 {
		t.Fatalf("%d hook children created, want zero (fail-closed means zero processes)", len(kids))
	}
}

func assertAuditHas(t *testing.T, sut SystemUnderTest, kinds ...AuditKind) {
	t.Helper()
	recs := sut.Observe().Audit()
	i := 0
	for _, r := range recs {
		if i < len(kinds) && r.Kind == kinds[i] {
			i++
		}
	}
	if i != len(kinds) {
		t.Fatalf("audit trail missing ordered kinds %v (got %d records, matched %d)", kinds, len(recs), i)
	}
}

func runTiming(t *testing.T, factory Factory, f TimingFixture) {
	sut := factory(t)
	defer func() {
		if err := sut.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()
	obj := buildProject(t)

	switch f.Kind {
	case TimingAbsentTrust:
		// HA-3b / HA-22: registered-but-unapproved project answers deny within
		// the gate, with zero children and an audited denial.
		p, err := sut.Trust().RegisterProject(obj.Root)
		if err != nil {
			t.Fatalf("RegisterProject: %v", err)
		}
		start := sut.Clock().NowUnixMilli()
		a, err := sut.Hooks().Activate(activation(p, "s1"))
		if err != nil {
			t.Fatalf("Activate: %v", err)
		}
		res := mustAwait(t, sut, a)
		assertDeny(t, res, f.Expect.Code)
		if d := res.CompletedAtMS - start; d > f.GateMS {
			t.Fatalf("denial took %d ms, gate is %d ms", d, f.GateMS)
		}
		assertZeroChildren(t, sut)
		assertAuditHas(t, sut, AuditActivationDeny)

	case TimingRevokeCancel:
		// HA-18b: revocation from session s1 cancels session s2's queued work
		// within the gate, across sessions.
		p := trustedProject(t, sut, obj)
		sut.Barriers().Hold(SyncBeforeFinalValidation)
		a, err := sut.Hooks().Activate(activation(p, "s2"))
		if err != nil {
			t.Fatalf("Activate: %v", err)
		}
		if err := sut.Barriers().AwaitParked(SyncBeforeFinalValidation, a); err != nil {
			t.Fatalf("AwaitParked: %v", err)
		}
		revokeAt := sut.Clock().NowUnixMilli()
		if _, err := sut.Trust().RevokeProject("s1", p); err != nil {
			t.Fatalf("RevokeProject: %v", err)
		}
		sut.Barriers().Release(SyncBeforeFinalValidation)
		res := mustAwait(t, sut, a)
		assertDeny(t, res, f.Expect.Code)
		if d := res.CompletedAtMS - revokeAt; d > f.GateMS {
			t.Fatalf("cross-session cancel took %d ms, gate is %d ms", d, f.GateMS)
		}
		assertZeroChildren(t, sut)
		assertAuditHas(t, sut, AuditProjectRevoked, AuditActivationDeny)

	case TimingRevokeFirst:
		// HA-14: revoke linearizes before the final pre-spawn authorization
		// point => the activation fails closed and ZERO children exist.
		p := trustedProject(t, sut, obj)
		sut.Barriers().Hold(SyncBeforeFinalValidation)
		a, err := sut.Hooks().Activate(activation(p, "s1"))
		if err != nil {
			t.Fatalf("Activate: %v", err)
		}
		if err := sut.Barriers().AwaitParked(SyncBeforeFinalValidation, a); err != nil {
			t.Fatalf("AwaitParked: %v", err)
		}
		if _, err := sut.Trust().RevokeProject("s1", p); err != nil {
			t.Fatalf("RevokeProject: %v", err)
		}
		sut.Barriers().Release(SyncBeforeFinalValidation)
		res := mustAwait(t, sut, a)
		assertDeny(t, res, f.Expect.Code)
		assertZeroChildren(t, sut)
		assertAuditHas(t, sut, AuditProjectRevoked, AuditActivationDeny)

	case TimingLaunchFirst:
		// HA-15: launch linearized first; revoke terminates immediately and
		// kill-tree fires exactly at the 2000 ms boundary, all audit-visible.
		p := trustedProject(t, sut, obj)
		sut.Barriers().Hold(SyncAfterSpawn)
		a, err := sut.Hooks().Activate(activation(p, "s1"))
		if err != nil {
			t.Fatalf("Activate: %v", err)
		}
		if err := sut.Barriers().AwaitParked(SyncAfterSpawn, a); err != nil {
			t.Fatalf("AwaitParked: %v", err)
		}
		kids := sut.Observe().Children()
		if len(kids) != 1 {
			t.Fatalf("%d children after spawn barrier, want exactly 1", len(kids))
		}
		if kids[0].ExecSHA256 != sha256hex(approvedExecBytes) {
			t.Fatalf("spawned digest %s is not the approved object", kids[0].ExecSHA256)
		}
		if _, err := sut.Trust().RevokeProject("s2", p); err != nil {
			t.Fatalf("RevokeProject: %v", err)
		}
		sut.Barriers().Release(SyncAfterSpawn)
		sut.Clock().Advance(f.GateMS)
		if _, err := sut.Hooks().Await(a); err != nil {
			t.Fatalf("Await: %v", err)
		}
		kid := sut.Observe().Children()[0]
		if kid.TerminatedAtMS == 0 {
			t.Fatal("revocation did not terminate the in-flight hook")
		}
		if kid.KilledAtMS == 0 || kid.KilledAtMS-kid.TerminatedAtMS != f.GateMS {
			t.Fatalf("kill escalation at %d ms after terminate, want exactly %d ms", kid.KilledAtMS-kid.TerminatedAtMS, f.GateMS)
		}
		if kid.ExitedAtMS == 0 {
			t.Fatal("child exit was never recorded")
		}
		assertAuditHas(t, sut, AuditSpawn, AuditProjectRevoked, AuditTerminate, AuditKillEscalation, AuditExit)

	default:
		t.Fatalf("unknown timing kind %q", f.Kind)
	}
}

// mutate performs the fixture's object substitution while the activation is
// parked in the check-to-exec window (SyncAfterObjectOpen).
func mutate(t *testing.T, obj projectObjects, mutation string) {
	t.Helper()
	switch mutation {
	case MutationSymlinkSwap:
		attacker := filepath.Join(filepath.Dir(obj.Root), "attacker.sh")
		if err := os.WriteFile(attacker, attackerExecBytes, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(obj.ExecPath); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(attacker, obj.ExecPath); err != nil {
			t.Fatal(err)
		}
	case MutationRenameSwap:
		attacker := filepath.Join(obj.Root, "attacker.sh")
		if err := os.WriteFile(attacker, attackerExecBytes, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(attacker, obj.ExecPath); err != nil {
			t.Fatal(err)
		}
	case MutationExecByteReplace:
		if err := os.WriteFile(obj.ExecPath, attackerExecBytes, 0o755); err != nil {
			t.Fatal(err)
		}
	case MutationConfigByteReplace:
		if err := os.WriteFile(obj.ConfigPath, attackerConfigBytes, 0o644); err != nil {
			t.Fatal(err)
		}
	case MutationProjectRootReplace:
		aside := obj.Root + ".orig"
		if err := os.Rename(obj.Root, aside); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(obj.Root, ".amux"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(obj.ExecPath, attackerExecBytes, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(obj.ConfigPath, attackerConfigBytes, 0o644); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unknown mutation %q", mutation)
	}
}

func runRace(t *testing.T, factory Factory, f RaceFixture) {
	sut := factory(t)
	defer func() {
		if err := sut.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()
	obj := buildProject(t)
	p := trustedProject(t, sut, obj)

	sut.Barriers().Hold(SyncAfterObjectOpen)
	a, err := sut.Hooks().Activate(activation(p, "s1"))
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if err := sut.Barriers().AwaitParked(SyncAfterObjectOpen, a); err != nil {
		t.Fatalf("AwaitParked: %v", err)
	}
	mutate(t, obj, f.Mutation)
	sut.Barriers().Release(SyncAfterObjectOpen)
	res := mustAwait(t, sut, a)

	// HA-11: exactly two legal outcomes — the APPROVED object executed, or
	// launch failed closed hook_grant_stale with zero children. Under no
	// outcome does the substituted object's digest appear in the ledger.
	kids := sut.Observe().Children()
	switch res.Decision {
	case DecisionAllow:
		if len(kids) != 1 {
			t.Fatalf("allow with %d children, want exactly 1", len(kids))
		}
		if kids[0].ExecSHA256 != sha256hex(approvedExecBytes) {
			t.Fatalf("executed digest %s != approved digest (descriptor binding broken)", kids[0].ExecSHA256)
		}
		if kids[0].ConfigSHA256 != sha256hex(approvedConfigBytes) {
			t.Fatalf("validated config digest %s != approved digest", kids[0].ConfigSHA256)
		}
	case DecisionDeny:
		if res.Code != v1.ErrHookGrantStale {
			t.Fatalf("deny code %s, want %s", res.Code, v1.ErrHookGrantStale)
		}
		assertZeroChildren(t, sut)
		assertAuditHas(t, sut, AuditActivationDeny)
	default:
		t.Fatalf("unknown decision %q", res.Decision)
	}
	for _, k := range kids {
		if k.ExecSHA256 == sha256hex(attackerExecBytes) {
			t.Fatal("the substituted object executed — check-to-exec race lost")
		}
	}
}

func runRestore(t *testing.T, factory Factory, f RestoreFixture) {
	sut := factory(t)
	defer func() {
		if err := sut.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()
	obj := buildProject(t)

	// Establish live state: approve (epoch e1), grant, revoke (epoch e2,
	// grant inactive). For the grant-reactivate claim, reapprove the project
	// (epoch e3) so only the grant's inactivity stands between the forged
	// generation and a launch.
	p, err := sut.Trust().RegisterProject(obj.Root)
	if err != nil {
		t.Fatalf("RegisterProject: %v", err)
	}
	e1, err := sut.Trust().ApproveProject("s1", p)
	if err != nil {
		t.Fatalf("ApproveProject: %v", err)
	}
	g, err := sut.Hooks().ApproveHook("s1", p, defaultGrant(obj))
	if err != nil {
		t.Fatalf("ApproveHook: %v", err)
	}
	e2, err := sut.Trust().RevokeProject("s1", p)
	if err != nil {
		t.Fatalf("RevokeProject: %v", err)
	}
	if e2 <= e1 {
		t.Fatalf("revocation did not increase the epoch (%d -> %d)", e1, e2)
	}
	live := e2
	expectCode := v1.ErrProjectTrustRequired
	if f.Claim == ClaimGrantReactivate {
		e3, err := sut.Trust().ApproveProject("s1", p)
		if err != nil {
			t.Fatalf("reapprove: %v", err)
		}
		if e3 <= e2 {
			t.Fatalf("reapproval did not increase the epoch (%d -> %d)", e2, e3)
		}
		live = e3
		expectCode = v1.ErrHookGrantRequired // grant stays inactive (HA-18e)
	}
	auditBefore := len(sut.Observe().Audit())

	gen := GenerationFixture{}
	switch f.Claim {
	case ClaimEpochDecrease:
		gen.ClaimedEpochs = map[ProjectID]Epoch{p: e1}
	case ClaimGrantReactivate:
		gen.ClaimedActiveGrants = []GrantID{g}
	case ClaimAuditErase:
		gen.OmitsAuditHistory = true
	case ClaimLaunchAuthority:
		gen.ClaimsLaunchAuthority = true
	default:
		t.Fatalf("unknown restore claim %q", f.Claim)
	}

	// Restore may reject the generation outright or accept it while ignoring
	// its trust claims — both are conformant. What it must never do is honor a
	// claim; the post-conditions below observe that directly.
	_ = sut.Restore().ImportGeneration(gen)

	after, err := sut.Trust().Epoch(p)
	if err != nil {
		t.Fatalf("Epoch: %v", err)
	}
	if after < live {
		t.Fatalf("epoch decreased across restore: %d -> %d (HA-4a/HA-18 broken)", live, after)
	}
	if got := len(sut.Observe().Audit()); got < auditBefore {
		t.Fatalf("audit history shrank across restore: %d -> %d (AUD-6 broken)", auditBefore, got)
	}
	a, err := sut.Hooks().Activate(activation(p, "s1"))
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	res := mustAwait(t, sut, a)
	assertDeny(t, res, expectCode)
	assertZeroChildren(t, sut)
}

func runRedaction(t *testing.T, factory Factory, f RedactionVector) {
	sut := factory(t)
	defer func() {
		if err := sut.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()
	secret := DeriveCandidateSecret(f.Label)
	payload := []byte(strings.ReplaceAll(f.Payload, SecretPlaceholder(f.Label), secret))
	if f.TruncateAtBytes > 0 {
		// RED-5: truncate FIRST (as the output cap does), then redact. The
		// vector positions the secret across the boundary; the surviving
		// prefix must still be caught.
		if int64(len(payload)) > f.TruncateAtBytes {
			payload = payload[:f.TruncateAtBytes]
		}
		if !strings.Contains(string(payload), CandidateSecretPrefix) {
			t.Fatalf("vector misconfigured: truncation at %d left no secret prefix to test", f.TruncateAtBytes)
		}
	}
	out, err := sut.Redactor().Redact(f.Context, payload)
	if err != nil {
		// RED-8: an engine error must fail closed (payload dropped/replaced),
		// never pass through — surfacing the error to the caller is the
		// conformant refusal here.
		return
	}
	s := string(out)
	if strings.Contains(s, secret) {
		t.Fatalf("raw candidate secret egressed context %q", f.Context)
	}
	if strings.Contains(s, CandidateSecretPrefix) {
		t.Fatalf("candidate-secret remnant (prefix) egressed context %q — truncation/partial redaction leak", f.Context)
	}
	if f.TruncateAtBytes == 0 && !strings.Contains(s, RedactionMarker(f.Label)) {
		t.Fatalf("output lacks the %s marker (secret silently dropped or mangled instead of redacted)", RedactionMarker(f.Label))
	}
}
