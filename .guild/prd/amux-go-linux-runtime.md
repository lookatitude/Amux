---
type: prd
slug: amux-go-linux-runtime
spec: .guild/spec/amux-go-linux-runtime.md
team: .guild/team/amux-go-linux-runtime.plan.yaml
right_size_trigger: multi-feature
created_at: 2026-07-15
approved: true
approved_at: 2026-07-15T05:26:13Z
---

# PRD: Amux Go Linux Workspace Runtime

## Product decision

Build Amux as a clean-room, Linux-first workspace runtime whose only durable authority is a Go daemon. The first release is a programmable terminal multiplexer with a complete CLI and a Bubble Tea client. It deliberately does not pursue cmux UI compatibility, browser embedding, desktop shells, macOS, or Windows in the MVP.

The product is successful when an Arch Linux operator can run one daemon, control the same durable workspace graph from scripts or an interactive TUI, recover it after restart without false process-resurrection claims, and safely integrate selected agent hooks without granting project code ambient execution authority.

## Why this product should exist

Traditional multiplexers are excellent terminal session managers but generally do not expose a typed, replayable workspace command model with stable IDs, semantic notifications, agent lifecycle events, project-scoped hook trust, and snapshot-on-gap recovery. Desktop-first agent workspaces add those concepts but often make Linux secondary or bind durable state to one UI process.

Amux occupies the middle:

- terminal-native and scriptable like a multiplexer;
- stateful and attention-aware like an agent workspace;
- local-first and inspectable like a developer tool;
- Linux-first without blocking later platform adapters;
- clean-room, with no runtime or source dependency on cmux.

## Primary operator and jobs

### Primary persona

An Arch Linux developer running several repositories and AI-assisted coding sessions from one workstation. They expect shell-native automation, transparent process ownership, XDG paths, recoverable state, and predictable behavior under daemon crashes or terminal detachments.

### Core jobs to be done

1. Create a durable session with multiple workspaces and panes without losing stable identities.
2. Run and supervise shells or long-lived commands in PTYs while multiple clients observe them.
3. Navigate a live split tree quickly with keyboard-first focus, resize, attach, and input controls.
4. Script every durable action through a stable CLI and consume a reconnectable event stream.
5. Restore layout and process intent truthfully after restart.
6. See repository, process, notification, and selected agent state without giving adapters ownership of core state.
7. Approve project hooks explicitly, understand their exact capability grant, and revoke them with deterministic launch ordering.
8. Install and update Amux on Arch Linux from a binary package with reproducible release evidence.

## Product principles

1. **One authority.** The daemon owns graph, PTY, event, persistence, notification, and trust state. Clients never become shadow authorities.
2. **Raw output is truth.** Terminal cell grids are derived and replaceable; raw PTY output plus dimensions and ordering remain replay evidence.
3. **Stable identity before presentation.** Sessions, workspaces, panes, and surfaces use opaque stable IDs independent of names or client layout.
4. **Detach is not stop.** A client view may disappear without mutating the process lifecycle.
5. **Recovery is explicit.** Event gaps, restore classifications, failed launches, and stale trust never degrade silently.
6. **Trust follows projects.** Hook authority is bound to canonical filesystem identity and a monotonic epoch, not to a pane label or current directory string.
7. **Linux is the product, not a port.** XDG paths, Unix sockets, signals, process groups, Arch packaging, and glibc runners are first-class acceptance surfaces.
8. **Portability lives behind narrow interfaces.** PTY, local transport, notification delivery, process inspection, and filesystem identity may gain macOS or Windows implementations later without changing the domain model.

## MVP feature requirements

### F1. Daemon, graph, and command authority

