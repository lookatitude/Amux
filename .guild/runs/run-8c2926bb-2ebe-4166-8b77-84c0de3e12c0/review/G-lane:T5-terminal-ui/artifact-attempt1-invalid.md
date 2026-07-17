# T5 terminal-ui — handoff

- task-id: T5-terminal-ui
- owner: terminal-ui
- depends-on: T1-architect (frozen contracts), T4-backend (client-facing state)
- status: done (within frozen contracts; 4 backend-projection ask-gates escalated)

## Summary

Built the Amux terminal client strictly over the frozen client-facing contracts
(`internal/client`, `internal/rpcapi`, `api/v1`, `internal/terminal` cell type):
11 `internal/tui/*` packages (pure geometry, renderer, view models, keymap/router,
attach-recovery state machine, notification/trust presentation, accessibility,
benchmarks, Elm-architecture runtime, integrated app model, and the single
`clientadapter` seam), plus a production `amux tui` entry point. **78 TUI/cmd
tests + 3 benchmarks; full repo 673 tests green (backend untouched); gofmt/vet
clean; race clean; Linux amd64+arm64 CGO_ENABLED=0 builds succeed; scope audit
clean.** The UI consumes immutable projections and issues daemon commands; it
parses no raw VT, owns no authoritative grid/notification/attach state, touches
no SQLite/snapshots, and authorizes no hooks.

## Frozen-contract decision — Bubble Tea v2 (ask-gate, escalated to T1)

Bubble Tea v2.0.8 / Lip Gloss v2.0.5 (PRD-approved) both `require x/ansi
v0.11.7`, which removes the streaming-parser API (`NewParser`, `DataLen`,
`ParamsLen`, indexable `Params`, `Cmd`) that the backend `internal/terminal`
engine + `spikes/ansi` use at the ADR-0007-frozen `x/ansi v0.4.5`. Go allows one
`x/ansi` per module, so adding either v2 module bumps `x/ansi` and **breaks the
backend** (`go build ./...` fails in `internal/terminal` — captured). Adopting
the toolkit requires migrating the backend VT engine — backend-authority + ADR
change forbidden to this lane. **Per the brief I stopped at the ask-gate rather
than modify backend authority, reverted go.mod/go.sum to the frozen state, and
built a dependency-free Elm core shaped 1:1 on Bubble Tea's Model/Update/View so
adoption is a mechanical shell swap once T1 resolves the pin.** Recorded in
`docs/dependencies.md` (“Selected, not yet pinned — BLOCKED”). Only dependency
delta shipped: `rivo/uniseg` promoted indirect→direct (same pin/hash).

## U1–U8 coverage

- **U1 client model boundaries** — `internal/tui/model` (immutable App/Workspace/
  Pane/Surface/Cell/CellSnapshot/Notification/HookGrant/Health + status enums);
  `internal/tui/clientadapter` maps wire types → models; `internal/tui/runtime`
  keeps `Update` deterministic (pure Elm core, I/O only in Cmds/Outbox). Evidence:
  `clientadapter` 3 tests, `app` deterministic-replay test.
- **U2 pure split-tree geometry** — `internal/tui/geometry`: ratio allocation with
  deterministic remainder, per-pane borders + content rects, equalize, resize
  (sibling-floor clamp), directional neighbours. Evidence: 16 tests — exact-tiling
  (no overlap/gap) for 1–8 panes over {80×24, 81×25, 132×43, 37×19}, nested
  splits, odd/tiny/zero terminals, border-drop under 3×3, `divide` sum invariant.
- **U3 pane/cell/status renderer** — `internal/tui/render`: composes CellSnapshot +
  focus/cursor/process/cwd/git/active-surface/unread/lease/restore/exit; mono path
  (no color, attrs+glyphs); wide (width-2 head + width-0 spacer) & combining
  clusters without geometry corruption; ANSI + damage-diff serialisers. Evidence:
  8 tests incl. `<HIGH_ENTROPY_REDACTED>`, combining-mark, monochrome,
  stopped-status, cursor-reverse.
- **U4 input & command modes** — `internal/tui/keys`: 8 explicit modes; conflict-
  checked configurable keymaps (dup-in-mode + prefix-reserved rejected, fail
  closed); Router proves **prefix/nav/mode keys never reach the PTY** (ToPTY only
  in Passthrough, never the prefix key) and Confirmation fails closed (esc/enter
  never consent). Evidence: 10 tests.
- **U5 attach & recovery UX** — `internal/tui/attachstate`: <HIGH_ENTROPY_REDACTED>
  live/read-only/lease-owned/takeover/gap-recovery/slow-<HIGH_ENTROPY_REDACTED>
  daemon-restarted/stopped; maps `v1` codes + `client.ErrBootChanged` → recovery
  recommendation (redial / <HIGH_ENTROPY_REDACTED> / reattach); never stitches gaps.
  Evidence: 7 tests + `clientadapter.ClassifyErr` mapping test.
- **U6 notification & trust** — `internal/tui/notify`: inbox (newest-first, unread,
  latest-unread routing, mark-read intent w/o local authority, local dismiss,
  delivery-failure); `TrustCard` shows every frozen trust field, marks wire-
  missing fields UNAVAILABLE, states fail-closed confirmation, decides nothing.
  Evidence: 9 tests.
- **U7 performance & accessibility** — `internal/tui/a11y` (NO_COLOR/dumb→mono,
  truecolor detect, reduced-motion, ASCII borders, min-size fallback, keyboard
  help/discovery, CLI-alternative pointer) + `internal/tui/bench` (8-pane/20-surface
  fixture; full-frame vs damage-aware strategies recording frame-time/allocs/
  bytes). Evidence: 6 a11y tests, 2 bench tests + 3 benchmarks. Portable numbers
  below. `docs/tui.md` documents accessibility + the screen-reader CLI alternative.
