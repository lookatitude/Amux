# T4-backend — B1–B12 backend lane receipt

- run: run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0
- task: T4-backend (backend specialist, tier powerful)
- status: done
- date: 2026-07-16
- host: macOS (darwin/arm64, go1.26.5) — portable evidence; Linux-only runtime evidence is explicitly deferred to T6 with reproducible commands (§6)

This receipt covers the full T4 lane: the preserved prior-session checkpoint
(verified and audited, not regenerated) plus this continuation session's
completion work. Two real defects were found in the checkpoint during the
audit and fixed with pinning tests (§3).

## 1. B1–B12 coverage

| Pkg | Package(s) | State | Key evidence |
|---|---|---|---|
| B1 binary/config/lifecycle | `cmd/amuxd`, `internal/config`, `internal/daemon/run.go`, `internal/version` | done | XDG fail-closed resolution, strict JSONC with located errors, boot identity, signal handling, single-instance socket (live socket never stolen: the production-assembly test in run_test.go boots the EXACT production assembly, proves second-daemon refusal + clean protocol shutdown). Daemon logs stderr-only. Config fuzz (`FuzzDecodeJSONC`) clean. |
| B2 control + session actors | `internal/control`, `internal/session`, `internal/domain` | done | Daemon-global trust/registry actor; per-session graph actors; bounded mailboxes; no blocking I/O on actor goroutines; immutable event payloads; `internal/domain/property_test.go` drives arbitrary valid command sequences; cross-session revoke covered by control tests + securitytest conformance (`timing.revoke-*`, `races.*`). |
| B3 protocol/transport/client | `api/v1`, `internal/protocol`, `internal/transport/local`, `internal/client`, `internal/rpcapi` | done | Bounded frames, strict durable decode, handshake/version-skew, deadlines, peer-UID gate (fake seam off Linux), symlink-free socket path validation, stale-socket reclaim-after-proof; golden vectors (`api/v1/golden_test.go`); shared reconnecting client with `ErrBootChanged`; `FuzzServerHeader` clean. Added this session: `input.release` method, lease-takeover params (confirmed-only). |
| B4 event replay/recovery | `internal/session`, `internal/ordering` | done | Commit-then-allocate sequences, bounded replay, filtered subscriptions, heartbeats, slow-consumer policy, typed `event_gap`; injected-drop and reconnect tests; E2E pins gap surfacing end to end (exit 7 + actionable message). |
| B5 PTY supervisor | `internal/pty`, `internal/platform` | done (portable) / Linux runtime → T6 | Spawn/resize/input/signal/exit/cancel/reap centralized; explicit cwd required (fail closed); A6 containment compiled for Linux (`containment_linux.go`), fail-closed fallback elsewhere, never claims unsupported containment. Kill-tree/orphan-scan runtime behavior is Linux-only evidence (§6). |
| B6 raw replay + VT engine | `internal/terminal` | done | Bounded ring (16 MiB floor per surface, budget rejected below minimum), sequence-safe restore, golden fixture→grid corpus (`testdata/terminal`), wide/combining cells, alternate screen, scroll regions, resize policy, damage sets; `FuzzEngineFeed` clean. |
| B7 attach + input leases | `internal/attach` | done | Linearized snapshot→replay(≤N)→live(>N) cutover, observer fanout, one lease per surface, acquire/takeover/release/disconnect, rejected writes, lag-disconnect receipts with `last_delivered`; checkpoint added drained-tiny-buffer + wedged-consumer regressions; stress: `-count=20` = 300 pass, `-race -count=5` = 75 pass. Detach never stops the process (pinned in E2E). |
| B8 persistence/restore | `internal/persist`, `internal/snapshot`, `internal/store`, `internal/daemon/{snapshot,truststore}.go` | done | Versioned manifest, component-first fsync, manifest-last commit, previous-known-good retention + diagnostic refusal, schema 0→1 forward migration, WAL SQLite (notifications/grants/audit/meta/cursors), monotonic epochs enforced at storage layer, trust excluded from layout restore, live/restarted/stopped classification (restore NEVER claims live — pinned over the wire and in E2E). **Fixed this session:** per-surface sidecar sections (§3.2) and notification export/import wiring (§4.3). |
| B9 CLI + machine contracts | `cmd/amux` (6 files) | done | All families: daemon, session, workspace, pane, surface, attach, input, replay, inspect, snapshot, restore, restart, stop, event, hook, notification, diagnostics; `--json` stable schemas (rpcapi types verbatim), documented exit-code table (0–9), cobra completions (bash/zsh/fish verified), `--timeout`, explicit ID targeting, confirmation matrix (destroy/stop/lease-takeover/hook-approve/trust-revoke) failing closed without TTY/`--yes` — pinned black-box (exit 2). **20-flow E2E: `cmd/amux/e2e_test.go` `TestTwentyFlows` passes** against a real daemon, real socket, real PTY processes via the real built binary. |
| B10 context/notify/adapters | `internal/context`, `internal/notify` | done | Bounded/debounced cwd/git/foreground/PID collectors off-actor (`poller`, `probe_linux` + portable probe); daemon-owned notification persistence/routing/read-state/latest-unread with replaceable Linux `notify-send` adapter (Nop elsewhere); 2 structured agent adapters (claude, codex) emitting only typed lifecycle/session/attention events; redaction fixtures + `FuzzAdapterFeed` clean; undeclared capabilities denied. |
| B11 hook runtime | `internal/hooks` | done (portable) / kernel races → T6 | Opt-in config load, argv launch, scratch cwd, env allowlist, timeout/output caps, audit, redaction; digest/inode descriptor binding with `openat2`+`execveat` compiled for Linux, fail-closed elsewhere; authorization consumed immediately pre-launch; same-project/two-session revoke barriers; 250 ms absent-trust and queued-cancellation gates pass via injected clock. **T2 conformance: T4-owned Factory in `internal/hooks/conformance_test.go` registers real enforcement; the security-conformance suite = 26/26 subchecks pass** (timing.*, races.*, restore.*, redaction.*) with the frozen securitytest package unmodified. |
| B12 diagnostics/perf | the observability package (internal) | done | `slog` correlation fields, queue/replay/PTY/attach metrics registry, bounded deterministic dump (single JSON object, wired to `diagnostics.dump` + CLI), local-owner-gated pprof controls, optional OTel wiring, benchmark entry points (`bench_test.go`) for QA/devops profiles. |

