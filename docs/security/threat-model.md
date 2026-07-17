# Amux threat model (T2-security, frozen pre-implementation)

Status: frozen 2026-07-15 (run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0, lane T2-security).
Scope: the MVP surface approved in `.guild/spec/amux-go-linux-runtime.md` and the
T1-frozen contracts (`docs/adr/0001`–`0007`, `internal/platform/platform.go`,
`api/v1`). This document is a contract input to T4 (backend) and T6 (QA); changes
to any threat's mitigation status that weaken a guarantee require the spec
confirmation gate, not a doc edit.

Every requirement in this document and its companions carries a stable ID
(`TM-*`, `HA-*` in `hook-authorization.md`, `STR-*` in
`local-transport-hardening.md`, `RED-*`/`AUD-*` in `redaction-and-audit.md`).
Fixtures in `internal/securitytest` and rows in
`testdata/security/trust-matrix.json` reference these IDs; the readiness
manifest (`docs/security/readiness-manifest.json`) maps them to blocking checks.

## 1. Assets

| ID | Asset | Why it matters |
|----|-------|----------------|
| A1 | Daemon authority (graph, PTY, event, snapshot, hook state) | Sole state authority; corruption or bypass subverts every other control. |
| A2 | Project trust state: epochs, grants, audit history (SQLite-only, control actor) | The entire hook capability system keys off it. ADR-0005: epochs never decrease, snapshots never import it. |
| A3 | Hook execution capability | The only place Amux deliberately runs third-party-influenced executables. |
| A4 | Operator terminal input/output (PTY streams, replay rings) | Contains whatever the operator types/sees, including credentials. |
| A5 | Environment and cwd of spawned processes | Ambient secrets (env) and filesystem reach (cwd) are the classic hook-abuse vectors. |
| A6 | Control socket beneath `$XDG_RUNTIME_DIR/amux/` | Full command authority for whoever can connect. |
| A7 | Persistence: snapshot generations, SQLite DB, audit/event logs | Restore-time integrity; a forged generation is a time-travel attack on A2. |
| A8 | Diagnostics (`pprof`, inspection endpoints, logs) | Leak channel for A4/A5 material; DoS amplifier. |
| A9 | Release artifacts (tarballs, AUR package) and dependency graph | Supply-chain: a compromised dep or artifact defeats everything above. |

## 2. Actors

| ID | Actor | Trust level |
|----|-------|-------------|
| P1 | Operator (daemon-owning UID) | Fully trusted. Same-UID compromise is out of scope (see §7). |
| P2 | Other local users (second UID on the host) | Untrusted. Must be unable to connect, read, signal, or exhaust. |
| P3 | Malicious repository author | Untrusted. Controls file contents, symlinks, `.amux/hooks.jsonc`, and timing of filesystem mutations inside any directory the operator opens as a pane cwd. Never controls anything until explicit project opt-in + per-hook grant. |
| P4 | Compromised hook executable | Semi-trusted-then-hostile: was approved (digest-bound), then behaves maliciously at runtime. Bounded by timeout, output cap, env allowlist, cwd scope, redaction — not by OS sandboxing (§7). |
| P5 | Stale or malicious local client (same UID) | Protocol-level untrusted input: malformed frames, hostile lengths, replay-cursor abuse, slow consumption. |
| P6 | Malformed agent adapter payload | Untrusted input on the adapter boundary; must not reach graph mutation or hook launch except through typed daemon commands. |
| P7 | Corrupted persistence (crash, bit rot, or an old/foreign snapshot presented at restore) | Untrusted data; restore validates, never trusts. |
| P8 | Compromised upstream dependency | Supply-chain actor; mitigated by ADR-0007 pinning + the scanner gates in `security-readiness.md`. |

## 3. Trust boundaries (S1 inventory)