- `amuxd` owns `session -> workspace -> split-tree pane -> ordered surface` state.
- A pane has exactly one active surface; MVP surfaces are terminals.
- Per-session graph mutation is serialized through one event-loop goroutine. A separate daemon-global control actor owns the session registry, project identities, hook grants, trust epochs, launch authorization, and cross-session revocation. PTY readers, clients, timers, persistence, and hook workers submit immutable messages to the owning actor.
- Actor ordering is explicit: no session actor may synchronously hold mutable session state while waiting on the global control actor, and the global actor never waits on a session actor while a trust transition is open. Cross-session effects use messages plus revision/epoch checks rather than nested locks.
- Commands are validated before mutation and either commit exactly one deterministic state transition or return a typed error without partial state.
- Every durable mutation is available to the CLI and TUI through the same protocol method.
- Health inspection exposes boot ID, uptime, protocol version, queue pressure, active sessions, PTYs, attachments, persistence state, and event cursor.

### F2. PTY and terminal-state engine

- Unix PTYs are created through a narrow interface backed initially by `github.com/creack/pty`.
- The supervisor owns child process groups, resize, input, output, signal, wait/reap, cancellation, exit status, and shutdown cleanup.
- Each surface owns a bounded raw-output ring retaining at least 16 MiB and monotonically increasing output sequence offsets.
- ANSI/VT decoding uses `github.com/charmbracelet/x/ansi` for streaming control-sequence parsing where its behavior meets the corpus; Amux owns the renderer-neutral cell grid, modes, cursor, damage tracking, and replay contract.
- Unsupported sequences are recorded as bounded diagnostics and must not crash or desynchronize the parser.
- Parser replacement remains possible behind a frozen `TerminalEngine` interface.

### F3. Split layout and navigation

- Create, list, rename, and destroy sessions and workspaces.
- Split panes horizontally or vertically, preserving a deterministic binary split tree.
- Focus by stable pane ID or direction; resize by bounded ratio; equalize subtrees.
- Spawn multiple ordered terminal surfaces in a pane and change its active surface.
- Preserve each pane's cwd, optional project identity, recent-focus metadata, and independent git-root discovery.
- Geometry calculation remains a pure function and handles minimum-size terminals without negative or overlapping rectangles.

### F4. Local protocol, events, and client attachment

- The local control endpoint is an owner-only Unix socket under `$XDG_RUNTIME_DIR/amux/`.
- On Linux every accepted connection must pass `SO_PEERCRED` ownership validation. Startup validates each runtime-path component with no symlink traversal, expected owner and safe mode; stale sockets are removed only after their ownership/type and lack of a live owner are proven.
- Protocol v1 uses bounded length-prefixed frames. Request/response/event headers are JSON; terminal-output frames carry sequenced raw byte bodies without base64 expansion.
- Every connection begins with protocol negotiation and rejects unsupported major versions before accepting commands.
- Events carry boot ID, session ID, monotonically increasing sequence, event type, timestamp, and typed payload.
- The daemon retains bounded event replay and detects subscriber lag. A gap returns a typed recovery boundary requiring a fresh snapshot plus a new cursor.
- Attach returns an atomic metadata snapshot, raw replay ending at a declared output sequence, and then ordered live output strictly after that boundary.
- Multiple observers may attach. Exactly one client may hold a surface input lease; takeover, release, disconnect, and rejected input are evented.
- Detach releases the client's lease and stream only. It never stops the PTY.

### F5. Complete CLI

- `amux` is built with Cobra and uses the same protocol client package as the TUI.
- Human output is stable enough for operators; automation uses `--json` with documented versioned schemas and meaningful exit codes.
- Shell completions are generated for Bash, Zsh, Fish, and PowerShell, although PowerShell completion does not imply Windows runtime support.
- The 20 required flows in the approved spec have automated end-to-end coverage.
- Destructive commands identify their target by stable ID and support explicit noninteractive confirmation semantics.
- A versioned confirmation matrix defines interactive prompts, required `--yes`/takeover flags, refusal codes, and no-TTY behavior for session/workspace destruction, process stop, input-lease takeover, hook approval, and project-trust revocation. Omitted confirmation always fails closed.

