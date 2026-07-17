# T4-backend — B1–B12 backend lane receipt (G-lane round-1 rework)

- run: run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0
- task: T4-backend (backend specialist, tier powerful)
- status: done
- date: 2026-07-16
- host: macOS (darwin/arm64, go1.26.5) — portable evidence; Linux-only runtime evidence remains explicitly deferred to T6 with reproducible commands (§7)
- supersedes: the round-0 receipt reviewed as `review/G-lane:T4-backend/artifact-round1.md` (sha256 1300365…25ce)

This receipt covers the full T4 lane after the mandatory G-lane round-1 rework:
the preserved, verified B1–B12 checkpoint plus the closure of the one blocking
review finding. Nothing outside that finding's blast radius was regenerated.

## 1. Review response — G-lane round 1

**F1 (blocking): "B8 is still missing the required in-daemon live-reconcile
restore path" — CLOSED with production code + production-path/E2E evidence.**

The finding was correct: the only production restore entry point
(`internal/daemon/snapshot.go RestoreSnapshot`) hard-coded
`persist.RestoreContext{FreshDaemon: true}`, structurally forbidding
`ClassLive`; the round-0 receipt marked B8 done while deferring the live
path. That deferral is removed — the behavior now exists:

- **Ownership attestation at save.** Every PTY spawn/restart mints a fresh
  `spawnID` (`internal/daemon/surface.go`). `SaveSnapshot` records, keyed by
  the committed manifest `CheckpointID`, which surfaces were live with which
  `spawnID` at capture — in daemon memory only, never persisted.
- **Two restore modes in the production path.** `RestoreSnapshot` looks up the
  session's current runtime: if this daemon still hosts it, the restore is an
  in-daemon reconcile (`FreshDaemon: false`); otherwise it is a fresh restore
  (`FreshDaemon: true`, live structurally excluded by the frozen classifier).
- **Trustworthy `<HIGH_ENTROPY_REDACTED>` evidence.** Supplied per surface only
  when (a) the loaded generation IS the exact checkpoint this incarnation
  saved, (b) the supervisor still reports the surface alive, and (c) the
  runtime's current live `spawnID` equals the one recorded at capture. Older /
  previous-known-good generations, another incarnation's checkpoints, exited
  processes, and stop→restart impostors (new `spawnID`) all fail this gate.