| ID | Boundary | Crossing | Primary controls |
|----|----------|----------|------------------|
| B1 | Socket | client ↔ daemon over Unix socket | Owner-only runtime dir + socket mode, mandatory `SO_PEERCRED` UID check before first protocol byte, frame limits (`api/v1`: header 1 MiB, body 8 MiB), version negotiation. `STR-1..STR-9`. |
| B2 | Filesystem | daemon ↔ project roots, hook executables/config, runtime dir | Canonical identity tuple (realpath, `st_dev`, `st_ino`) → SHA-256 project key; descriptor-bound open/validate/exec (`openat2` + `fexecve` semantics, no symlink traversal); no-symlink runtime-path validation. `HA-2`, `HA-10..HA-13`, `STR-2..STR-4`. |
| B3 | Process | daemon ↔ hook children and PTY children | Containment (process group + guardian/cgroup), terminate→2 s kill escalation, orphan reaping, spawn ledger auditing. `HA-14..HA-17`. |
| B4 | Environment | daemon env ↔ child env | Explicit per-grant env-key allowlist; empty by default; inherited environment never passed through. Snapshots persist only the explicit non-secret allowlist (ADR-0005). `HA-8`, `RED-3`. |
| B5 | PTY | operator terminal bytes ↔ daemon replay/event pipeline | Raw bytes are operator-classified data: replay bounded (16 MiB floor, budgeted), input leases gate writes, hooks never receive raw PTY bytes unless an event type explicitly carries a redacted excerpt. `RED-4`, `AUD-3`. |
| B6 | Notification | daemon store ↔ desktop delivery adapter | Store is authoritative (ADR-0005/0006); delivery is advisory, errors never mutate state; notification payloads pass redaction before leaving the daemon. `RED-6`. |
| B7 | Diagnostics | daemon internals ↔ `pprof`/inspection/logs | Owner-only, explicitly enabled, bounded; logs/audit pass redaction. `STR-10..STR-12`, `RED-5`, `AUD-*`. |
| B8 | Release | source + deps ↔ shipped artifacts | Pinned module graph (`go.mod`/`go.sum`, ADR-0007), vulnerability/secret/license scans and provenance gates frozen in `security-readiness.md` (pipeline wiring is T3 devops). |

## 4. STRIDE by boundary

Legend: **M** mitigated by a frozen requirement (ID cited), **R** residual (listed in §6), **N** explicit non-guarantee (§7).

### B1 socket
- **S**poofing: P2 connects to the socket → M (`STR-1..STR-3`: dir/socket owner+mode, mandatory peer-UID check pre-protocol).
- **T**ampering: symlinked or pre-created hostile socket path → M (`STR-4`: no-symlink component walk, owner/mode/type validation, stale-socket liveness proof before unlink).
- **R**epudiation: unauthenticated command origin → M (`AUD-1`: connection identity in audit records).
- **I**nfo disclosure: P2 reads socket → M (`STR-1..STR-2`); leaked fd inheritance → M (`STR-9` close-on-exec).
- **D**oS: connection floods, hostile frame lengths, slow consumers → M (`STR-5..STR-8`: frame limits pre-allocation, connection cap, per-connection read deadlines, slow-consumer disconnect per ADR-0004).
- **E**levation: protocol bug grants unintended method → M (strict decode `api/v1.DecodeStrict`, frozen taxonomy; QA integrated suite).

### B2 filesystem
- **S**: P3 substitutes a different executable behind an approved path (symlink swap, rename, byte replacement, root replacement/remount) → M (`HA-10..HA-13`: descriptor-bound validate+exec; identity tuple invalidation). Fixtures: `races.*`.
- **T**: `.amux/hooks.jsonc` edited after grant → M (`HA-7`: config digest binding → `hook_grant_stale`).
- **R**: which object actually ran → M (`AUD-2`: audit records digest actually validated on the executed descriptor).
- **I**: hook config read before opt-in → M (`HA-1`: config ignored until explicit opt-in).
- **D**: hostile filesystem (FIFOs, huge files, device nodes at hook paths) → M (`HA-12`: regular-file type check on the opened descriptor; open with `O_NOFOLLOW|O_NONBLOCK` semantics).
- **E**: cwd escape via `workspace-primary`/`pane` resolution → M (`HA-9`, matrix rows `scope.*`: same-project-identity requirement, fail-closed on absent/ambiguous/replaced/foreign roots).

