# T4-backend — B1–B12 backend lane receipt (G-lane round-2 rework)

- run: run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0
- task: T4-backend (backend specialist, tier powerful)
- status: done
- date: 2026-07-16
- host: macOS (darwin/arm64, go1.26.5) — portable evidence; Linux-only runtime evidence remains explicitly deferred to T6 with reproducible commands (§7)
- supersedes: the round-1-rework receipt reviewed as `review/G-lane:T4-backend/artifact-round2.md` (sha256 c214733…938)

This receipt covers the full T4 lane after the mandatory G-lane round-2 rework:
the preserved, verified B1–B12 checkpoint (including the accepted F1 closure)
plus the closure of the round-2 blocking finding. Nothing outside that
finding's blast radius was regenerated.

## 1. Review response — G-lane round 2

**F2 (blocking): "Restore can report `restarted` without actually relaunching
the surface process" — CLOSED with production code + production-path/E2E
evidence.**

The finding was correct: the production restore path classified an
automatic-policy surface `restarted` via the frozen classifier, then only
installed the rebuilt replay/VT runtime — `RestoreSnapshot` never spawned the
replacement PTY, so the reported class could be false. ADR-0005 defines
`restarted` as a NEW process launched under an explicit automatic policy — a
claim about completed behavior, not intent. That behavior now exists:

- **Relaunch is part of restore.** A surface classified `restarted` is only
  PENDING at commit (installed fail-closed as stopped, "relaunch pending",
  never a live owner). After the commit installs the rebuilt runtimes and
  stops every unvouched live process, `RestoreSnapshot` spawns the
  replacement PTY for each pending surface through the session's production
  supervisor — restored argv/cwd/env, restore geometry 24×80 (the size the
  rebuilt VT grid derives at) — so its output lands in the restored ring
  after the checkpoint history and exit tracking flows through the same
  supervisor callbacks.
- **`restarted` only after success.** Only when the spawn returns success is
  the surface marked live with class `restarted`, reason "relaunched under
  automatic restart policy", and a freshly minted spawn identity — so only
  checkpoints saved from that point on can vouch for the replacement (a later
  in-daemon restore adopts it live; proven by test).
- **Fail-closed launch failure.** A failed spawn demotes the surface to
  `stopped` with the exact reason `automatic restart policy but relaunch
  failed: <error>`. It can never read as `restarted` or `live`; no false live
  owner is retained (engine flag and supervisor agree); the unvouched
  predecessor was already stopped by the commit path; and the surface remains
  explicitly restartable once launching works again.
- **Stop-then-relaunch race handled.** A just-stopped predecessor on the same
  surface id retires asynchronously in the supervisor, so the relaunch
  retries a spawn conflict within a bounded 5 s window; window expiry or any
  other error is a launch failure (fail closed as above).
- **Round-1 guarantees preserved verbatim.** Live adoption precedes restarted
  (a still-owned automatic-policy surface reconciles live, untouched, never
  relaunched — pinned in E2E); ownership mismatch, manual/stopped, and
  clean/fresh-daemon paths never claim live; fresh-daemon automatic restore
  now performs a REAL replacement launch under the new daemon's supervisor;
  fresh-daemon manual restore launches nothing (replay marker counts pinned).
- **No contract or compatibility change.** `persist.Classify` and ADR-0005
  are untouched; graph schema, wire protocol, CLI contracts, and SQLite
  schemas unchanged; trust epochs/grants/audit stay structurally out of
  restore's reach; the notification export remains the only import. The
  frozen contract was implementable without amendment — the ask-gate was not
  triggered.

## 2. B1–B12 coverage

Unchanged from the verified round-1 checkpoint except **B8/B9**, restated:

| Pkg | Package(s) | State | Key evidence |
|---|---|---|---|
| B1 binary/config/lifecycle | `cmd/amuxd`, `internal/config`, `internal/daemon/run.go`, `internal/version` | done | XDG fail-closed resolution, strict JSONC with located errors, boot identity, signal handling, single-instance socket; production-assembly test proves second-daemon refusal + clean protocol shutdown; logs off protocol streams; `FuzzDecodeJSONC` clean. |
| B2 control + session actors | `internal/control`, `internal/session`, `internal/domain` | done | Global trust/registry actor; per-session graph actors; bounded mailboxes; no blocking I/O on actor goroutines; property tests over arbitrary valid command sequences; cross-session revoke via control tests + conformance. |
| B3 protocol/transport/client | `api/v1`, `internal/protocol`, `internal/transport/local`, `internal/client`, `internal/rpcapi` | done | Bounded frames, strict durable decode, handshake/version-skew, peer-UID gate, symlink-free socket validation, stale-socket reclaim-after-proof, golden vectors, reconnecting client, `FuzzServerHeader` clean. |
| B4 event replay/recovery | `internal/session`, `internal/ordering` | done | Commit-then-allocate sequences, bounded replay, filtered subscriptions, heartbeats, slow-consumer policy, typed `event_gap` (E2E exit 7). |
| B5 PTY supervisor | `internal/pty`, `internal/platform` | done (portable) / Linux runtime → T6 | Spawn/resize/input/signal/exit/cancel/reap centralized; explicit cwd fail-closed; A6 containment compiled for Linux, fail-closed elsewhere. |
| B6 raw replay + VT engine | `internal/terminal` | done | Bounded ring (16 MiB floor), sequence-safe restore, golden fixture→grid corpus, `FuzzEngineFeed` clean. |
| B7 attach + input leases | `internal/attach` | done | Linearized snapshot→replay(≤N)→live(>N) cutover, one lease per surface, takeover/release/disconnect receipts; stress `-count=20` = 300 pass, `-race -count=5` = 75 pass; detach never stops the process. |
| B8 persistence/restore | `internal/persist`, `internal/snapshot`, `internal/store`, `internal/daemon/{snapshot,truststore}.go` | **done — round-2 rework made `restarted` true in behavior** | Versioned manifest, component-first fsync, manifest-last commit, previous-known-good retention + diagnostic refusal, schema 0→1 migration, WAL SQLite, monotonic epochs, trust excluded from layout restore. Production classification complete AND enacted: **in-daemon live reconcile** (same still-owned spawn identity → live, untouched), **automatic-policy restore actually launches the replacement PTY and reports `restarted` only after success; launch failure fails closed to stopped with the exact reason**, **clean/fresh-daemon restore never live** (structural) with automatic policy performing a real relaunch, **manual policy never launches**, stale security generation/grants excluded and fail-closed (conformance `restore.*` 4/4). §1 has the design; §4 the tests. |
| B9 CLI + machine contracts | `cmd/amux` (6 files) | done | All command families; `--json` stable schemas; exit-code table; completions; fail-closed confirmation matrix. **20-flow E2E extended, not disguised**: flow 11 additionally spawns an automatic-policy surface whose marker embeds `$(pwd)`; flow 17 proves live-precedes-restarted on the first restore, then a stop→restore cycle proves restore itself relaunches the automatic surface (fresh marker in the restored stream, answers input, operator restart refuses conflict) while the manual surface reconciles live; the fresh-daemon segment proves automatic → real relaunch + input echo and manual → stopped with exactly the checkpoint's marker. Flows 18/19 (operator stop→restart) remain their own distinct segment on the manual surface. |
| B10 context/notify/adapters | `internal/context`, `internal/notify` | done | Bounded/debounced collectors off-actor; daemon-owned notification persistence/routing/read-state; 2 structured adapters (claude, codex) with redaction fixtures; `FuzzAdapterFeed` clean. |
| B11 hook runtime | `internal/hooks` | done (portable) / kernel races → T6 | Opt-in config, descriptor-bound launch, env allowlist, timeout/output caps, audit, redaction; conformance 26/26 subchecks with the frozen securitytest package unmodified. |
| B12 diagnostics/perf | internal observability | done | `slog` correlation, metrics registry, bounded deterministic dump, owner-gated pprof, OTel wiring, benchmark entry points. |

## 3. Changed files (this rework session — everything else preserved)

