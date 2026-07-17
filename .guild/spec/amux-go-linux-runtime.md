---
type: spec
slug: amux-go-linux-runtime
owner: orchestrator
confidence: high
risk_level: high
status: approved
approved: true
approved_at: 2026-07-15T03:37:00+01:00
source_refs:
  - .guild/runs/019f6360-6c58-7d41-ba38-c4498e3c719d/research/cmux-linux-replication-deep-dive.md
  - .guild/runs/run-bc5df50b-2431-46bf-94a0-624f9dd33115/loops/loop-clarify-summary.md
created_at: 2026-07-15
updated_at: 2026-07-15
---

# Amux Go Linux Workspace Runtime

## Goal & outcome

Build a clean-room, Go-authoritative, local-first terminal workspace runtime inspired by cmux’s durable product concepts: an authoritative workspace graph, PTY-backed terminal surfaces, a complete CLI/control plane, sequence-numbered events, persistent layouts, attention routing, and bounded agent lifecycle integration.

The MVP must feel like a programmable workspace runtime rather than a collection of terminal windows. An Arch Linux user can start one daemon, create and restore multi-pane workspaces, operate every core flow from the CLI, use an interactive terminal multiplexer TUI, observe agent/process/git context, and automate the runtime through one versioned command and event contract.

## Audience / operator

- Primary: Linux developers and AI-assisted engineering operators using Arch Linux as their daily environment.
- Secondary: operators on other glibc Linux distributions who want a self-contained daemon, CLI, and TUI.
- Future, not MVP: macOS and Windows desktop users, remote SSH operators, and embedded-browser workflows.

## Product boundary

### Compatibility target

Amux is a clean-room implementation of concepts. It does not copy cmux code, does not depend on `cmux-tui`, and does not promise cmux CLI, protocol, configuration, or pixel-level UX compatibility. The pinned cmux repository is evidence and inspiration, not a dependency.

### Authoritative object model

```text
daemon
  -> sessions
    -> workspaces
      -> split-tree panes
        -> ordered surfaces
```

- Each pane has exactly one active surface.
- MVP surfaces are terminals; the type boundary must permit later browser or viewer surfaces without changing graph identity.
- Desktop windows are future client views and are not daemon-owned core state.
- Workspaces are repository-agnostic with an optional primary root.
- Each pane has an independent cwd and per-pane git-root discovery.
- Session, workspace, pane, and surface IDs are opaque, stable, and preserved through snapshots.

### Project identity and trust boundary

- Hook trust is project-scoped, never workspace-scoped. A project is identified by the canonical `realpath` of an explicitly selected root plus the root filesystem identity (`st_dev`, `st_ino`); its durable key is the SHA-256 digest of that tuple. Moving, replacing, or remounting a root changes the identity and invalidates trust.
- A Git-backed pane selects the discovered Git worktree root. A non-Git pane has no project until the operator explicitly registers a root; cwd alone never silently creates a trust boundary.
- Project hook configuration is read only from the fixed project-relative path `.amux/hooks.jsonc`, after project opt-in. A workspace spanning multiple repositories therefore has independent trust, config, grants, audit history, and trust epochs for each project.
- `workspace-primary` cwd scope resolves only when the workspace has one explicitly configured primary root and that root has the same project identity as the hook grant. It is denied before launch when the root is absent, ambiguous, replaced, or belongs to another project.
- `pane` cwd scope is allowed only when the target pane resolves to the same project identity as the hook grant. Cross-project and unregistered panes fail closed; no grant implicitly crosses project boundaries.

## MVP feature scope

### Runtime and state authority

- `amuxd`, a long-lived Go daemon, owns all session, workspace, pane, surface, PTY, notification, hook, snapshot, and event state.
- Mutation is serialized through an event-loop goroutine per session; clients submit immutable command inputs.
- `amux`, the CLI client, and the Bubble Tea TUI call the same versioned command surface. No TUI-only durable mutation exists.
- The daemon exposes inspectable health, process, pane, event-cursor, snapshot, and hook state.

### PTY and terminal surfaces

- Unix PTYs use a narrow platform interface initially backed by `github.com/creack/pty`.
- PTY supervision covers spawn, resize, input, output, signal, reap, exit status, cancellation, and orphan cleanup.
- A Go VT parser and cell-state engine sits behind a renderer-neutral interface.
- Bubble Tea composes live terminal cell state across the visible split tree; the MVP is a real terminal multiplexer, not an external-terminal launcher.
- Raw PTY output remains protocol truth. Derived cell state must be reproducible from replay fixtures.
- Each surface retains at least the most recent 16 MiB of raw PTY output, configurable upward under a documented global storage budget.