### B3 process
- **S**: PID-reuse confusion in kill/reap paths → M (`HA-16`: containment handle targets the tree, not a raced PID).
- **T**: hook double-forks to escape termination → M (`HA-15`: process-group + guardian containment; KillTree idempotent).
- **R**: child lifecycle unaccounted → M (`AUD-2`/`AUD-4`: spawn/terminate/kill/exit audit).
- **I**: child inherits descriptors → M (`STR-9`, `HA-17`: minimal fd set, close-on-exec).
- **D**: runaway hook output/time → M (`HA-8`: 2 s default / 30 s max timeout, 1 MiB output cap, enforced kill escalation).
- **E**: hook launched after revoke → M (`HA-14`: linearizable launch/revoke; revoke-first ⇒ zero children). Launch-first execution window → N (§7.3).

### B4 environment / B5 PTY
- **I**: secrets leak into hook env, snapshots, or hook-visible payloads → M (`HA-8` allowlist; ADR-0005 snapshot policy; `RED-1..RED-8`).
- **T**: non-lease-holder injects PTY input → M (ADR-0004 input leases; rejected before the seam).
- **D**: replay-ring exhaustion → M (bounded rings + storage budget; `STR-8`).

### B6 notification / B7 diagnostics
- **I**: notification/diagnostic payloads carry unredacted secrets → M (`RED-5`, `RED-6`).
- **T**: delivery failure mutates store → M (ADR-0006 advisory-error contract; seam-frozen).
- **D**: pprof/diagnostics abused by P2 or for amplification → M (`STR-10..STR-12`: disabled by default, owner-only, bounded).
- **E**: diagnostics expose mutation → M (`STR-11`: read-only inspection surface).

### B8 release
- **S/T**: dependency or artifact substitution → M (pinned `go.sum`, `go mod verify`, checksummed artifacts; scanner gates in `security-readiness.md`; pipeline = T3).
- **I**: committed secrets → M (`.gitleaks.toml` policy + history scan gate).

### Persistence (P7, crosses B2/B7)
- **T**: old/foreign snapshot generation decreases a trust epoch, reactivates a grant, erases audit, or authorizes launch → M (`HA-18..HA-21`; ADR-0005 authority table; persist.Manifest checksum + commit-point rules). Fixtures: `restore.*`.
- **D**: corrupt generation blocks startup → M (previous-known-good rule; fail closed with exportable diagnostic, never partial load).

## 5. Abuse cases (must stay red-team-tested)

| ID | Abuse case | Expected outcome | Pinned by |
|----|------------|------------------|-----------|
| AB-1 | Clone a repo containing `.amux/hooks.jsonc`; open a pane in it | Zero hook processes, `project_trust_required` within 250 ms; config not parsed before opt-in | `timing.absent-trust`, matrix `project.*` |
| AB-2 | After approval, repo swaps the hook executable via symlink/rename/byte-rewrite timed against launch | Approved bytes execute, or launch fails closed `hook_grant_stale`; never the substituted object | `races.*` |
| AB-3 | Revoke trust in session 1 while session 2 has queued invocations | Queued work canceled ≤ 250 ms cross-session, no later launch, inactive history retained | `timing.revoke-cancel`, `timing.revoke-first` |
| AB-4 | Revoke while a hook is in flight | Terminate, kill after 2 s, both steps audited | `timing.launch-first` |
| AB-5 | Grant `pane` scope in project X, then point the pane at project Y (or an unregistered dir) | Denied before launch, `scope_denied` | matrix `scope.pane.*` |
| AB-6 | `workspace-primary` scope with zero/two/replaced primary roots | Denied before launch, `scope_denied` | matrix `scope.wsprimary.*` |
| AB-7 | Restore an old snapshot taken while trust was active, after revocation | Epoch unchanged (monotonic), grant stays inactive, audit intact, no launch authority | `restore.*` |
| AB-8 | Hook floods stdout / never exits | Output truncated at 1 MiB (truncation redaction-safe), timeout kill at bound | matrix `bounds.*`, `redaction.truncation` |
| AB-9 | Hook requests `AWS_SECRET_ACCESS_KEY` via env allowlist widening | Requires explicit operator confirmation (lane autonomy: widening = confirm); non-allowlisted keys never injected | matrix `env.*` |
| AB-10 | Second UID pre-creates the runtime dir/socket path, or connects | Bind refused / peer rejected pre-protocol; audited | `STR-1..STR-4` tests (T4/T6) |
| AB-11 | Malicious client sends 2 GiB header length / trailing garbage / unknown durable fields | Fail closed before allocation; strict decode rejects | `api/v1` golden + `STR-5..STR-7` |
| AB-12 | Secrets typed into a pane appear in hook payloads, logs, audit, snapshots, notifications | Redaction classes applied at every egress in `RED-1..RED-8`; raw secret never persisted outside PTY replay ring | `redaction.*` |