### F6. Bubble Tea TUI

- Use Bubble Tea v2 (`charm.land/bubbletea/v2`) and Lip Gloss v2 for the Linux terminal client.
- The client consumes immutable graph, cell, notification, lease, and health snapshots from the daemon-facing client package.
- Rendering composes the visible split tree, pane borders/status, active surface content, focus, unread state, and stopped/restarted state.
- Input has explicit modes: terminal passthrough, command prefix, navigation, resize, surface selection, notification inbox, and confirmation.
- Key bindings are configurable, conflict-checked, discoverable in-app, and have keyboard-only fallbacks.
- The TUI owns no raw VT parser, authoritative cell state, attach sequencing, notification store, or durable mutation.
- Reduced-motion/no-animation behavior, monochrome-safe focus markers, narrow-terminal fallbacks, and screen-reader-friendly noninteractive CLI alternatives are documented.

### F7. Snapshots, SQLite metadata, and restore

- Atomic versioned JSON snapshot manifests store graph shape, stable IDs, cwd, argv, explicit non-secret environment allowlist, restart policy, replay configuration, notification/read checkpoint, and event cursor. Versioned binary replay sidecars carry raw bytes; no 16 MiB stream is base64-embedded in graph JSON.
- Writes use temp file, file sync, atomic rename, directory sync, and a retained previous-known-good snapshot.
- SQLite/WAL through `modernc.org/sqlite` stores indexed metadata, notification records, trust grants, hook audit, and cursor bookkeeping without cgo.
- Every snapshot generation has a unique checkpoint ID, checksummed component manifest, replay sidecars, and a logical notification/read export. Components are prepared and synced first; the manifest rename is the commit point. Partial generations are ignored and the prior committed generation remains usable.
- Live SQLite is canonical for notifications during normal crash recovery and is the sole authority for project trust epochs, grants, revocation, and audit. Explicit snapshot restore may import only its notification/read export; it can never restore, decrease, or reactivate security state.
- Schema migrations are ordered, transactional, forward-only at runtime, and accompanied by export/restore procedures.
- Restore classifies each surface as `live`, `restarted`, or `stopped` with a reason. A fresh daemon can never label a surface `live`.
- Attachments and input leases are ephemeral and never restored.

### F8. Context, notifications, and agent adapters

- Context collectors publish per-pane cwd, git root, branch, dirty state, foreground command, PID, exit state, and timestamps through typed updates.
- Git discovery runs per pane and never assumes one repository per workspace.
- The daemon owns notification creation, routing, unread/read state, latest-unread navigation, dismissal, retention, and persistence.
- Linux desktop delivery is optional and best-effort; failure never removes or marks the in-app notification.
- Initial agent support is limited to 2–3 providers selected during implementation from evidence-backed, structured lifecycle outputs. Before selection, each adapter must freeze its input transport, schema and byte limits, filesystem/environment/process capabilities, timeout/failure isolation, and secret classification.
- Provider adapters translate into core lifecycle/session/attention events; provider-specific payloads do not enter the domain graph.
- Adapters cannot spawn provider binaries, read project files, inherit ambient environment, mutate the graph, or trigger project execution unless that exact capability is separately declared in the approved adapter contract and routed through a daemon command or the hook trust system.

### F9. Hook trust and execution