### Layout and navigation

- Multiple sessions and workspaces.
- Horizontal and vertical pane splits.
- Directional focus and explicit pane targeting by stable ID.
- Pane resizing and equalization.
- Multiple ordered terminal surfaces per pane with one active surface.
- Workspace and surface names, descriptions, status, and recent-focus metadata.

### Control plane and events

- Versioned JSON-RPC-like requests over a Unix socket beneath `$XDG_RUNTIME_DIR`.
- Sequence-numbered event stream with boot identity, bounded replay, slow-consumer handling, and snapshot-on-gap recovery.
- CLI commands cover lifecycle, graph mutation, PTY input/replay, inspection, snapshot/restore, hooks, notifications, and event subscription.
- Local socket access is restricted to the owning user. Remote/network API exposure is not part of MVP.

### Client attach and detach contract

- `attach` is an ephemeral client-view operation over the control socket, not durable graph state. The TUI and `amux attach <pane-id>` both use this operation and count as attached clients while their stream is open.
- Multiple clients may attach to the same pane concurrently. Each receives an atomic pane/surface metadata snapshot, bounded raw replay ending at a declared output sequence, then ordered live PTY output and pane lifecycle events strictly after that sequence.
- Output attachments are shared. Interactive input uses one explicit per-surface input lease; a client must acquire or deliberately take over that lease before sending bytes. Lease acquisition, takeover, release, and disconnect are evented, and input from non-owners is rejected without reaching the PTY.
- Detach closes the client's stream and releases any input lease it owns. Detach never stops, signals, restarts, or removes the pane or PTY process.
- Backpressure follows the event-stream slow-consumer policy: a lagging attachment is disconnected with its last delivered sequence and must reattach using bounded replay or snapshot-on-gap recovery.

### Persistence and restore

- Atomic, versioned JSON snapshots preserve graph state and stable IDs.
- SQLite/WAL through `modernc.org/sqlite` stores indexed metadata, notifications, hook grants/audit, and event cursors without requiring cgo.
- Snapshots persist pane cwd, argv, explicit non-secret environment allowlist, restart policy, the 16 MiB replay floor, notification/read state, and event cursor.
- Restart policy defaults to `manual`.
- Snapshots never persist stdin boot input, arbitrary inherited environment, process memory, secrets outside the explicit allowlist, or browser state.
- Restore classifies every terminal surface as exactly one of `live`, `restarted`, or `stopped`. `live` is valid only for an in-daemon restore that reconciles the snapshot to the same still-owned PTY/process identity; a fresh daemon can never manufacture this state. `restarted` means a new process was launched under an explicit automatic policy. `stopped` is the default for manual policy, missing executables/cwds, or failed validation and includes an accurate reason.
- Client attachments are never persisted or recreated by restore. Restore is usable only when the tree and each surface classification are visible; clients attach separately through the attach contract.

### Context and attention

- Per-pane cwd, detected git root, branch, dirty state, foreground command, PID, exit state, and timestamps.
- Daemon-owned notification inbox with read/unread state, pane/workspace routing, and latest-unread navigation.
- Optional Linux desktop notification delivery is an adapter; in-app state is authoritative.
- Agent adapters can publish typed lifecycle/session/attention events for an initial 2–3 explicitly selected providers without putting provider parsing in the core state model.

### Hook trust and execution

- Hooks are typed JSON executables, never interpolated shell strings.
- Project hook configuration is ignored until the operator opts that project into hook-config reading.
- Each hook additionally requires a grant bound to executable absolute path, executable/config digest, allowed event set, cwd scope, environment-key allowlist, timeout, and output cap.
- Cwd access is separately denied by default. Hooks otherwise run in an Amux-owned scratch directory. A grant selects exactly one scope: `fixed`, `workspace-primary`, or `pane`; the daemon validates the resolved path.
- Default timeout is 2 seconds, maximum configurable timeout is 30 seconds, and output is capped at 1 MiB.
- Inputs, outputs, and audit records pass secret redaction.
- CLI and TUI expose hook inspect, approve, deny, and revoke flows. Noninteractive missing/invalid trust fails closed with actionable instructions.
- A changed executable, digest, event set, cwd scope, environment allowlist, timeout, or cap invalidates the hook grant.
- Revoking project trust cancels queued invocations, marks retained hook grants inactive, and requires both project and per-hook reapproval.

### Linearizable hook launch contract