- **U8 regression corpus** — `internal/tui/runtime.Drive` replays recorded message
  streams; `internal/tui/app` golden frames (`testdata/*.golden`: four_pane_focus,
  stopped_restarted, trust_confirmation, min_size) + intent/mode assertions for
  <HIGH_ENTROPY_REDACTED>-confirm/gap-<HIGH_ENTROPY_REDACTED>-routing/
  determinism. Evidence: 12 app tests (goldens verified without `-update`).

## Production entry point

`amux tui` (registered in `cmd/amux`): dials the shared client (no TUI-only
path), seeds health/notifications/session/workspace, runs the raw-mode driver
(termios via `x/sys/unix`, Linux+darwin; input decoder incl. atomic bracketed
paste; SIGWINCH resize; full-frame render), and dispatches model intents to
daemon methods (pane.focus/split/resize, input.send/release, surface.select,
notification.read, hook.approve/deny/revoke with the frozen confirm token).
`amux tui --preview` renders a static demo frame (no daemon/TTY). `--mono` and
NO_COLOR/AMUX_REDUCED_MOTION/AMUX_ASCII honored.

## Evidence / commands run (author host: darwin/arm64)

- `gofmt -l` clean; `go vet ./...` clean.
- `go test ./...` → **673 passed / 44 pkgs**; `go test -race ./internal/tui/... ./cmd/amux/` → 78 passed.
- `CGO_ENABLED=0 GOOS=linux GOARCH={amd64,arm64} go build ./...` → both succeed.
- `go mod verify` → all modules verified; `internal/archtest` (import rules + TestNoCgo) → 3 passed.
- Scope audit: no `internal/{store,hooks,daemon,snapshot,pty,attach,ordering,
  session,persist,securitytest}` or `modernc.org/sqlite` imports under
  `internal/tui`; `internal/terminal` imported ONLY by `clientadapter`; all 9
  pure packages import only `internal/tui/*` + `uniseg` + stdlib.
- **Portable benchmark evidence (macOS/arm64, NOT Arch p95):** FrameLatency
  (compose) ~0.14–0.18 ms/op; RenderFullFrame ~0.72 ms/op, 67 KB/frame, 37.8k
  allocs; RenderDamageAware ~0.85 ms/op, 62 KB/frame. Full-frame meets the 75 ms
  budget with ~100× headroom and is simpler → **recommended default** (damage-diff
  retained for churny scenes). Design is capable of the p95 <75 ms gate.

## Honest Arch / T6 deferrals

- **No Arch p95 claim from macOS.** The exact Arch reference command is frozen at
  `internal/tui/bench/fixture.go` → `RunbookArchCommand` (Arch x86_64, release,
  `-bench BenchmarkRender|<HIGH_ENTROPY_REDACTED> -benchmem -benchtime=2000x
  -count=10`, then p95 of ns/op < 75e6). T6 runs it on the Arch reference profile.
- Linux-only runtime behaviour (real termios raw-mode loop, live attach) is T6
  integration evidence; the author host exercises the pure model + `--preview`.

## Ask-gates → T4 backend contract requests (documented, not locally faked)

Recorded verbatim in `internal/tui/clientadapter/adapter.go` and `docs/tui.md`:

1. **Live cell delivery** — `attach_snapshot` payload carries pane meta only, not
   `terminal.CellSnapshot`; live output is raw VT the TUI may not parse. Needs a
   client-facing cell snapshot/delta projection. `<HIGH_ENTROPY_REDACTED>` is
   ready to map it.
2. **Hook trust detail** — `hook.list` lacks <HIGH_ENTROPY_REDACTED>-scope/env-keys/
   timeout/output-cap; `TrustCard` marks them UNAVAILABLE. Needs a hook.inspect
   detail projection.
3. **Pane context** — `pane.inspect` lacks git branch/dirty + foreground process.
4. **Pane split-tree structure** — no wire method returns a workspace's split tree
   (orientation/nesting/ratios) needed for geometric layout.

The UI compensates with none of these; each is a blocking backend contract
request, and the pure packages already consume the intended client-facing model.

## T6 handoff pointers

- Pure geometry/model tests: `internal/tui/{geometry,model,render,keys,attachstate,
  notify,a11y}` (78 tui/cmd tests).
- Golden frames: `<HIGH_ENTROPY_REDACTED>*.golden` (regenerate: `go test
  ./internal/tui/app -update`); recorded-stream driver: `runtime.Drive`.
- Performance hooks: `internal/tui/bench` (`<HIGH_ENTROPY_REDACTED>`,
  `<HIGH_ENTROPY_REDACTED>`, `<HIGH_ENTROPY_REDACTED>`); Arch command in
  `RunbookArchCommand`.
- Recorded client-event sequences: `app` message types (`PaneTreeMsg`,
  `PaneContentMsg`, `LeaseMsg`, `AttachEventMsg`, `AttachErrMsg`,
  `NotificationsMsg`, `HealthMsg`, `ConfirmRequestMsg`, `TrustPromptMsg`).
- Error→recovery mapping to pin: `clientadapter.ClassifyErr`.

## Scope

Added: `internal/tui/**`, `cmd/amux/tui*.go` (+ `main.go` registration), `docs/tui.md`,
`go.mod`/`go.sum` (uniseg direct promotion only), `docs/dependencies.md`
(reconciliation). Did NOT modify: ADRs, backend authority, `api/v1`,
`internal/rpcapi`, security corpus, CI, packaging, research.