- Project identity is the SHA-256 digest of canonical realpath plus root `st_dev` and `st_ino`.
- Hook configuration is ignored until the project is opted into reading `.amux/hooks.jsonc`.
- Hooks are executable-plus-argument arrays, never interpolated shell strings.
- Every grant binds executable path and digest, configuration digest, event set, cwd scope, environment-key allowlist, timeout, output cap, project identity, and trust epoch.
- Default timeout is 2 seconds, configurable to at most 30 seconds; output is capped at 1 MiB.
- Inputs, outputs, diagnostics, and audit records pass centralized secret redaction.
- Launch authorization linearizes at final grant/epoch validation immediately before process creation.
- Approved executable/config bytes are bound to the object launched: the Linux implementation must use a descriptor/inode-bound `openat2` plus `execveat`/`fexecve` design or an equivalently race-safe mechanism. Path-only revalidation is insufficient; unsupported executable forms fail closed.
- Revoke-first creates no child. Launch-first terminates and escalates to kill after 2 seconds, with both orderings audit-visible.
- Missing project trust returns `project_trust_required` within 250 ms and creates no child. Revocation cancels queued same-project work across all sessions within 250 ms while preserving a monotonic epoch and inactive audit history.
- The subsystem can be disabled globally without affecting core terminal/session behavior.

### F10. Configuration, diagnostics, and release

- JSONC configuration has an explicit schema version, strict unknown-key handling at durable boundaries, actionable diagnostics, and XDG-compliant paths.
- Logging uses `log/slog`; stdout/stderr are never used for daemon diagnostics that would corrupt a TUI or machine protocol.
- `pprof` is exposed only through an explicitly enabled owner-only local endpoint or diagnostic command.
- OpenTelemetry export is optional and disabled by default.
- Blocking CI runs on Arch and Ubuntu 24.04 for Linux amd64 and arm64 build/package targets.
- Releases provide checksumed, provenance-attested glibc tarballs and an AUR binary package definition.

## Tooling decisions

| Concern | Selected tool | Reason and boundary |
|---|---|---|
| Language/runtime | Go modules with a pinned stable toolchain | One runtime authority, excellent process/concurrency tooling, straightforward Linux distribution. The repository pins the toolchain and dependency checksums; it does not rely on the host's rolling Go package. |
| CLI | `github.com/spf13/cobra` | Mature nested commands, validation hooks, generated completions, and shared client code. Cobra owns presentation only; daemon commands remain independent. |
| PTY | `github.com/creack/pty` | Narrow Unix PTY primitive with Linux support. Amux wraps it so future Darwin/ConPTY adapters do not leak platform types. |
| VT decoding | `github.com/charmbracelet/x/ansi` parser plus Amux-owned cell engine | Reuses a maintained streaming parser while retaining raw bytes and a replaceable renderer-neutral grid. Adoption is gated by the replay corpus, not assumed. |
| TUI | Bubble Tea v2 + Lip Gloss v2; Bubbles only for generic controls | Declarative input/render loop and optimized terminal renderer. Domain state, terminal parsing, and protocol recovery stay outside the UI framework. |
| Persistence | Atomic JSON + `modernc.org/sqlite` | Human-exportable authoritative snapshots plus indexed WAL metadata, cgo-free Linux amd64/arm64 builds. Versions are pinned and tested under race/recovery suites. |
| IPC | Go standard library Unix sockets + Amux framing | Avoids a network-facing server and unnecessary RPC framework. Typed schemas and golden vectors keep protocol evolution testable. |
| Config | Small JSONC lexer over `encoding/json` + JSON Schema artifact | Preserves standard decoding semantics and source locations without a broad configuration framework. Unknown fields fail at contract boundaries. |
| IDs | `github.com/google/uuid` UUIDv7 or an equivalent small audited generator | Opaque sortable identifiers with no semantic coupling. The architect spike verifies monotonicity and clock-regression behavior before freezing. |
| Logging/metrics | `log/slog`, `runtime/pprof`, OpenTelemetry Go SDK | Standard structured logging and profiling first; vendor-neutral export remains optional. |
| Tests | `testing`, fuzzing, `-race`, golden fixtures, fault-injection harness | Prefer standard tooling. Add a property-test library only if stdlib fuzzing cannot express split-tree/state-machine invariants cleanly. |
| Packaging | GoReleaser or an equivalent reproducible release script, hand-reviewed PKGBUILD | Multi-arch tarballs/checksums/provenance plus an Arch-native installation path. The final choice is made by a reproducibility spike, not by convenience alone. |