- Launch authorization and revocation are serialized by the daemon and guarded by a monotonic project trust epoch.
- Launch linearizes at final successful grant and epoch validation immediately before process creation.
- If revocation linearizes first, no hook child is created.
- If launch linearizes first, the invocation is in-flight; revocation sends terminate and escalates to kill after 2 seconds.
- Some instructions may execute in the launch-first ordering. MVP guarantees no launch linearizes after revocation; it does not claim retroactive zero-execution, OS sandboxing, or network isolation.

### Configuration, observability, and packaging

- JSONC configuration with explicit schema version and generated shell completions through Cobra.
- XDG config, data, state, cache, and runtime paths.
- Structured `slog` logging, bounded local audit/event logs, `pprof`, and optional OpenTelemetry export.
- Prebuilt glibc tarballs for Linux `x86_64` and `aarch64`.
- AUR binary package for Arch Linux.
- Arch rolling CI/reference lane plus Ubuntu 24.04 LTS as the second glibc CI/support target.
- musl, AppImage, deb/rpm repositories, system GUI packaging, and automatic update channels are not MVP gates.

## Success criteria

### Functional acceptance

1. The daemon creates, persists, restores, lists, and destroys sessions containing multiple workspaces.
2. The TUI renders an 8-pane live split tree backed by concurrent PTY fixtures with correct focus, resize, redraw, input, and exit behavior.
3. Each pane supports an independent cwd and git-root discovery without requiring a workspace-wide repository.
4. Snapshot restore preserves stable IDs, tree shape, cwd, argv, allowed environment, restart policy, 16 MiB replay floor, notification state, and cursor.
5. Every restored terminal surface is visibly `live`, `restarted`, or `stopped` under the restore classification contract; client attachments are absent until a client explicitly attaches, and no UI state implies process-memory resurrection.
6. VT replay fixtures deterministically reproduce expected cell grids for the supported escape-sequence corpus.
7. Normal daemon shutdown and forced daemon termination recovery leave zero unmanaged/orphaned PTYs in the integration harness.
8. Hook timeout, output, schema, environment, cwd, trust, and digest failures fail closed and remain audit-visible.
9. Snapshot-on-gap recovers a deliberately dropped event and resumes from a new valid cursor without silent state drift.
10. Both Linux architectures compile and package; Arch and Ubuntu CI run the blocking functional suite.
11. A multi-repository workspace proves that project opt-in, grants, trust epochs, and revocation remain isolated per canonical project identity; unregistered roots and invalid `workspace-primary`/`pane` scopes start zero hook processes.
12. Two concurrent attached clients receive the same ordered output stream, input from the non-lease-holder is rejected, deliberate lease takeover is evented, and detaching either client leaves the pane process running.

### Required CLI flow contract

All 20 flows must exist and pass automated end-to-end tests:

1. start daemon
2. stop daemon
3. create session
4. list sessions
5. create workspace
6. list workspaces
7. split pane horizontally
8. split pane vertically
9. focus pane
10. resize pane
11. spawn terminal surface
12. attach to pane
13. send input to pane
14. read bounded replay
15. inspect pane state
16. save snapshot
17. restore snapshot
18. restart a stopped pane
19. stop a pane process
20. subscribe to events

### Performance and reliability

- CI profile: documented 4-vCPU, 8-GiB glibc Linux runner.
- Reference profile: documented Arch Linux `x86_64` workstation kept stable for release comparisons.
- Blocking soak: 30 minutes with 20 concurrent PTYs, no daemon crash, unrecovered event gap, or orphan process.
- Nightly soak: 8 hours on the reference profile with the same pass conditions.
- Restore an 8-pane release fixture from a clean daemon to usable state in under 2 seconds on the reference profile.
- Split, focus, and resize appear in the active TUI frame and subscribed event stream at p95 under 75 ms on the reference profile.
- Event IDs remain monotonic and contiguous during normal operation; every injected gap must recover through a snapshot refresh or fail the test.

### Hook trust acceptance

- With project trust absent, hook activation returns `project_trust_required` within 250 ms, starts zero processes, and does not place project hooks in the runnable set.
- After project and per-hook approval, execution audit references both active grants.
- Project trust revocation cancels queued work within 250 ms, prevents later launches, retains inactive grant history, and requires both grants to be freshly approved.
- Deterministic concurrency barriers force both orderings: revoke-first creates no child; launch-first follows the terminate/2-second-kill audit path.
- Cwd-containment tests deny a hook before launch when its resolved path falls outside the granted scope.

## Non-goals

