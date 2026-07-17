# `amux tui` — interactive terminal client (operator guide)

`amux tui` is the interactive split-pane client for the Amux daemon. It renders
daemon-provided state and issues daemon commands over the **same** protocol
client every other `amux` subcommand uses — there is no TUI-only mutation path,
and the UI never parses raw terminal output, owns authoritative grids, sequences
attach streams, or decides trust/lease authority. All of that is the daemon's.

```
amux tui                 # attach the first session/workspace
amux tui -s <session> -w <workspace>
amux tui --preview       # render a static demo frame to stdout (no daemon, no TTY)
amux tui --mono          # force the monochrome / no-color path
```

## Interaction model

A tmux-style prefix (`Ctrl+b`) opens command mode; the prefix and all command
keys are consumed by the UI and **never** leak to the focused process. Only in
passthrough mode does typing reach the PTY.

| In prefix mode (`Ctrl+b` then…) | Action |
|---|---|
| `h` `j` `k` `l` | focus pane left / down / up / right |
| `%` / `"` | split horizontally / vertically |
| `=` | equalize splits |
| `r` | enter resize mode (arrows grow/shrink, `enter` exits) |
| `n` | enter navigation mode (arrows move focus, `enter` exits) |
| `o` / `i` | next / previous surface |
| `s` | select the active surface |
| `!` | open the notification inbox |
| `u` | jump focus to the latest unread notification |
| `T` | request input-lease takeover (**confirmation required**) |
| `x` | release the input lease |
| `t` | hook trust: inspect the focused pane's project (then `a`/`d`/`r`, see below) |
| `d` | detach: closes the attach stream, releases the input lease, quits the client (does **not** stop the process) |
| `g` | recover after a gap / disconnect / daemon restart (re-attaches from the last delivered sequence) |
| `?` | help / keybinding discovery (lists the prefix commands) |
| `esc` | cancel back to passthrough |

Destructive and trust-granting actions (lease takeover, hook approve/revoke,
surface stop) **fail closed**: they open a confirmation and require an explicit
`y`; `esc`/`n` denies, and `enter` is never treated as consent.

### Hook trust workflow (`Ctrl+b t`)

`Ctrl+b t` fetches the **`hook.inspect`** projection for the focused pane's
project and opens the trust inspection: project identity and trust state/epoch
plus every grant's frozen detail (executable path + SHA-256 digest, events, cwd
scope, env-key **names**, timeout, output cap). Inside it:

| Key | Action |
|---|---|
| `a` | request **approve** — opens the confirmation card (`y` issues `hook.approve` with the confirm token) |
| `r` | request **revoke** — opens the confirmation card (`y` issues `hook.revoke` with the confirm token) |
| `d` | request **deny** — opens the card (`y` issues `hook.deny`; no token needed per the frozen matrix) |
| `j` / `k` | select the grant whose detail the card will show |
| `esc` | close (no mutation) |

The card renders the frozen grant detail verbatim before any confirmation.
Cancelling (`n`/`esc`) performs **no** daemon call; with no grants on the
projection, approve/revoke/deny are unreachable (there is no frozen detail to
confirm against — use `amux hook …`). The TUI never decides authorization:
every transition is a daemon command subject to daemon-side audit and grant
enforcement.

Keymaps are configurable and **conflict-checked**: a key bound to two actions in
one mode, or a passthrough binding that shadows the prefix key, is rejected
before the UI starts (fail closed) rather than silently picking a winner.

## Accessibility

- **No color / monochrome focus.** `NO_COLOR` (or `--mono`, or `TERM=dumb`)
  selects the monochrome path: focus and state are shown with border weight,
  bold/reverse attributes, and glyphs — never color alone. `AMUX_ASCII=1` draws
  borders with ASCII (`+ - |`) for screen readers and legacy terminals.
- **Reduced motion.** `AMUX_REDUCED_MOTION=1` suppresses spinners/animation;
  frames are static.