## 6. Residual risks (accepted, tracked)

| ID | Residual risk | Severity | Rationale for acceptance |
|----|---------------|----------|--------------------------|
| RR-1 | Launch-first ordering executes some hook instructions before kill completes (§7.3 window) | Medium | Approved spec semantics: linearizable no-launch-after-revoke is guaranteed; retroactive zero-execution is explicitly not claimed. Bounded by timeout/output/env/cwd controls and full audit. |
| RR-2 | An approved hook binary can do anything its digest-approved code does within its grant (read granted cwd, spend its timeout, use allowlisted env) | Medium | Inherent to running operator-approved code without OS sandboxing (spec non-goal). Grant minimization + default-deny cwd/env keep the floor low. |
| RR-3 | PTY replay rings intentionally store raw terminal bytes (may include typed secrets) on disk unredacted | Medium | Product requirement (raw bytes are protocol truth). Bounded, owner-only-file-mode requirement `RED-4`; snapshots exclude rings from any redaction *bypass* path; documented operator guidance. |
| RR-4 | Digest binding does not re-verify transitively loaded libraries of an approved executable | Low | Path-of-record is the executable+config objects; transitive library integrity is host integrity (P1 domain). |
| RR-5 | Host-clock abuse cannot defeat epochs (counter-based) but can skew audit timestamps | Low | Audit ordering uses monotonic sequence + epoch, wall time is advisory (`AUD-5`). |

No residual risk here is high-severity; per the lane's autonomy policy, accepting a
high-severity residual would require orchestrator confirmation — none was needed.

## 7. Explicit non-guarantees

1. **No OS-level sandboxing or network isolation** for hooks (spec non-goal). A granted hook has the operator's UID and unrestricted network reach for its bounded lifetime.
2. **No same-UID defense**: an attacker already running code as the daemon owner's UID can do everything the operator can (connect, approve, read SQLite). All controls target P2–P8, not P1 compromise.
3. **No retroactive zero-execution**: if launch linearizes before revoke, instructions may execute during the terminate→2 s kill window (RR-1). The guarantee is *no launch linearizes after a completed revoke* — pinned by `timing.revoke-first`/`timing.launch-first`.
4. **No multi-user or remote operation**: single-user local socket only; any network exposure is a spec change.
5. **No secrecy of raw PTY history from the operator's own UID** (RR-3).
6. **No cmux compatibility surface** — no inherited compatibility promises to secure.

## 8. Mitigation ownership

Security (this lane) freezes the contract and ships the executable fixtures.
T4 backend implements enforcement behind the frozen `internal/platform` seams and
must pass `securitytest.RunConformance` with a real system under test. T6 QA
executes the same fixtures plus the integrated checks in
`docs/security/readiness-manifest.json` before any release promotion. T3 devops
wires the scanner/provenance pipeline per ADR-0007 (independent of this lane's
output by plan).