- **Adoption without disturbance.** A surface classifying live is adopted
  untouched: same process, same replay ring (a superset of the checkpoint's),
  same attach surface — it is never stopped or relaunched. Adoption is
  re-verified under the runtime lock at commit; a process that died between
  classification and commit demotes fail-closed via `persist.Classify` with
  ownership evidence cleared.
- **Fail-closed truthfulness.** Any live process the checkpoint does not vouch
  for (identity mismatch, or absent from the restored graph) is stopped so the
  reported stopped/restarted classification is true. Rebuild is
  prepare-then-commit: a corrupt sidecar aborts the whole restore with the
  prior runtime untouched.
- **No contract or compatibility change.** ADR-0005 precedence is consumed
  verbatim (`persist.Classify` untouched); graph schema, wire protocol, CLI
  contracts, and SQLite schemas are unchanged. Trust epochs/grants/audit stay
  structurally out of restore's reach; the notification export remains the
  only import, unchanged.

The graph actor swap that in-daemon reconcile requires is race-safe: the
`sessionRuntime.actor` pointer is now guarded by the runtime mutex and read
through `graphActor()` at every call site — validated by the full `-race` run.

## 2. B1–B12 coverage

Unchanged from the verified checkpoint except **B8/B9**, restated in full:

| Pkg | Package(s) | State | Key evidence |
|---|---|---|---|
| B1 binary/config/lifecycle | `cmd/amuxd`, `internal/config`, `internal/daemon/run.go`, `internal/version` | done | XDG fail-closed resolution, strict JSONC with located errors, boot identity, signal handling, single-instance socket; production-assembly test proves second-daemon refusal + clean protocol shutdown; logs off protocol streams; `FuzzDecodeJSONC` clean. |
| B2 control + session actors | `internal/control`, `internal/session`, `internal/domain` | done | Global trust/registry actor; per-session graph actors; bounded mailboxes; no blocking I/O on actor goroutines; property tests over arbitrary valid command sequences; cross-session revoke via control tests + conformance. |
| B3 protocol/transport/client | `api/v1`, `internal/protocol`, `internal/transport/local`, `internal/client`, `internal/rpcapi` | done | Bounded frames, strict durable decode, handshake/version-skew, peer-UID gate, symlink-free socket validation, stale-socket reclaim-after-proof, golden vectors, reconnecting client, `FuzzServerHeader` clean. |
| B4 event replay/recovery | `internal/session`, `internal/ordering` | done | Commit-then-allocate sequences, bounded replay, filtered subscriptions, heartbeats, slow-consumer policy, typed `event_gap` (E2E exit 7). |
| B5 PTY supervisor | `internal/pty`, `internal/platform` | done (portable) / Linux runtime → T6 | Spawn/resize/input/signal/exit/cancel/reap centralized; explicit cwd fail-closed; A6 containment compiled for Linux, fail-closed elsewhere. |
| B6 raw replay + VT engine | `internal/terminal` | done | Bounded ring (16 MiB floor), sequence-safe restore, golden fixture→grid corpus, `FuzzEngineFeed` clean. |
| B7 attach + input leases | `internal/attach` | done | Linearized snapshot→replay(≤N)→live(>N) cutover, one lease per surface, takeover/release/disconnect receipts; stress `-count=20` = 300 pass, `-race -count=5` = 75 pass; detach never stops the process. |
| B8 persistence/restore | `internal/persist`, `internal/snapshot`, `internal/store`, `internal/daemon/{snapshot,truststore}.go` | **done — rework closed the last gap** | Versioned manifest, component-first fsync, manifest-last commit, previous-known-good retention + diagnostic refusal, schema 0→1 migration, WAL SQLite, monotonic epochs, trust excluded from layout restore. Classification now complete in the production path: **clean/fresh-daemon restore never live** (structural), **in-daemon live reconcile** (same still-owned spawn identity → live, untouched), **stopped/restarted reasons and restart-policy validation exact**, stale security generation/grants excluded and fail-closed (conformance `restore.*` 4/4). §1 has the design; §4 the tests. |
| B9 CLI + machine contracts | `cmd/amux` (6 files) | done | All command families; `--json` stable schemas; exit-code table; completions; fail-closed confirmation matrix. **20-flow E2E reworked**: flow 17 now proves the in-daemon live reconcile (surface stays live, still answers input, exactly one spawn marker) AND the fresh-daemon never-live restore against a rebooted real daemon over the same durable state; flows 18/19 reordered (stop → restart) because a live-reconciled surface correctly refuses restart. |
| B10 context/notify/adapters | `internal/context`, `internal/notify` | done | Bounded/debounced collectors off-actor; daemon-owned notification persistence/routing/read-state; 2 structured adapters (claude, codex) with redaction fixtures; `FuzzAdapterFeed` clean. |
| B11 hook runtime | `internal/hooks` | done (portable) / kernel races → T6 | Opt-in config, descriptor-bound launch, env allowlist, timeout/output caps, audit, redaction; conformance 26/26 subchecks with the frozen securitytest package unmodified. |
| B12 diagnostics/perf | internal observability | done | `slog` correlation, metrics registry, bounded deterministic dump, owner-gated pprof, OTel wiring, benchmark entry points. |

## 3. Changed files (this rework session — everything else preserved)

- `internal/daemon/engine.go` — `savedIdentity` record + `Engine.saved` map; `surfaceRuntime.spawnID`; `sessionRuntime.graphActor()` (actor pointer now mutex-guarded for the restore swap); cleanup on destroy
- `internal/daemon/surface.go` — spawn/restart mint a fresh `spawnID` (a restart is a NEW process; no earlier attestation can match it)
- `internal/daemon/snapshot.go` — `SaveSnapshot` records the per-checkpoint live-identity attestation; `RestoreSnapshot` rewritten: in-daemon reconcile vs fresh restore, ownership-evidence probe, prepare-then-commit rebuild, adoption re-verified under lock, fail-closed stop of unvouched processes
- `internal/daemon/wire.go` — event subscribe reads the actor through `graphActor()`
- `internal/daemon/restore_test.go` — **new**: blocking-Wait fake PTY + 4 production-path tests (§4)
- `internal/daemon/server_test.go` — stay-open fake now genuinely alive (blocking `Wait`); wire restore pins `live` for the still-owned surface
- `internal/daemon/engine_test.go`, `internal/daemon/snapshot_test.go` — comments corrected: those in-engine restores classify stopped because their fake processes exit instantly (no ownership), not because restore is fresh
- `cmd/amux/e2e_test.go` — daemon boot extracted to `(h *e2eEnv).boot()` for a second incarnation; flow 17 in-daemon live assertions; flows 18/19 reordered with polling; fresh-daemon never-live segment (§2 B9)

## 4. New acceptance evidence (production path + E2E)

Unit/classifier tests were never the gap; these drive `RestoreSnapshot` itself:

1. `<HIGH_ENTROPY_REDACTED>` — save→restore on the same
   engine with a genuinely alive PTY: classifies `live` with the exact ADR
   reason, restart refuses (conflict — the process was never stopped), replay
   holds exactly ONE spawn marker (never relaunched), and a second
   save→restore cycle re-reconciles live (adoption keeps the identity).
2. `<HIGH_ENTROPY_REDACTED>` — surface stopped after the
   save: in-daemon restore yields `stopped` with the exact manual-policy
   reason.
3. `<HIGH_ENTROPY_REDACTED>` — stop→restart between save
   and restore (same surface id, NEW process): never `live`; the impostor is
   stopped fail-closed and the surface becomes restartable.
4. `<HIGH_ENTROPY_REDACTED>` — a second
   engine over the same snapshot root restores `stopped` while the saving
   engine's process is literally still running, and rebuilds replay from the
   committed sidecar.
5. `<HIGH_ENTROPY_REDACTED>` (wire) — in-daemon restore over the real socket
   classifies the still-owned surface `live`, reason always present.
6. `TestTwentyFlows` (black-box E2E, real binary/daemon/PTYs) — flow 17:
   in-daemon restore keeps the live `cat` shell `live` with the exact reason,
   it still answers input under the held lease, exactly one spawn marker;
   flows 19/18: confirmed stop then restart → `restarted` + second marker;
   after flow 2 a NEW daemon boots over the same durable state and the same
   checkpoint restores `stopped`/never-live with replay history intact.
7. Existing pins preserved: `<HIGH_ENTROPY_REDACTED>`,
   `<HIGH_ENTROPY_REDACTED>` (per-surface sidecar
   partitioning), `TestClassifyRules`/`<HIGH_ENTROPY_REDACTED>`
   (frozen classifier), securitytest `restore.*` (epoch-monotonic,
   grant-stays-inactive, audit-retained, no-launch-authority), notification
   export/import round-trip over the wire.

## 5. Exact verification evidence (this session, post-change)

All commands from repo root, go1.26.5 darwin/arm64:

1. `gofmt -l cmd internal api` → empty
2. `go vet ./...` → clean
3. Focused restore tests: `go test ./internal/daemon/ -count=1` → 17 pass (4 new)
4. `go test -count=1 ./...` → **593 passed / 33 packages** (589 at round 0 + 4 new)
5. `go test -race -count=1 ./...` → **593 passed / 33 packages** (validates the actor-swap locking)
6. Attach stress: `go test -count=20 ./internal/attach` → 300 pass; `go test -race -count=5 ./internal/attach` → 75 pass
7. Security conformance: `go test -count=1 -run Conformance ./internal/hooks` → 26 pass (timing.*, races.*, restore.*, redaction.*), frozen securitytest package unmodified
8. 20-flow E2E: `go test ./cmd/amux/ -count=1 -run TestTwentyFlows` → PASS (real binary, real daemon boots TWICE, real `/bin/sh` PTYs)
9. Cross-compile: `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./...` OK; `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...` OK (no cgo maintained)
10. Modules: `go mod verify` → all verified; `go mod tidy -diff` → empty

## 6. Scope compliance

- Session writes: exactly the 9 files in §3 plus this receipt — all inside
  `scope/T4-backend.json` write-globs (`internal/**`, `cmd/amux/**`, receipt
  path). `go.mod`/`go.sum` untouched (tidy clean).
- Forbidden-surface check: `find docs/adr docs/security <HIGH_ENTROPY_REDACTED>
  .github/workflows packaging docs/operations docs/release internal/tui
  testdata/tui research -newermt "2026-07-16 06:00" -type f` → **0 files**.
- No ADR was changed, no persisted or protocol compatibility was altered
  (graph schema still v1, no new persisted fields — the ownership attestation
  is daemon-memory only), no trust semantic touched, no cgo, no fake live
  classification. The frozen contract was implementable without amendment, so
  the ask-gate was not triggered.
- Standing caveat from round 0: the repository still has no git baseline, so
  the audit is session write-attestation + mtime windows; recommend a baseline
  commit before T5/T6.

## 7. Honest deferrals — Linux-only runtime evidence (T6 prerequisites)

Unchanged from round 0 (portable/macOS evidence is real but not a Linux
runtime claim): full suite + race on Arch/Ubuntu CI (SO_PEERCRED,
openat2/execveat, A6 containment, Linux probes), subprocess-daemon E2E path,
second-UID/resource-exhaustion/kernel-race matrix on a two-user host, forced
SIGKILL orphan scans, soak/perf gates, and the T2 scanner debts
(`govulncheck`, `gitleaks`, `go-licenses`) — commands frozen in
`docs/security/security-readiness.md`. The in-daemon live-reconcile E2E runs
on Linux CI via the same `TestTwentyFlows`.

## 8. Risks and residuals

- The live-reconcile ownership attestation is per daemon incarnation and keyed
  to the LATEST committed checkpoint per session; restoring after fallback to
  a previous-known-good generation classifies conservatively (never live) by
  design.
- `RestoreSnapshot` classifies automatic-policy surfaces `restarted` per the
  frozen classifier but does not itself relaunch processes — relaunch remains
  the explicit restart flow (flow 18), as in the reviewed checkpoint.
- Restored VT grids re-derive at 24×80 and reflow on next resize; persisted
  grid geometry is a possible refinement.
- Restoring a generation older than the last committed notification export
  skips the import on a TYPED checkpoint mismatch (deterministic, never
  partial).
- CLI attach is read-only streaming; interactive raw-TTY attach is T5 by
  design. `daemon.health` counts live session runtimes only.

## 9. Handoffs

**To T5 (terminal-ui):** unchanged from round 0 — `internal/rpcapi` types,
`internal/client`, attach contract (`attach_snapshot` header then raw frames),
cell snapshots via `terminal.Engine.CellSnapshot`, notification views. New:
restored surfaces may now genuinely be `live` after an in-daemon restore; the
class/reason pair on `RestoredSurface` is the single source the TUI must
render (spec criterion 5 — no UI state may imply resurrection beyond it).

**To T6 (QA):** §7 verbatim, plus: the in-daemon live path is exercised by
`TestTwentyFlows` flow 17 and `internal/daemon/restore_test.go`; the
blocking-Wait fake (`holdPTY`) is the seam for deterministic liveness; fault
seams unchanged (`snapshot.Writer{FS,Observe,Now,NewCheckpointID}`,
`daemon.Deps.PTY`, `daemon.RunOptions`, `platform.Clock`).

## 10. Envelope

```guild.handoff.v2
{
  "schema_version": "guild.handoff.v2",
  "task_id": "T4-backend",
  "tier": "powerful",
  "status": "done",
  "summary": "G-lane r1 rework: blocker F1 closed — B8 in-daemon live reconcile is now real production behavior. Save records each checkpoint's live spawn identities in daemon memory; RestoreSnapshot distinguishes in-daemon reconcile from fresh restore and classifies live ONLY when this incarnation still owns the identical spawn identity, adopting that surface untouched; every mismatch fails closed and unvouched live processes are stopped. No persisted/protocol change. Evidence: 4 new restore tests, wire live pin, reworked 20-flow E2E (live + fresh never-live); 593 tests/33 pkgs incl -race; all gates green.",
  "artifacts": [
    "internal/daemon/engine.go",
    "internal/daemon/surface.go",
    "internal/daemon/snapshot.go",
    "internal/daemon/wire.go",
    "internal/daemon/restore_test.go",
    "internal/daemon/server_test.go",
    "internal/daemon/engine_test.go",
    "internal/daemon/snapshot_test.go",
    "cmd/amux/e2e_test.go",
    ".guild/runs/run-8c2926bb-2ebe-4166-8b77-<HIGH_ENTROPY_REDACTED>-T4-backend.md"
  ],
  "issues": [
    "Linux-only runtime evidence (SO_PEERCRED, openat2/execveat, A6 containment, orphan scans, subprocess daemon E2E path, second-UID/kernel-race matrix, soak/perf gates) deferred to T6 with exact commands in receipt §7",
    "Repo has no git baseline; scope audit used session write-attestation + mtime windows; recommend a baseline commit before T5/T6",
    "govulncheck/gitleaks/go-licenses still absent on author host: deferred_prerequisite per T2 frozen commands, no clean scan claimed"
  ],
  "learnings": [
    "Unit-level classifier support is not the acceptance behavior: the contract only exists once the production entry point can produce the evidence the classifier consumes.",
    "A checkpoint-keyed in-memory ownership attestation proves same-PTY-identity without any persisted-format change, and excludes a fresh daemon structurally rather than by policy.",
    "Test fakes whose Wait() returns immediately silently turn in-daemon restores into no-ownership cases; liveness paths need a blocking-Wait fake to be exercised at all."
  ],
  "notes": "Live evidence is daemon-memory only (checkpoint-keyed spawn IDs): no snapshot schema change; a fresh daemon is structurally excluded. E2E now boots a second daemon for the clean-restore proof.",
  "injection_clean": "clean"
}
```