## 2. Changed files (this continuation session)

Checkpoint audit fixes + completion (all inside scope write-globs):

- `internal/rpcapi/rpcapi.go` — added `input.release`, lease `Takeover`/`Confirm` params
- `internal/daemon/engine.go` — removed dead `var _ = fmt.Sprintf`; added optional `Deps.Store`
- `internal/daemon/truststore.go` — **bug fix** §3.1
- `internal/daemon/snapshot.go` — **bug fix** §3.2 + notification export/import wiring
- `internal/daemon/surface.go` — spawn cwd fallback validated before graph commit
- `internal/daemon/wire.go` — takeover (confirmed-only) + `ReleaseInput`
- `internal/daemon/server.go` — registered `input.release`
- `internal/daemon/run.go` — store wired into engine deps
- `internal/snapshot/graph.go`, `internal/snapshot/reader.go` — per-surface sidecar sections (SidecarOffset + SidecarLength, `ReplayNextSeq`, `SurfaceReplayChunks`)
- New tests: `internal/daemon/{server,run,snapshot}_test.go`, `internal/snapshot/section_test.go`; `internal/daemon/engine_test.go` updated for explicit cwd
- New CLI: `cmd/amux/{main,graph,surface,streams,admin}.go`, E2E `cmd/amux/e2e_test.go` (replaces the T1 placeholder)

Prior-session checkpoint surfaces preserved (not regenerated): `internal/attach/{attachment,surface,attach_test}.go`, `internal/terminal/{ring,ring_test}.go`, `internal/daemon/*`, `cmd/amuxd/main.go`, and the rest of the T4 corpus.

## 3. Audit findings on the checkpoint (fixed, with pinning tests)

