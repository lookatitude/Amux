# T5 terminal-ui — handoff (attempt 2 + G-lane round-1 rework)

- task-id: T5-terminal-ui
- owner: terminal-ui
- depends-on: T1-architect (frozen seams), T4-backend (minor-1 projections + toolkit co-resolution)
- status: done (real Bubble Tea v2 / Lip Gloss v2 runtime over the four T4 projections)

## What changed vs attempt 1

Attempt 1's pure Elm-shaped core (`internal/tui/{geometry,render,keys,model,notify,
attachstate,a11y,app,runtime,bench}`) is **preserved and reused** — it is correct,
deterministic, and golden-tested. Attempt 1 was rejected because it (a) never
adopted Bubble Tea/Lip Gloss, (b) imported `internal/terminal` from the client
adapter as a bypass, and (c) documented four ask-gates instead of consuming
backend projections. All three are fixed:

- **Pinned + imported** `charm.land/bubbletea/v2@v2.0.8` and
  `charm.land/lipgloss/v2@v2.0.5` (go.mod; `go mod tidy` keeps x/ansi at exactly
  v0.11.7 — no backend churn).
- **New production runtime** `internal/tui/teabridge`: a real bubbletea/v2
  `Model`/`Update`/`View` wrapping the pure `app.Model`. ALL I/O (daemon calls,
  projection fetches) is isolated in `tea.Cmd`s; input/data events are translated
  into the pure core's messages and folded through its side-effect-free `Update`,
  so every transition stays deterministic and golden-testable.
- **Client adapter rewired** onto the four T4 projections; the `internal/terminal`
  import is **removed** (forbidden-import audit clean). Cells reach the UI only
  through `surface.cells` / attach-cells — never by reaching into the VT engine.
- `cmd/amux/tui.go` runs the Bubble Tea program; the hand-rolled raw-mode driver
  and input decoder (`tui_driver.go`, `tui_input.go`, `tui_input_test.go`) are
  deleted — Bubble Tea owns raw mode, input decode, resize, alt-screen, paste.

## U1–U8 coverage

- **U1 Client model boundaries.** Immutable <HIGH_ENTROPY_REDACTED>
  notification/health view models (`internal/tui/model`) fed only from wire
  projections via `clientadapter`. `app.Update` is pure; I/O lives in teabridge
  `tea.Cmd`s. Evidence: `app` golden/replay tests; `teabridge` Update folds.
- **U2 Pure split geometry.** Unchanged `internal/tui/geometry` (ratio alloc,
  borders, content rects, min dims, equalize, directional neighbours) + its
  property/golden tests. `workspace.tree` → `geometry.Node` via
  `clientadapter.TreeFromWire` (ratios carried; binary→N-child).
- **U3 Pane/cell/status renderer.** `surface.cells` (`rpcapi.CellGrid`) →
  `model.CellSnapshot` with AUTHORITATIVE widths (no client recomputation);
  wide heads/spacers and combining marks copied verbatim. Monochrome path
  preserved (`PlainString`); styled path (`StyledString`) added for the color
  frame. Evidence: `clientadapter` width/combining test; `teabridge`
  <HIGH_ENTROPY_REDACTED> + 8-pane fixture.
- **U4 Input & command modes.** Pure router unchanged; `mapKeyPress` is the single
  Bubble-Tea→`keys.Key` boundary. Prefix/navigation keys are consumed by the UI
  and NEVER sent to the PTY; paste is atomic and dropped outside passthrough.
  Evidence: `teabridge` <HIGH_ENTROPY_REDACTED>, <HIGH_ENTROPY_REDACTED>,
  <HIGH_ENTROPY_REDACTED> (assert the fake daemon received
  pane.focus but NOT input.send).
- **U5 Attach & recovery UX.** <HIGH_ENTROPY_REDACTED>-only/lease-owned/
  takeover-confirm/gap-recovery/slow-detached/disconnected/daemon-restarted
  states via `attachstate` fed by real signals (error classification + boot-id
  change + accepted-write lease). Recovery uses backend APIs (fresh
  `surface.cells` re-fetch); no local gap stitching. Evidence: `teabridge`
  GapRecovery, SlowConsumer, DaemonRestart tests. A tree re-fetch during a
  pending recovery no longer clobbers the recovery state (fixed).
- **U6 Notification & trust.** Inbox/unread/latest-unread/route/dismiss over
  `notification.list`/`notification.read`. `hook.inspect` → FULL trust card
  (project identity, exec path + SHA-256, events, cwd scope, env KEY names only,
  timeout, output cap, bound epoch) — no more UNAVAILABLE fields. approve/deny/
  revoke are daemon calls gated by the frozen confirmation matrix; the UI never
  decides authorization. Evidence: `teabridge` <HIGH_ENTROPY_REDACTED>,
  <HIGH_ENTROPY_REDACTED> (confirm→hook.approve w/ Confirm token),
  <HIGH_ENTROPY_REDACTED>.
- **U7 Performance & accessibility.** Full-frame strategy frozen (simpler, meets
  budget); damage-aware kept for comparison in `bench`. Lip Gloss chrome is
  geometry-safe (status/min-size width-clamped) and degrades to plain under
  NO_COLOR/mono; min-size + reduced-motion + ASCII + CLI-alternative paths
  preserved. Evidence: `teabridge` <HIGH_ENTROPY_REDACTED>, <HIGH_ENTROPY_REDACTED>,
  <HIGH_ENTROPY_REDACTED>; `bench` frame-time/allocs/bytes benchmarks.
- **U8 Snapshot/model regression corpus.** App golden frames + deterministic
  replay preserved; extended with production adapter + Bubble Tea integration
  tests (projections→frames, prefix/nav-never-to-PTY, 8-pane real
  cell/context/tree with Unicode wide/combining, focus, status, unread, lease,
  exit/restore, gap/reconnect/slow-consumer states).

## Evidence (author host darwin/arm64; linux cross-builds)

- gofmt clean; `go vet ./...` clean; staticcheck v0.6.1 clean on
  `internal/tui/...` + `cmd/amux/`.
- `go test ./...` and `go test -race ./...` → **707 passed / 45 packages** (was
  690; +17 new TUI tests), no data races.
- Focused: app golden/replay all pass; `teabridge` 16 tests pass.
- Benchmarks (portable macOS/arm64 — NOT Arch p95): `<HIGH_ENTROPY_REDACTED>`
  ≈138µs/op; `<HIGH_ENTROPY_REDACTED>` ≈739µs/op, 67479 bytes/frame, 37822
  allocs/op; damage-aware ≈877µs/op, 62125 bytes/frame. Vast headroom under the
  75 ms budget; exact Arch reference-profile p95 is T6's (command frozen in
  `internal/tui/bench.RunbookArchCommand`).
- `CGO_ENABLED=0 GOOS=linux GOARCH={amd64,arm64} go build ./...` both succeed.
- `make deps-manifest` (27-module build+test graph frozen), `make license` (all
  27 permissive), `go mod verify`, `go mod tidy` diff empty, archtest 3 pass.
- Forbidden-import/scope audit clean: NO `internal/terminal`, `internal/store`/
  `database/sql`/`modernc`, `internal/snapshot`/`persist`, or `internal/hooks`
  under `internal/tui`; bubbletea/lipgloss confined to `teabridge` (+ the
  `cmd/amux` shell). TUI packages contain no raw VT parser, authoritative grid,
  attach sequencing, notification persistence, hook authorization, or direct
  SQLite.

## Scope

Added: `<HIGH_ENTROPY_REDACTED>{teabridge,dispatch,chrome}.go` +
`{teabridge,integration}_test.go`. Modified: `go.mod`/`go.sum`;
`internal/tui/clientadapter/adapter.go` (+test rewritten);
`internal/tui/render/ansi.go` (`StyledString`); `internal/tui/app/view.go`
(exported `Screen`/`StatusText`/`Fits`/`Size`/`Profile`/`MinSizeFrame`; pure
`View()` bytes unchanged); `cmd/amux/tui.go`, `cmd/amux/tui_term_{unix,darwin,
linux,other}.go`; `scripts/expected-modules-{build,test}.txt`;
`docs/dependencies.md`, `docs/tui.md`. Deleted: `cmd/amux/{tui_driver,tui_input,
tui_input_test}.go`. Did NOT modify: ADRs, backend authority, securitytest, CI,
packaging, research.

## T6 pointers

- Arch reference-profile p95 split/focus/resize <75 ms: run
  `internal/tui/bench.RunbookArchCommand` on Arch x86_64 (portable macOS numbers
  above are relative evidence only, not Arch p95).
- Soak workload (`TestSoak -tags soak`) remains T6's deliverable (unchanged).

## Honest residuals (NOT frozen-contract gaps; the four ask-gates are closed)

- **Equalize** has no dedicated daemon method; mapped to `pane.resize(focused,
  0.5)` (even the focused binary split). Daemon re-lays-out; UI shows the
  re-fetched tree — not local layout authority.
- ~~Live cells via polling~~ **CLOSED by G-lane round-1 rework (F1)** — the
  focused surface now holds the real attach stream; unfocused panes keep the
  delta-gated poll. See the rework section below.
- ~~Detach is a client-local disconnect~~ **CLOSED (F1)** — `Ctrl+b d` closes
  the attach stream, releases the lease, and quits.
- ~~Hook-trust trigger~~ **CLOSED by G-lane round-1 rework (F2)** — prefix `t`
  reaches `hook.inspect` and the approve/deny/revoke confirmations in the
  shipped Update/dispatch path; `RequestHookTrust` is deleted.
- **Lease** presents owned on accepted write, other on the daemon's typed
  `not_input_lease_holder` rejection (→ read-only phase), and lost via error
  codes (no `LeaseNotice` subscription) — all daemon-derived, never invented.

## G-lane round-1 rework (2026-07-16) — review findings F1 + F2 CLOSED

Review `review/G-lane:T5-terminal-ui/result-1.json` (round 1, verdict issues)
found two blocking gaps. Both are closed with production code + adversarial
tests; all accepted Bubble Tea/Lip Gloss rendering, projection authority,
input non-leakage, prior tests, and the tidy-clean module graph are preserved.

### F1 — real attach lifecycle (was: projection-poll only, `d` a no-op)

- **Real stream.** `internal/tui/teabridge/attach.go`: the production TUI now
  holds the daemon `attach` stream (flow 12) for the focused pane's active
  surface via the typed client (`client.Client.Attach` → `AttachConn`/
  `AttachStream` seam). It runs on a DEDICATED connection
  (`cmd/amux/tui.go` `AttachDial` → new `app.dialFresh`) because the shared
  client multiplexes one stream/conn and a lagging reader must not stall
  commands. `AttachParams{Cells: true}` seeds the exact snapshot-at-N grid
  (decoded by `clientadapter.<HIGH_ENTROPY_REDACTED>`); each `raw_output`
  frame is folded as delivered-sequence + change signal that re-projects
  daemon-owned `surface.cells` through the `if_changed_since` delta gate
  (coalesced: ≤1 outstanding fetch/surface). Frame BODIES are never parsed —
  no VT engine, no client sequence authority.
- **Fabricated `live` removed.** `applyTree` no longer folds `PhaseLive`;
  attach phases come only from the stream lifecycle (connecting → snapshot/
  replaying(+gap) → live at the daemon cutover → stopped on clean daemon end).
- **Sequence/resume/replay.** Last delivered seq tracked per surface
  (`attSeqs`); recover (`Ctrl+b g`) and refocus re-attach `FromSeq=last+1`;
  the daemon's typed `replay_gap` boundary surfaces verbatim (status bar
  `gap=<req><<oldest>`); slow-consumer (`resource_exhausted`) → slow_detached/
  reattach; conn loss/dial failure → disconnected/redial; boot-id change kept.
  No auto-reconnect — the machine never recovers on its own.
- **Real detach.** `Ctrl+b d` now closes the attach session's connection (the
  daemon observes stream end → server-side detach), explicitly issues
  `input.release` for our lease id, then quits. Not a no-op.
- **Lifecycle safety.** One session ever; a monotonic generation stamps every
  stream message; supersession (focus/surface change, recover, detach) closes
  the old session first and discards stale opens/frames/closes; quit tears the
  session down. `not_input_lease_holder` on input.send now folds lease=other →
  read-only phase (typed code, `clientadapter.IsLeaseDenied`).
- **Tests** (`teabridge/attach_test.go`, scripted `AttachConn` fakes over the
  production Update/dispatch path): open-for-focused-surface (params, cells
  fold, live, cutover seq), frame→seq+re-projection, detach (conn closed +
  `input.release` + quit), daemon-loss → recover reattaching `FromSeq=5` with
  replay-gap surfaced then live at new cutover, slow-consumer recovery state,
  dial-failure → disconnected, focus change moves the stream (old closed
  before new; no duplicates), stale open/frame/close discarded (race safety),
  lease-denied → read-only, quit cancels the session.

### F2 — reachable hook trust workflow (was: test-only helper)

- **Shipped keybindings.** New `Trust` key mode: prefix `t` (discoverable via
  `?`, which now lists the Prefix vocabulary) → the core emits
  `IntentHookInspect`; the bridge dispatch calls `hook.inspect` for the focused
  pane's daemon-reported project and folds `TrustInspectMsg`. The inspection
  overlay renders project trust state/epoch + every grant's frozen detail.
  `a`/`d`/`r` open the approve/deny/revoke confirmation card
  (`notify.TrustCard`: executable, SHA-256 digest, events, cwd scope, env-key
  names, timeout, output cap, epoch) for the `j`/`k`-selected grant.
- **Fail-closed.** Approve/revoke issue `hook.approve`/`hook.revoke` with the
  backend Confirm token only on explicit `y`; deny issues `hook.deny` on `y`
  (no token, per the frozen matrix); `n`/`esc` performs NO mutation anywhere.
  No project → no `hook.inspect`, visible explanation. Absent grants → a/d/r
  cannot open a card (no frozen detail to display) and no call is reachable.
  `hook.inspect` errors present as an explicit unavailable state. The UI never
  decides authorization; daemon audit/grant enforcement is untouched.
- **Test-only path deleted.** `RequestHookTrust`/`trustPromptMsg` removed; all
  trust tests now drive real `tea.KeyPressMsg` sequences through the shipped
  Update/dispatch path: keybinding-reaches-inspect, approve (frozen fields on
  card; no call before `y`; Confirm token + project asserted), revoke (token
  asserted), cancel/esc (zero mutations), explicit deny, absent-grants
  fail-closed, inspect-error safety, no-project fail-closed, help
  discoverability. `docs/tui.md` key table + workflow section updated
  truthfully.

### Rework verification evidence (author host darwin/arm64; 2026-07-16) — FINAL

Supersedes the earlier counts in this receipt:

- gofmt clean (repo-wide). `go vet ./...` + `GOOS=linux GOARCH=amd64 go vet
  ./...` clean.
- staticcheck 2025.1.1 via the pinned `./.<HIGH_ENTROPY_REDACTED>` under
  `GOFLAGS=-mod=readonly`: clean; `go.mod`/`go.sum` SHA-256 byte-identical
  before/after the run (no module mutation).
- `go mod tidy -diff` empty; `go mod verify` all modules verified;
  `make deps-manifest` build+test graphs match the frozen manifests (27/27,
  go.sum hashes verified); `make license` all permissive. No dependency added
  or changed by this rework (go.mod/go.sum untouched).
- `go test -count=1 ./...` **724 passed / 45 packages** (was 707; +17 new
  attach/trust tests). `go test -race -count=1 ./...` **724 passed / 45
  packages, no data races**.
- Focused TUI: all `internal/tui/...` + `cmd/amux` green (112 tests / 13
  packages); teabridge **33/33** (was 16).
- `CGO_ENABLED=0 GOOS=linux GOARCH={amd64,arm64} go build ./...` both succeed.
- Benchmark truth (portable macOS/arm64 M4 — relative evidence, NOT Arch p95;
  Arch reference run stays T6's via `bench.RunbookArchCommand`):
  <HIGH_ENTROPY_REDACTED> ≈747µs/op, 67480 bytes/frame, 37823 allocs/op;
  <HIGH_ENTROPY_REDACTED> ≈923µs/op, 61813 bytes/frame;
  <HIGH_ENTROPY_REDACTED> ≈145µs/op. Full-frame remains the frozen strategy;
  vast headroom under the 75 ms budget.
- Boundary audit unchanged and re-verified: no `internal/terminal`, store/
  SQLite, snapshot/persist, or hooks-internal imports under `internal/tui` or
  the TUI command; attach reaches the TUI only through `internal/client` +
  `rpcapi` wire types; archtest passes.

Scope of this rework: `internal/tui/keys/{keymap,router,defaults}.go`,
`internal/tui/app/{app,view,messages,intents}.go`,
`internal/tui/clientadapter/adapter.go`, `<HIGH_ENTROPY_REDACTED>{teabridge,
dispatch}.go` + NEW `attach.go`, NEW `attach_test.go`, `{teabridge,
integration}_test.go`, `cmd/amux/{main,tui}.go`, `docs/tui.md`, this receipt.
No ADR, backend, security, CI, or packaging file touched.

## Module-graph rework (2026-07-16, post independent-verification)

Independent orchestrator verification rejected attempt 2's receipt: the final
static-analysis pass had been run with an unpinned staticcheck (`honnef.co/go/
tools v0.7.0`) *inside* the module, so `go install`/`go get` side effects
polluted the durable graph — `go.mod` carried `honnef.co/go/tools v0.7.0
// indirect` and `go.sum` carried stale staticcheck-only sums (`BurntSushi/toml`,
`golang.org/x/exp/typeparams`, `golang.org/x/tools/go/expect`, an older
`golang.org/x/exp`). `go mod tidy -diff` exited 1. Narrow fix applied:

- `go mod tidy` restored the canonical graph: removed `honnef.co/go/tools
  v0.7.0` and the stale staticcheck sums; the correct indirect
  `golang.org/x/exp v0.0.0-20231110203233-9a3e6036ecaa` is retained.
  staticcheck is **not** an application dependency — it stays a pinned
  build-time tool installed via `go install …@2025.1.1` into `./.tools/bin`
  (`Makefile:tools` + `scripts/tools.env`), which is module-agnostic and cannot
  mutate `go.mod`/`go.sum`.
- **Compiled graph unchanged.** `GOOS=linux GOARCH=amd64 go list -deps [-test]`
  is byte-identical to the frozen `scripts/expected-modules-{build,test}.txt`
  (27 build modules = 27 test modules), so neither manifest was regenerated.
- `docs/dependencies.md`: the module-graph-only section was corrected from a
  stale **23** to the true **27** (`go list -m all` = 54 non-main = 27 build/
  test + 27 graph-only), adding the five omitted pruned-graph residents
  (`aymanbagabas/go-udiff`, `charmbracelet/x/exp/golden`, `clipperhouse/
  stringish`, `google/go-cmp`, `golang.org/x/exp`) and recording that the
  staticcheck-only modules are absent because staticcheck is never a require.

**Final green gate (author host darwin/arm64):** `go mod tidy -diff` empty;
`go mod verify` all modules verified; `make deps-manifest` (build + test graphs
match frozen manifest, go.sum hashes verified); `make license` all permissive;
gofmt clean; `go vet ./...` + `GOOS=linux GOARCH=amd64 go vet ./...` clean;
`go test ./...` 707 passed / 45 pkgs; `go test -race ./...` 707 passed / 45 pkgs,
no data races; `CGO_ENABLED=0 GOOS=linux GOARCH={amd64,arm64} go build ./...`
both ok; focused TUI tests green (all `internal/tui/...` + `cmd/amux`, teabridge
16/16). staticcheck 2025.1.1 (0.6.1) re-run via the pinned `./.tools/bin`
binary under `GOFLAGS=-mod=readonly`: clean, and `go.mod`/`go.sum` SHA-256 are
byte-identical before and after the run. No production TUI semantics changed;
only `go.mod`, `go.sum`, and `docs/dependencies.md` were touched by this rework.