- **Minimum size.** Below the terminal floor the UI shows a plain "terminal too
  small" message with the required size and a pointer to the CLI — it never
  renders a corrupt partial frame.
- **Keyboard-only.** Every action is reachable from the keyboard; `?` lists the
  bindings for discovery. No action requires a mouse (mouse focus is additive).

## Screen-reader-friendly / non-interactive alternative

The interactive TUI is a convenience over a fully scriptable CLI. Every action
has a non-interactive `amux` subcommand that emits stable text or `--json`:

| TUI action | Non-interactive command |
|---|---|
| focus a pane | `amux pane focus …` |
| split a pane | `amux pane split …` |
| resize a split | `amux pane resize …` |
| inspect a pane | `amux pane inspect …` (or `amux inspect …`) |
| select / spawn / stop / restart a surface | `amux surface select|spawn|stop|restart …` |
| attach to output | `amux attach <surface> -s <session>` (raw or `--json`) |
| send input | `amux input …` |
| read replay | `amux replay …` |
| list / read notifications | `amux notification list|read …` |
| inspect / approve / deny / revoke hooks | `amux hook list|approve|deny|revoke …` |
| subscribe to events | `amux event subscribe …` |

Screen-reader users and automation should prefer the `--json` forms, which emit
the daemon's stable result schemas and the deterministic exit-code table
documented in `amux --help`.

## Runtime and backend projections

The interactive client is a **Bubble Tea v2** program (`charm.land/bubbletea/v2`
+ `charm.land/lipgloss/v2`, pinned in `go.mod`). All terminal I/O — raw mode,
input decoding, resize, alt-screen, bracketed paste — is owned by Bubble Tea;
every model transition is a pure, deterministic fold (golden-tested), and every
daemon call is isolated in a Bubble Tea command. Lip Gloss renders the
geometry-safe chrome (status bar, minimum-size fallback); the authoritative cell
grid is rendered by the pure renderer so backend-owned cell widths are never
re-measured.

The client renders from the daemon's four read-only projections (protocol minor
1), consumed through `internal/tui/clientadapter` — it never fabricates or
locally approximates any of them:

1. **Live cell content** — the TUI is a real **attached client**: for the
   focused surface it holds the daemon `attach` stream (flow 12) on a dedicated
   connection, seeds from the exact snapshot-at-N `cells` payload, and treats
   each delivered output frame as the daemon's change signal to re-project
   `surface.cells` (`if_changed_since` delta gate); unfocused panes refresh by
   the same delta-gated poll. The raw frame body is **never parsed** — the UI
   holds no VT engine and no sequence authority. It tracks the last delivered
   sequence only to resume: recovery (`Ctrl+b g`) and refocus re-attach
   strictly after it, and an evicted cursor surfaces the daemon's typed
   `replay_gap` boundary (visible as `gap=…` in the status bar). Slow-consumer
   disconnects, connection loss, and daemon restarts all present as explicit
   recovery states — never auto-stitched. `Ctrl+b d` closes the stream (the
   daemon observes the detach), releases the input lease, and exits.
2. **Full hook trust fields** — `hook.inspect` delivers project identity,
   executable + SHA-256 digest, config digest, events, cwd scope, env-key
   allowlist (names only), timeout, output cap, and bound epoch. The
   confirmation card shows them verbatim and the UI never decides authorization
   (approve/deny/revoke are daemon calls gated by the frozen confirmation
   matrix).
3. **Pane git/process context** — `pane.context` (cwd, git root/branch/dirty,
   foreground PID/cmd, recorded exit). Absent fields fail closed to blank —
   never guessed.
4. **Workspace split tree** — `workspace.tree` (orientation, nesting, ratios,
   focus, ordered surfaces) drives the pure geometry layout.

`amux tui --preview` renders a representative frame using the built-in demo
scene, independent of a live daemon.