### Implementation model dispatch policy

Guild's project tier map is configured so `powerful` implementation lanes run on **5.6 Sol**, while `mid` and `cheap` implementation lanes run on **Terra**. The plan deliberately assigns `powerful` to architecture, daemon/process correctness, terminal UI performance, security, and release verification; bounded CI/packaging mechanics remain `mid` and therefore use Terra. Dispatch may escalate a simpler task to 5.6 Sol when the deterministic scorer detects security sensitivity, expanded blast radius, or prior failure, but it must not downgrade a plan-pinned powerful lane.

## Repository and package architecture

```text
cmd/amuxd/                 daemon entry point
cmd/amux/                  CLI and TUI entry point
api/v1/                    protocol schemas, golden vectors, compatibility notes
internal/domain/           IDs, graph, commands, events, invariants
internal/control/          daemon-global registry, trust epochs, launch serialization
internal/session/          per-session graph actor/event loop
internal/protocol/         framing, negotiation, codecs, typed errors
internal/transport/local/  Unix socket listener, peer validation, permissions
internal/client/           shared CLI/TUI protocol client and recovery
internal/pty/              platform-neutral interface and Unix implementation
internal/terminal/         raw replay ring, ANSI parser adapter, cell engine
internal/attach/           replay/live cutover and input leases
internal/snapshot/         versioned JSON, atomic I/O, migration/restore
internal/store/            SQLite migrations and repositories
internal/hooks/            identity, grants, trust epoch, execution, audit
internal/notify/           semantic notification store and delivery adapters
internal/context/          cwd/git/process/agent collectors
internal/tui/              Bubble Tea models, geometry, rendering, keymaps
internal/config/           JSONC, schema, XDG resolution
internal/observability/    slog, metrics, profiling, diagnostics
internal/testkit/          fake clocks, fake PTYs, barriers, fixtures, fault injection
packaging/aur/             PKGBUILD template and install metadata
docs/adr/                  frozen design decisions and migration policy
```

Imports must point inward toward contracts. In particular, `domain` imports no transport, persistence, PTY, TUI, or provider package; `tui` imports only client-facing immutable types; provider adapters import the adapter contract rather than domain internals.

## Command and event contract

### Request lifecycle

1. Client negotiates protocol major/minor and capabilities.
2. Client sends a bounded request with request ID, method, deadline, and typed parameters.
3. Transport validates frame limits and peer ownership.
4. Command decoder rejects unknown durable fields and invalid IDs.
5. The relevant session loop validates invariants, mutates state, allocates event sequence, and returns a typed result.
6. Subscribers observe committed events only; responses and events reference the same resulting revision.

### Error families

- `invalid_argument`
- `not_found`
- `conflict`
- `unsupported_version`
- `not_input_lease_holder`
- `event_gap`
- `replay_gap`
- `project_trust_required`
- `hook_grant_required`
- `hook_grant_stale`
- `scope_denied`
- `resource_exhausted`
- `internal`

Errors include a stable code, human message, retryability, and structured details. Human strings are not automation contracts.

## Data ownership and durability