- Reusing or linking `cmux-tui`, `libghostty`, or any Rust mux authority.
- cmux CLI, socket, config, or exact UX compatibility.
- Full tmux command/config compatibility.
- Native GPU terminal rendering, Ghostty configuration parity, or cgo.
- Wails desktop shell or xterm.js frontend in MVP.
- Embedded browser parity, headless/headful CDP browser beta, browser import, profiles, or proxy UI in MVP.
- SSH remote daemon, cloud VMs, multi-user collaboration, mobile client, freeform canvas, or Agent Chat in MVP.
- macOS support in MVP.
- Windows, ConPTY, named pipes, or Windows packaging in MVP.
- musl/static-Linux support.
- OS-level hook sandboxing or network isolation guarantees.

## Constraints

- stack: Go is the sole authority for runtime, state, PTYs, IPC, persistence, and policy.
- platform: Arch Linux is the development/reference platform; MVP release support is glibc Linux `x86_64` and `aarch64`, verified on Arch and Ubuntu 24.04 LTS.
- legal: clean-room implementation; no reuse of the ambiguously licensed cmux portable subsystem without separate written legal clearance and explicit rescoping.
- security: single-user local socket, default-denied hooks, least-exposed environment/cwd, explicit trust, redaction, bounded execution, linearizable revocation.
- architecture: UI, CLI, hooks, and restore call one daemon command model; no second state authority and no Go/Rust mux split.
- delivery: daemon/control/state foundations precede feature-differentiator and platform-expansion work.

## Autonomy policy

- may act without asking: choose internal Go package boundaries; select or write a Go VT parser behind the frozen interface; define versioned request envelopes; choose snapshot JSON versus SQLite indexing details; tune bounded queues/replay above the minimum; choose Bubble Tea components; choose Linux notification and git-discovery adapters; add tests, CI, benchmarks, and documentation inside the approved scope.
- requires confirmation: weakening any success criterion; changing the object model; introducing cgo/Rust; adding a network listener; changing hook trust semantics; adding a new supported OS or browser tier; adding dependencies with material licensing obligations; changing persisted public contracts after an alpha release.
- forbidden: copying cmux code; claiming cmux compatibility; storing inherited secrets by default; executing untrusted project hooks; bypassing the daemon as state authority; claiming OS/network sandboxing; implementing cloud/mobile/browser/desktop scope under the MVP plan.

## Risks & rollback

- Go VT correctness may lag mature terminal engines → keep raw PTY bytes authoritative, gate on replay corpus tests, and allow the VT implementation to be replaced behind its interface.
- Bubble Tea composition may not meet terminal fidelity/performance → preserve daemon/CLI protocol and ship a headless/attach alpha while the TUI remains release-gated; do not alter core state to accommodate renderer quirks.
- PTY/signal edge cases can leave children behind → centralize lifecycle ownership, add process-group cleanup tests, and keep daemon shutdown idempotent.
- Snapshot migration can corrupt recoverability → version every snapshot, write atomically, retain the previous known-good snapshot, and fail closed with an exportable diagnostic rather than partially loading.
- Hook execution expands attack surface → capability gates remain default-denied and independently disableable; the entire hook subsystem can be turned off without affecting terminal/session operations.
- Performance budgets may prove unrealistic on the measured profile → report evidence and seek explicit approval before changing budgets; do not silently weaken gates.
- Arch rolling changes can break packaging → pin CI images/toolchains where necessary, keep Ubuntu LTS as a stability lane, and publish tarballs independently of AUR metadata.
- Scope can expand toward full cmux parity → treat the Non-goals section as a release gate; new surfaces require a new spec/plan.

## Release sequence

1. Foundation: domain graph, commands/events, Unix socket, persistence schemas, IDs, config, observability.
2. Terminal core: PTY supervisor, VT/replay engine, attach/input/resize, process cleanup.
3. Operator surfaces: complete CLI contract, Bubble Tea split-tree TUI, context metadata.
4. Reliability and trust: snapshots/restore, notifications, hooks and trust/audit, gap recovery.
5. Linux release: Arch/Ubuntu CI, soak/bench gates, `x86_64`/`aarch64` tarballs, AUR binary package, user documentation.

## Assumptions (from brainstorm)

- No blocking unknown was assumed through; L1 terminated after six architect/researcher rounds with a clean `## NO MORE QUESTIONS` sentinel.
- The draft intentionally chooses Go despite Rust remaining the stronger reuse-oriented option because this project is a clean-room Go implementation.
- Browser, macOS, and Windows are roadmap directions only; they do not influence MVP acceptance beyond preserving platform interfaces.
- The slug `amux-go-linux-runtime` and every proposed default in this draft remain subject to the explicit user spec-approval gate.
