# L1 Architect Handoff — Round 1

- run_id: `run-bc5df50b-2431-46bf-94a0-624f9dd33115`
- loop: `L1`
- lane: `phase:brainstorm`
- role: `architect`
- dispatch: Guild Codex host adapter, read-only, model `gpt-5.4`
- note: two in-process dispatches did not return a handoff; the host-adapter fallback completed successfully.

## Proposed bounded brief

Build a clean-room, Go-authoritative workspace runtime for Linux first, optimized for Arch Linux and terminal-native use. The MVP is a local-first multiprocess workspace supervisor with a daemonized Go core, PTY management, a Bubble Tea TUI, a socketed CLI/control plane, event stream, workspace split tree, snapshots, notifications, agent hooks, and rich session metadata including git, cwd, process, and pane state. It is a programmable workspace runtime, not a tmux clone: deterministic state, inspectable events, and automation-first boundaries.

### MVP components

- `amuxd`: long-lived Go daemon owning sessions, workspaces, panes, PTYs, state, snapshots, and IPC.
- `amux`: CLI for lifecycle, attach, split, focus, resize, snapshot, restore, inspect, and event subscription.
- Bubble Tea TUI for split-tree navigation, terminal attach, process status, notifications, and operator actions.
- PTY runtime for spawn/reap, resize, input/output, exit tracking, and signals.
- Explicit workspace/tab/split-tree model with stable IDs and serializable state.
- Versioned local IPC plus sequence-numbered event stream.
- Metadata for repository root, branch, dirty state, cwd, command, PID, exit code, and timestamps.
- Snapshots of layout, environment policy, cwd, restart intent, and bounded replay; never claim process-memory restoration.
- In-app notifications with optional Linux desktop delivery.
- Typed agent lifecycle adapters and hook events with bounded inputs.

### Architecture constraints

- Go is the only authority for runtime, state, IPC, and persistence.
- Linux/Arch is the first-class MVP target; other platforms are later adapters.
- Clean-room implementation; do not copy or link the ambiguous-license `cmux-tui` code.
- Single-user and local-only in MVP.
- State is typed, versioned, and replayable from snapshots plus events.
- The TUI, CLI, and later desktop client call the same command surface.
- Hooks and agents are untrusted edge actors and cannot bypass daemon authority.

### Recommended defaults

- Unix socket beneath `$XDG_RUNTIME_DIR`; named-pipe adapter reserved for Windows.
- JSON-RPC-like commands and sequence-numbered JSON events.
- Atomic versioned JSON snapshots plus SQLite/WAL metadata through `modernc.org/sqlite`.
- Bubble Tea, Lip Gloss, and Bubbles for the TUI; Cobra for CLI shape.
- xterm.js only when the desktop shell arrives; preserve raw VT/replay streams meanwhile.
- Hook execution disabled until explicitly trusted; typed executable JSON protocol rather than shell interpolation.
- Snapshot autosave after debounced layout mutations and process lifecycle changes.

### Measurable criteria proposed

- Cold daemon start to ready state under 150 ms on the agreed Arch reference machine.
- Four-pane/four-PTY workspace creation under 500 ms; attach under 100 ms.
- Split/focus/resize event visible to a subscribed client at p95 under 50 ms.
- Sustain 20 concurrent PTYs for a defined soak duration without daemon crash, orphaned child processes, or event gaps.
- Snapshot save under 200 ms and restore to usable 20-pane layout under 2 seconds.
- Event replay reproduces the last persisted tree and stable IDs deterministically.
- At least 90% of MVP operator flows are available from the CLI without the TUI.
- Normal and forced shutdown tests leave zero orphan PTYs.

### Non-goals for MVP

- Embedded-browser parity, cloud VMs, mobile, freeform canvas, Agent Chat.
- Full tmux command/config compatibility.
- Native GPU terminal renderer or `libghostty` integration.
- Wails desktop shell, SSH remote daemon, multi-user collaboration.
- macOS/Windows parity claims.

### Risks and rollback

- PTY/signal edge cases, state-tree complexity, misleading restore promises, unsafe hook execution, and scope creep toward cmux parity.
- Keep optional subsystems capability-gated; daemon/CLI can ship before the TUI is fully interactive; hooks and desktop notifications can be disabled independently; restore can fall back to layout plus explicit restart commands.

### Blocking questions

1. MVP hierarchy: `workspace → tabs → split tree → surfaces`, or omit a level?
2. Snapshot boundary: store cwd, allowlisted env, command argv, and optional boot input, or less?
3. One repository root per workspace or multi-repository workspaces in MVP?
4. Typed executable hook protocol from day one or shell hooks first?
5. Does the MVP require terminal rendering inside Bubble Tea, or can it initially manage and attach external terminals?
6. Confirm browser tier: headful Chromium/CDP beta after MVP, not embedded parity.
7. Confirm Windows tier: buildable/best-effort after macOS, not supported parity.

## Handoff receipt

- loop_id: `loop-clarify`
- lane_id: `phase:brainstorm`
- round: 1
- role: `architect`
- status: `proposed`
- next: `researcher challenge`
- evidence:
  - `.guild/runs/019f6360-6c58-7d41-ba38-c4498e3c719d/research/cmux-linux-replication-deep-dive.md`
  - `.guild/runs/run-bc5df50b-2431-46bf-94a0-624f9dd33115/handoffs/loop-clarify/round-1-architect.md`