| Data | Authority | Durability | Recovery rule |
|---|---|---|---|
| Session registry/project trust | Daemon-global control actor | SQLite | Epochs never decrease; old snapshots cannot reactivate grants or audit history. |
| Graph and stable IDs | Session event loop | Snapshot manifest | Reject partial/corrupt generations; retain prior known-good manifest. |
| PTY/process identity | PTY supervisor | Runtime only | Fresh daemon classifies stopped/restarted; never reconstructs live. |
| Raw terminal output | Surface replay ring | Checkpointed binary sidecars | Manifest checksum/generation chooses valid bytes; replay gap is explicit; derived grid is rebuilt. |
| Cell grid | Terminal engine | Derived/runtime | Reproduce from raw fixture; replaceable implementation. |
| Events | Session loop | Bounded ring plus cursor metadata | Gap requires snapshot and cursor reset. |
| Attachments/input leases | Attach manager | Ephemeral | Disconnect releases lease; never snapshot. |
| Notifications | Daemon notification service | Live SQLite; logical export in explicit snapshot | Live DB wins crash recovery; explicit restore imports only the matching committed notification checkpoint. |
| Hook grants/audit | Global control actor/trust service | SQLite only | Never imported from layout snapshots; digest/epoch/config change invalidates active use and retains history. |
| Client view state | Client | Ephemeral/local preference only | Cannot mutate durable state without commands. |

## Delivery waves and exit gates

### Wave 0 — contracts and skeleton

- Freeze ADRs for authority, package imports, IDs, protocol framing/versioning, event ordering, snapshots, platform interfaces, dependency policy, Linux descendant containment, and race-safe executable launch.
- Land buildable `amuxd`/`amux` skeletons, security contracts, CI, lint, race, fuzz smoke, and generated protocol fixtures.
- Exit: graph model and protocol vectors pass without PTY or TUI code.

### Wave 1 — headless graph and control plane

- Implement sessions, workspaces, split trees, surfaces, local socket, commands/events, client recovery, config, and health.
- Complete graph/CLI flows that do not require live PTYs.
- Exit: deterministic state-machine tests and multi-client event-gap recovery pass.

### Wave 2 — PTY, VT, replay, and attach

- Implement process groups, PTY supervision, raw replay, terminal cell engine, attach cutover, input leases, resize, and cleanup.
- Exit: two-client attach contract, VT corpus, and zero-orphan forced-shutdown harness pass.

### Wave 3 — persistence, trust, and backend recovery

- Complete atomic multi-component snapshots, SQLite metadata, restore classifications, hook opt-in/grants/epochs/audit, race-safe launch, redaction, semantic notifications, context collectors, selected agent adapters, and the full backend CLI contract.
- Exit: backend persistence, corruption recovery, multi-repository/cross-session trust isolation, 250 ms trust gates, revoke/launch barriers, confirmation semantics, and all backend protocol/CLI fixtures pass before terminal-ui begins.

### Wave 4 — integrated operator surfaces

- Implement the Bubble Tea 8-pane client, keymap modes, attach/lease recovery, context/status presentation, notification inbox, restore classifications, trust confirmations, and accessible fallbacks over the completed backend contracts.
- Exit: all 20 installed-binary CLI flows, notification UI, restore UI, full TUI acceptance, and the complete functional acceptance suite pass without fixture-only semantics.

### Wave 5 — release hardening

- Meet latency, restore, soak, architecture, packaging, provenance, and operator-documentation gates.
- Exit: 30-minute blocking soak, 8-hour nightly evidence, both architectures, Arch/Ubuntu packages, and release checklist pass.

## Acceptance traceability

| Requirement surface | Primary evidence | Owning lanes |
|---|---|---|
| Session/workspace lifecycle and stable graph | State-machine/property tests plus CLI E2E | architect, backend, qa |
| 8-pane live TUI | Concurrent fake/real PTY fixture and frame assertions | terminal-ui, backend, qa |
| Independent cwd/git roots | Multi-repository integration fixture | backend, qa |
| Snapshot completeness and restore classes | Golden snapshots, migration, clean/in-daemon restore tests | backend, qa |
| Deterministic VT cells | Raw-byte corpus replay to golden grids | backend, terminal-ui, qa |
| Zero orphan PTYs | PID/process-group fault-injection harness | backend, qa |
| Hook fail-closed behavior | Trust matrix and audit assertions | security, backend, qa |
| Event-gap recovery | Forced drop, snapshot refresh, cursor continuation | architect, backend, qa |
| Multi-arch Linux release | Arch/Ubuntu amd64/arm64 build and package jobs | devops, qa |
| Project trust isolation | Distinct realpath/device/inode fixtures and zero-child assertions | security, backend, qa |
| Concurrent attach/input lease | Two-client ordered output and rejected-input test | backend, terminal-ui, qa |
| 20 CLI flows | Versioned black-box command suite | backend, qa |
| Latency/restore budgets | Stable reference-profile benchmark artifacts | terminal-ui, backend, devops, qa |
| 30-minute/8-hour soak | Leak, gap, crash, orphan, queue-pressure telemetry | backend, devops, qa |