- `internal/daemon/snapshot.go` — `RestoreSnapshot`: pending-relaunch commit
  (a `restarted` classification installs fail-closed as stopped/"relaunch
  pending"), post-commit relaunch loop (restored argv/cwd/env, 24×80) that
  reports `restarted` only after a successful spawn and mints the
  replacement's spawn identity; `spawnReplacement` bounded conflict-retry
  helper; doc comments state the completed-behavior semantics
- `internal/daemon/restore_test.go` — 3 new production-path tests (§4) plus a
  spec-recording PTY fake, a launch-failure injection fake, an explicit-policy
  spawn helper, and never-launch assertions added to the manual-policy and
  fresh-daemon tests
- `cmd/amux/e2e_test.go` — automatic-policy surface added to `TestTwentyFlows`
  with in-daemon and fresh-daemon relaunch segments (§2 B9)

## 4. New acceptance evidence (production path + E2E)

All drive `RestoreSnapshot` itself (round-1 tests preserved unchanged in
behavior; two gained never-launch assertions):

1. `<HIGH_ENTROPY_REDACTED>` — save → confirmed stop →
   restore: classifies `restarted` with the exact ADR reason; the recording
   PTY seam proves EXACTLY one replacement launch with the restored
   argv/cwd/env and 24×80 geometry; the replacement is live (restart refuses
   conflict) and its marker joins the restored stream (count 2); a second
   save/restore cycle adopts the replacement live (minted identity is
   attestable).
2. `<HIGH_ENTROPY_REDACTED>` — the PTY seam
   fails the relaunch: restore succeeds, the surface reports `stopped` with
   reason "automatic restart policy but relaunch failed: <the exact error>";
   engine flag and supervisor agree nothing is live; replay holds only the
   checkpoint history; once the seam recovers, explicit restart works.
3. `<HIGH_ENTROPY_REDACTED>` — a second engine over
   the same snapshot root restores the automatic surface `restarted` with a
   REAL replacement live under ITS supervisor and the fresh marker appended
   (count 2).
4. `<HIGH_ENTROPY_REDACTED>` (round 1, preserved) —
   still-owned surface reconciles live, untouched, exactly one spawn marker.
5. `<HIGH_ENTROPY_REDACTED>` (extended) — manual policy
   stays stopped AND never launches: marker count pinned to 1, supervisor
   owns no process.
6. `<HIGH_ENTROPY_REDACTED>` (round 1, preserved) —
   stop→restart impostor never live; stopped fail-closed.
7. `<HIGH_ENTROPY_REDACTED>` (extended) —
   fresh-daemon manual restore never live and never launches (marker count
   pinned to 1).
8. `TestTwentyFlows` (black-box E2E, real binary/daemon/PTYs) — flow 11 adds
   an automatic-policy `/bin/sh` surface marking `e2e-auto $(pwd)`; flow 17
   first restore: BOTH still-owned surfaces reconcile live (live precedes
   restarted); then confirmed stop → second restore: the automatic surface is
   `restarted` by the restore itself — fresh cwd marker (same
   executable/argv/cwd) joins the restored history, answers input under a new
   lease, operator restart refuses (conflict, exit 4) — while the manual
   surface stays live; flows 19/18 unchanged (operator stop→restart of the
   manual surface); after reboot the fresh daemon restores automatic →
   `restarted` with a real launch + input echo, manual → `stopped` with
   exactly the checkpoint's single marker.

## 5. Exact verification evidence (this session, post-change)

All commands from repo root, go1.26.5 darwin/arm64:

1. `gofmt -l cmd internal api` → empty
2. `go vet ./...` → clean
3. Focused restore tests: `go test ./internal/daemon/ -count=1` → **20 pass** (3 new)
4. 20-flow E2E: `go test ./cmd/amux/ -count=1 -run TestTwentyFlows` → PASS (real binary, real daemon boots TWICE, real `/bin/sh` PTYs, restore-relaunched replacement answers real input)
5. `go test -count=1 ./...` → **596 passed / 33 packages** (593 at round 1 + 3 new)
6. `go test -race -count=1 ./...` → **596 passed / 33 packages**
7. Attach stress: `go test -count=20 ./internal/attach` → 300 pass; `go test -race -count=5 ./internal/attach` → 75 pass
8. Security conformance: `go test -count=1 -run Conformance ./internal/hooks` → 26 pass (timing.*, races.*, restore.*, redaction.*), frozen securitytest package unmodified
9. Cross-compile: `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./...` OK; `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...` OK (no cgo maintained)
10. Modules: `go mod verify` → all verified; `go mod tidy -diff` → empty

## 6. Scope compliance

- Session writes: exactly the 3 files in §3 plus this receipt — all inside
  `scope/T4-backend.json` write-globs (`internal/**`, `cmd/amux/**`, receipt
  path). `go.mod`/`go.sum` untouched (tidy clean).
- Forbidden-surface check: `find docs/adr docs/security <HIGH_ENTROPY_REDACTED>
  .github/workflows packaging docs/operations docs/release internal/tui
  testdata/tui research -newermt "2026-07-16 06:58" -type f` → **0 files**.
- No ADR was changed, no persisted or protocol compatibility was altered
  (graph schema still v1, no new persisted fields, no new wire fields — the
  relaunch is runtime behavior behind the existing class/reason contract), no
  trust semantic touched, no cgo, no fake classification. The frozen contract
  was implementable without amendment, so the ask-gate was not triggered.
- Standing caveat from earlier rounds: the repository still has no git
  baseline, so the audit is session write-attestation + mtime windows;
  recommend a baseline commit before T5/T6.

## 7. Honest deferrals — Linux-only runtime evidence (T6 prerequisites)

Unchanged from round 1 (portable/macOS evidence is real but not a Linux
runtime claim): full suite + race on Arch/Ubuntu CI (SO_PEERCRED,
openat2/execveat, A6 containment, Linux probes), subprocess-daemon E2E path,
second-UID/resource-exhaustion/kernel-race matrix on a two-user host, forced
SIGKILL orphan scans, soak/perf gates, and the T2 scanner debts
(`govulncheck`, `gitleaks`, `go-licenses`) — commands frozen in
`docs/security/security-readiness.md`. The in-daemon live-reconcile AND the
automatic-policy relaunch E2E run on Linux CI via the same `TestTwentyFlows`.

## 8. Risks and residuals

- Automatic-policy restore semantics are now exact: success = replacement PTY
  spawned with restored argv/cwd/env at 24×80, surface live, class
  `restarted`, new attestable spawn identity; failure = class `stopped` with
  reason "automatic restart policy but relaunch failed: <error>", no live
  owner anywhere, predecessor already stopped, surface restartable later.
  There is no path that reports `restarted` without a completed launch.
- The relaunch waits up to 5 s (bounded conflict retry) for a just-stopped
  predecessor on the same surface id to retire before declaring launch
  failure; an explicit restore RPC may therefore block briefly in that edge.
- The live-reconcile ownership attestation is per daemon incarnation and keyed
  to the LATEST committed checkpoint per session; restoring after fallback to
  a previous-known-good generation classifies conservatively (never live) by
  design — under automatic policy it relaunches instead.
- Restored VT grids re-derive at 24×80 (the relaunch geometry) and reflow on
  next resize; persisted grid geometry is a possible refinement.
- Restoring a generation older than the last committed notification export
  skips the import on a TYPED checkpoint mismatch (deterministic, never
  partial).
- CLI attach is read-only streaming; interactive raw-TTY attach is T5 by
  design. `daemon.health` counts live session runtimes only.

## 9. Handoffs

**To T5 (terminal-ui):** unchanged interfaces — `internal/rpcapi` types,
`internal/client`, attach contract (`attach_snapshot` header then raw frames),
cell snapshots via `terminal.Engine.CellSnapshot`, notification views. New
semantics: a restore may now genuinely relaunch automatic-policy surfaces —
`restarted` always means a running replacement process; a failed relaunch
surfaces as `stopped` with the launch-failure reason. The class/reason pair on
`RestoredSurface` remains the single source the TUI must render (spec
criterion 5 — no UI state may imply resurrection beyond it).

**To T6 (QA):** §7 verbatim, plus: the automatic-policy relaunch is exercised
by `TestTwentyFlows` (in-daemon and fresh-daemon segments) and
`internal/daemon/restore_test.go`; the launch-failure seam is `limitedPTY`
(deterministic spawn exhaustion), the spec-assertion seam is `recordPTY`, and
the liveness seam remains the blocking-Wait `holdPTY`; fault seams unchanged
(`snapshot.Writer{FS,Observe,Now,NewCheckpointID}`, `daemon.Deps.PTY`,
`daemon.RunOptions`, `platform.Clock`).

## 10. Envelope

```guild.handoff.v2
{
  "schema_version": "guild.handoff.v2",
  "task_id": "T4-backend",
  "tier": "powerful",
  "status": "done",
  "summary": "G-lane r2 rework: blocker F2 closed — `restarted` is now completed behavior. RestoreSnapshot launches the replacement PTY for every automatic-policy surface (restored argv/cwd/env, 24x80) into the restored runtime and reports restarted only after the spawn succeeds; launch failure fails closed to stopped with the exact reason and no false live owner. Live adoption, mismatch, manual-stopped, fresh-daemon exclusion and trust monotonicity preserved; no persisted/protocol change. Evidence: 3 new production-path restore tests + extended 20-flow E2E; 596 tests/33 pkgs incl -race; all gates green.",
  "artifacts": [
    "internal/daemon/snapshot.go",
    "internal/daemon/restore_test.go",
    "cmd/amux/e2e_test.go",
    ".guild/runs/run-8c2926bb-2ebe-4166-8b77-<HIGH_ENTROPY_REDACTED>-T4-backend.md"
  ],
  "issues": [
    "Linux-only runtime evidence (SO_PEERCRED, openat2/execveat, A6 containment, orphan scans, subprocess daemon E2E path, second-UID/kernel-race matrix, soak/perf gates) deferred to T6 with exact commands in receipt §7",
    "Repo has no git baseline; scope audit used session write-attestation + mtime windows; recommend a baseline commit before T5/T6",
    "govulncheck/gitleaks/go-licenses still absent on author host: deferred_prerequisite per T2 frozen commands, no clean scan claimed"
  ],
  "learnings": [
    "A class/reason restore contract is only closed when every class names completed behavior: classification-then-commit designs silently turn 'restarted' into intent unless the launch is part of the restore transaction.",
    "Installing a pending surface fail-closed (stopped, never live) and patching the result after the spawn keeps the reported classification true under every failure interleaving without widening the contract.",
    "A stopped predecessor retires asynchronously; relaunch-on-restore needs a bounded conflict-retry window or it fails spuriously exactly when the policy should fire."
  ],
  "notes": "Relaunch retries spawn conflicts up to 5s while a stopped predecessor retires; failure reason is 'automatic restart policy but relaunch failed: <err>'. No schema or wire change.",
  "injection_clean": "clean"
}
```