1. **`sqliteTrustStore.SaveProject` broke project registration against the real store.** It unconditionally called `SetProjectState`, which correctly refuses the registration state (`""`, epoch 0) — so `RegisterProject`, and with it the ENTIRE wire hook family, failed against SQLite (it only worked in control tests using the in-memory store). Fixed: registration upserts identity only; state/epoch move on real transitions. Pinned by the server hook-family test (server_test.go) and the production-assembly test (run_test.go — full trust round trip over the production assembly).
2. **Multi-surface snapshot replay was ambiguous.** Save interleaved all surfaces' chunks into one flat sidecar; surfaces' sequence spaces overlap (each starts at 1), so restore could not partition them and only handled the single-surface case (confessed in checkpoint comments, dead `bySurface` var). Fixed: each surface's retained output is encoded as its own complete sidecar stream, concatenated, with per-surface offset/length + `ReplayNextSeq` in the graph doc; restore decodes each surface's own section fail-closed (out-of-bounds/misaligned sections are typed corruption) and resumes sequence allocation without reuse. Pinned by the multi-surface partition test in snapshot_test.go (cross-surface bleed check) and `internal/snapshot/section_test.go`.
3. `SpawnSurface` with empty cwd committed a graph surface then failed opaquely in the PTY layer. Now: pane-cwd fallback, validated BEFORE the graph mutation commits (no orphan surface), typed `invalid_argument`.
4. Cosmetic: leftover compile crutch removed.

## 4. Exact verification evidence (this session, post-change)

All commands from repo root, go1.26.5 darwin/arm64:

1. `gofmt -l cmd internal api` → empty
2. `go vet ./...` → clean
3. `go test -count=1 ./...` → **589 passed / 33 packages** (577 at checkpoint + 12 new)
4. `go test -race -count=1 ./...` → **589 passed / 33 packages**
5. Attach stress: `go test -count=20 ./internal/attach` → 300 pass; `go test -race -count=5 ./internal/attach` → 75 pass
6. Security conformance: `go test -v -run Conformance ./internal/hooks` → the security-conformance suite: 26/26 named subchecks pass (timing.absent-trust, timing.revoke-cancel, timing.revoke-first, timing.launch-first, races.{symlink,rename,exec-byte,config-byte,project-root}-*, restore.{epoch-monotonic,grant-stays-inactive,audit-retained,no-launch-authority}, redaction.{config,environment,hook-input,hook-output,error,log,audit,snapshot,agent-adapter,notification,diagnostics,truncation})
7. Bounded fuzz smoke, 15 s each, all PASS: `FuzzDecodeSidecar` (snapshot), `FuzzAdapterFeed` (context), `FuzzDecodeJSONC` (config), `FuzzEngineFeed` (terminal), `FuzzServerHeader` (protocol), `FuzzRedact` (redact)
8. Cross-compile: `GOOS=linux GOARCH=amd64 go build ./...` OK; `GOOS=linux GOARCH=arm64 go build ./...` OK; `CGO_ENABLED=0 go build ./...` OK (**no cgo maintained**)
9. Modules: `go mod verify` → all verified; `go mod tidy -diff` → **empty** (T2's stale go.sum finding is resolved)
10. 20-flow black-box E2E: `go test -count=1 ./cmd/amux` → `TestTwentyFlows` PASS (builds the real binary, boots a real daemon on a real owner-only socket, spawns real `/bin/sh` PTYs; asserts human AND `--json` contracts, exit codes 2/3/4/7/9, confirmation fail-closed, typed event_gap, lease conflict/takeover)
11. Binary smoke: `amux version`, `amuxd --version`, `amux completion bash|zsh` all OK

## 5. Scope compliance

- All session writes fall inside `scope/T4-backend.json` write-globs (`internal/**`, `cmd/amux/**`, `cmd/amuxd/**`, `api/v1/**`, `go.mod`, `go.sum`, this receipt).
- Forbidden-surface check: `find <all forbidden globs> -newermt "2026-07-16 00:00"` → **0 files** modified in the T4 continuation window. Files under `docs/adr`, `docs/security`, and the securitytest package carry July-15 mtimes from their OWN authoring lanes (T1/T2), not from T4. That frozen package was consumed, never modified — its own coverage-floor tests and the full conformance suite pass unchanged.
- Caveat stated honestly: the repository has no git baseline (everything is untracked), so a mechanical VCS diff audit is impossible. Evidence is session write-attestation + mtime windows + green frozen-corpus self-checks. **Recommendation to orchestrator: establish a git baseline commit before T5/T6 dispatch.**
- No frozen ADR, protocol or persistence compatibility, trust semantic, cgo policy, supported-OS, or acceptance-threshold change was made; no ask-gate condition arose. The one durable-format extension (per-surface sidecar sections) is additive inside T4's own graph schema v1, authored this lane, pre-release, with fail-closed decoding — not a frozen-contract change.

## 6. Honest deferrals — Linux-only runtime evidence (T6 prerequisites)

Portable/macOS evidence above is real but NOT a claim of Linux runtime behavior. T6 must run, on the Linux CI lane (Arch + Ubuntu 24.04, amd64 + arm64):

1. `go test -count=1 ./...` and `go test -race ./...` — exercises `SO_PEERCRED` enforcement (`peercred_linux.go`), `openat2`+`execveat` descriptor-bound hook launch, A6 containment (`containment_linux.go`, `launch_linux.go`), Linux foreground-process probe, and `notify-send` adapter compilation paths that are fake-seamed or fail-closed off Linux.
2. `go test -count=1 ./cmd/amux` — on Linux the E2E starts the daemon as a REAL subprocess via `amux daemon start` with production peer credentials (flow 1/2 subprocess path; off Linux it ran in-process with an injected owner-UID seam — documented in the test header).
3. Second-UID rejection, resource-exhaustion, and the deterministic kernel race matrix (T2 readiness manifest "integrated checks") — real two-user Linux host.
4. Forced daemon `SIGKILL` / double-fork / process-group-escape orphan scans (`internal/pty` Linux-gated tests) — zero-orphan criterion is a Linux runtime claim only.
5. Performance and reliability gates: 30-min soak (20 PTYs), 8-pane restore < 2 s, p95 < 75 ms, 8-h nightly — reference profile (T3 docs).
6. Scanner debts inherited from T2 (tools absent on this host too, commands frozen in `docs/security/security-readiness.md`): `govulncheck ./...`, `gitleaks detect`, `go-licenses check ./...` — deferred_prerequisite, NOT claimed clean.

## 7. Risks and residuals

- **In-daemon restore is stop-and-reclassify.** `RestoreSnapshot` always classifies with `FreshDaemon: true`: it stops the old runtime and never claims `live`, so the no-resurrection invariant holds conservatively; but a finer "in-daemon live reconcile" (keeping genuinely live surfaces live across an in-place restore) is not implemented. Flagged for orchestrator/T6 review as an acceptance-interpretation item, not silently absorbed.
- Restored VT grids re-derive at 24×80 and reflow on next resize; persisted grid geometry is a possible refinement.
- Restoring a generation older than the last committed notification export skips the import on a TYPED checkpoint mismatch (notifications stay SQLite-current, deterministic; graph restore unaffected).
- CLI attach is read-only streaming (input goes through `input send` leases); interactive raw-TTY attach is a T5 TUI concern by design.
- `daemon.health` reports live session runtimes; sessions existing only as snapshots are not counted until restored.

## 8. Handoffs

**To T5 (terminal-ui):** versioned immutable client models = `internal/rpcapi` types (strict-decoded both directions); shared client = `internal/client` (`Dial`/`Call`/`Stream`, `ErrBootChanged`, typed `*client.Error` with frozen codes); attach contract = `attach_snapshot` header event (`up_to_seq`, rows/cols, optional `replay_gap`) then `raw_output` body frames; cell snapshots via `attach.Surface` snapshot seam (`terminal.Engine.CellSnapshot`); notification views via `notification.list`; fake-client fixture pattern = `internal/daemon/server_test.go` `startDaemon` (full in-process daemon harness with injectable PTY/peer/clock seams). No TUI-owned durable mutation exists to inherit — every path is one RPC method.

**To T6 (QA):** deterministic binaries (`go build ./cmd/amux ./cmd/amuxd`, version-stampable via `internal/version` ldflags); black-box 20-flow suite (`go test ./cmd/amux`); fixture controls and fault-injection seams: `snapshot.Writer{FS, Observe, Now, NewCheckpointID}`, `daemon.Deps.PTY`, `daemon.RunOptions{Getenv, Peers, Ready, Logger}`, `platform.Clock`, securitytest harness clocks/barriers; metrics = `observability.Registry` + `diagnostics.dump`; benchmarks = `internal/observability/bench_test.go`. Linux prerequisites: §6 verbatim.

## 9. Envelope

```guild.handoff.v2
{
  "schema_version": "guild.handoff.v2",
  "task_id": "T4-backend",
  "tier": "powerful",
  "status": "done",
  "summary": "B1-B12 complete. Audited the interrupted checkpoint, fixed 2 real defects (SQLite truststore broke project registration; multi-surface snapshot replay unpartitionable). Added daemon/server/run tests, per-surface sidecar sections, notify export/import, lease takeover/release, full B9 CLI (--json, exit codes, completions, fail-closed confirmations), 20-flow black-box E2E on a real daemon+PTYs. Gates: gofmt/vet clean; 589 tests/33 pkgs incl -race; attach stress 300+75; conformance 26/26; 6 fuzz clean; linux amd64/arm64 no-cgo builds; mod verify/tidy clean; scope clean. Linux runtime to T6.",
  "artifacts": [
    "cmd/amux/main.go",
    "cmd/amux/graph.go",
    "cmd/amux/surface.go",
    "cmd/amux/streams.go",
    "cmd/amux/admin.go",
    "cmd/amux/e2e_test.go",
    "cmd/amuxd/main.go",
    "internal/rpcapi/rpcapi.go",
    "internal/daemon/server.go",
    "internal/daemon/run.go",
    "internal/daemon/snapshot.go",
    "internal/daemon/truststore.go",
    "internal/daemon/wire.go",
    "internal/daemon/surface.go",
    "internal/daemon/server_test.go",
    "internal/daemon/run_test.go",
    "internal/daemon/snapshot_test.go",
    "internal/snapshot/graph.go",
    "internal/snapshot/reader.go",
    "internal/snapshot/section_test.go",
    "internal/hooks/conformance_test.go",
    ".guild/runs/run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0/handoffs/backend-T4-backend.md"
  ],
  "issues": [
    "Linux-only runtime evidence (SO_PEERCRED, openat2/execveat, A6 containment, orphan scans, subprocess daemon E2E path, second-UID/kernel-race matrix, soak/perf gates) deferred to T6 with exact commands in receipt §6",
    "In-daemon restore is stop-and-reclassify (never claims live; conservative); finer in-daemon live reconcile flagged for orchestrator/T6 acceptance review, not silently absorbed",
    "Repo has no git baseline; scope audit used session write-attestation + mtime windows + green frozen-corpus self-checks; recommend a baseline commit before T5/T6",
    "govulncheck/gitleaks/go-licenses still absent on author host: deferred_prerequisite per T2 frozen commands, no clean scan claimed"
  ],
  "learnings": [
    "An integration test over the real assembly finds what unit seams hide: the trust store bug only existed against real SQLite because control tests injected a MemStore that accepted what the store correctly refuses.",
    "When per-item sequence spaces overlap, a shared flat journal is unrecoverable by construction; partition at write time (own section per item + offsets) so restore never has to guess.",
    "Validate spawn preconditions before committing the graph mutation, or every refused launch leaves durable orphan state behind."
  ],
  "notes": "Continuation of interrupted checkpoint: preserved and audited, not regenerated. E2E runs in-process off Linux (peer seam injected, documented); subprocess path is Linux CI. tidy -diff now clean.",
  "injection_clean": "clean"
}
```