## Release gates

The MVP cannot be marked complete while any of these is red:

1. Protocol compatibility vectors and snapshot migration fixtures.
2. Full unit/property/fuzz/race suite on supported Linux architectures where tooling permits.
3. All 20 CLI end-to-end flows.
4. 8-pane TUI acceptance with latency evidence.
5. PTY forced-termination cleanup and orphan scan.
6. Event and replay gap recovery tests.
7. Hook trust, cwd containment, digest invalidation, revocation-ordering, timeout, output-cap, and redaction tests.
8. 30-minute blocking soak; nightly 8-hour soak remains required for release promotion.
9. Arch and Ubuntu package-install smoke tests for amd64 and arm64 artifacts.
10. Operator documentation for backup, restore, diagnostics, hook trust, and package verification.

## Explicitly deferred roadmap

The package boundaries preserve future adapters, but the MVP plan contains no implementation work for:

- macOS PTYs, launch agents, native notifications, or packaging;
- Windows ConPTY, named pipes, services, or installers;
- Wails or any desktop GUI;
- embedded or automated browsers;
- SSH remote daemons or cloud sessions;
- tmux compatibility beyond concepts required by Amux's own CLI;
- OS-level hook sandboxing or network isolation;
- mobile, collaboration, Agent Chat, freeform canvas, Dock, or custom sidebars.

Each deferred family requires its own approved spec because it changes platform, security, packaging, or product-support promises.

## Rollback and kill switches

- TUI failure does not block a headless/CLI alpha; daemon and protocol must remain independent.
- The hook subsystem has a global disable flag and can be removed from the runnable set without changing session behavior.
- Native notification delivery can be disabled while preserving the daemon inbox.
- Snapshot migration failure preserves and reports the previous known-good snapshot; no partial load is committed.
- A VT implementation that fails the corpus can be replaced behind `TerminalEngine` while raw replay remains compatible.
- Release promotion stops rather than weakening performance, soak, trust, or platform gates without explicit spec amendment.

## Open implementation spikes that do not reopen product scope

1. Verify `charmbracelet/x/ansi` parser coverage and extension hooks against the initial VT corpus; fall back to an Amux parser only if the evidence fails.
2. Verify UUIDv7 generation under clock rollback and concurrent creation; select the smallest audited implementation meeting the invariant.
3. Compare GoReleaser with a small repository-owned release script for bit-for-bit inputs, SBOM/provenance generation, and AUR metadata.
4. Select the initial 2–3 agent adapters from providers with stable structured lifecycle outputs and low secret exposure.
5. Measure Bubble Tea v2 full-frame versus pane-damage rendering on the 8-pane and 20-PTY fixtures before freezing redraw strategy.
6. Prove a Linux descendant-containment design for daemon `SIGKILL` using a guardian/cgroup/parent-death strategy, including double-fork, grandchild, process-group escape, guard failure, supported-kernel assumptions, and fail-closed fallback behavior.
7. Prove descriptor-bound executable/config validation and launch using `openat2` plus `execveat`/`fexecve` or an equivalent race-safe mechanism across symlink, rename, replacement, and project-root races.

These spikes may change an internal dependency or adapter choice. They may not change authority, supported platforms, trust semantics, or acceptance thresholds without user confirmation.
