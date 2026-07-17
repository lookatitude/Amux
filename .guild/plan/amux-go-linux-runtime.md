---
type: plan
spec: .guild/spec/amux-go-linux-runtime.md
team: .guild/team/amux-go-linux-runtime.plan.yaml
backend: auto
created_at: 2026-07-15
approved: true
approved_at: 2026-07-15T05:26:13Z
implementation_model_policy:
  powerful: "5.6 Sol"
  mid: "Terra"
  cheap: "Terra"
---

# Plan: Amux Go Linux Workspace Runtime

## PRD

PRD: .guild/prd/amux-go-linux-runtime.md

### Execution dependency gates

| Gate | May start when | Required handoff |
|---|---|---|
| T1 architect | Spec and team approved | ADRs, contracts, protocol/snapshot fixtures, Linux containment and race-safe launch spike decisions. |
| T2 security | T1 complete | S2–S5 authorization, endpoint, confirmation, 250 ms, cross-session, TOCTOU, and rollback matrices. S4 reviews the A4 pre-spawn seam, not backend implementation. |
| T3 devops | T1 complete | Reproducible toolchain, CI/package/release definitions, and build/version interface. D3/D4 author pipelines against the skeleton; integrated execution is deliberately deferred to Q8. |
| T4 backend | T1, T2, and T3 complete | Backend consumes security policy/fixtures and the CI/build interface. B11 cannot start without S2–S5. |
| T5 terminal-ui | T4 complete | Immutable client models, cell snapshots/deltas, attach/lease recovery, notifications, and confirmation contract. The whole UI lane waits rather than guessing partial backend APIs. |
| T6 qa | T2, T3, T4, and T5 complete | Q1 finalizes the plan's existing traceability baseline from all receipts; Q2–Q8 execute the integrated evidence and package pipelines. |

This is a strict DAG: `T1 -> {T2,T3} -> T4 -> T5 -> T6`, with T6 also consuming T2 and T3 directly. No lane waits on an artifact produced by one of its downstream consumers.

## Lane: architect

- task-id: T1-architect
- owner: architect
- depends-on: []
- complexity_score: 4
- tier: powerful
- scope: Freeze the Go authority boundaries and versioned contracts before implementation begins. Produce the decisions that every daemon, client, persistence, security, packaging, and test package must follow.
- success-criteria:
  - `docs/adr/0001-authority-and-package-boundaries.md` defines the daemon-global control actor, per-session graph actors, cross-actor message/lock ordering, import direction, ownership table, and prohibited duplicate authorities.
  - `docs/adr/0002-domain-graph-and-identifiers.md` freezes graph invariants, opaque ID behavior, split-tree rules, revisions, and deletion semantics.
  - `docs/adr/0003-local-protocol-v1.md` freezes framing, negotiation, request/response/event envelopes, limits, typed errors, compatibility policy, and golden vectors under `api/v1/testdata/`.
  - `docs/adr/0004-event-and-attach-ordering.md` proves the snapshot/replay/live cutover, boot/session/output sequences, slow-consumer boundary, and input-lease state machine.
  - `docs/adr/0005-persistence-and-restore.md` freezes canonical ownership per field, checkpoint generation IDs, manifest/replay/notification components, commit ordering, SQLite precedence, non-rollback of trust state, migrations, previous-known-good handling, and `live|restarted|stopped` classification.
  - `docs/adr/0006-platform-interfaces.md` defines narrow PTY, descendant containment, local transport, peer credentials, notification, process-inspection, filesystem-identity, descriptor-bound launch, and clock interfaces without implementing non-Linux platforms.
  - `docs/adr/0007-dependency-and-compatibility-policy.md` records selected libraries, license checks, pin/update policy, protocol/snapshot compatibility windows, and cgo prohibition.
  - A pinned `go.mod`/`go.sum` and toolchain declaration, buildable `cmd/amuxd` and `cmd/amux` skeletons, dependency/license manifest, and baseline `go test ./...` exist before T2/T3/T4 dispatch.
  - `internal/domain` property tests prove graph invariants and deterministic command transitions before transport or UI packages depend on them.
  - A dependency-rule test or static check rejects imports from `internal/domain` into transport, persistence, PTY, TUI, or provider adapters.
- autonomy-policy:
  - may act without asking: choose package names, initialize the approved Go skeleton/toolchain, define internal interfaces, write ADRs and schemas, prototype protocol vectors, run bounded spikes/tests, and refine implementation-neutral invariants inside the approved spec.
  - requires confirmation: changing the object model, persisted public contract, trust semantics, supported platform, cgo policy, or any acceptance threshold.
  - forbidden: implementing cmux compatibility, introducing a second state authority, copying cmux code, or adding browser/desktop/remote scope.

### Work packages

1. **A1 — Repository skeleton and dependency direction**
   - Establish `cmd/`, `api/v1/`, `internal/control`, `internal/session`, `docs/adr/`, `packaging/`, and test-fixture boundaries from the PRD.
   - Initialize the pinned Go module/toolchain, minimal buildable `amuxd`/`amux` entry points, dependency/license manifest, and baseline test target so T2 security fixtures and T3 CI never depend on downstream bootstrap.
   - Specify which packages may expose interfaces and which must remain implementation details.
   - Add an architecture test that parses Go imports and enforces the dependency graph.
2. **A2 — Domain model and invariants**
   - Define immutable command inputs and explicit state-transition results.
   - Specify split-tree node invariants, surface ordering, active-surface validity, focus history, optional primary root, and project association.
   - Define deletion/cascade behavior and typed conflict errors.
3. **A3 — Protocol v1 and compatibility**
   - Write canonical request, response, event, snapshot, attach, and error examples.
   - Set frame/header/body limits, deadlines, heartbeat behavior, and unknown-field rules.
   - Define major/minor negotiation and the compatibility test matrix.
4. **A4 — Ordering and concurrency proof**
   - Model command commit, event allocation, PTY output sequencing, replay cutover, lease takeover, disconnect, restore, and hook launch/revoke linearization across two sessions sharing one project.
   - Record happens-before relationships, global-control/session message ordering, absence of nested actor waits, and ownership of every mutable counter and trust epoch.
5. **A5 — Persistence/platform contracts**
   - Freeze component preparation, checksum, fsync and manifest-commit steps; live-WAL responsibilities; explicit notification export; replay sidecars; migrations; restore precedence; and every partial-write recovery ordering.
   - Define Linux implementations and compile-only placeholder boundaries for future Darwin/Windows files without adding support claims.
6. **A6 — Tool spikes and decision closure**
   - Run bounded spikes for `charmbracelet/x/ansi`, UUIDv7, Bubble Tea damage strategy inputs, release tooling, Linux daemon-death descendant containment, and descriptor-bound executable/config launch.
   - The containment spike must kill `amuxd` with `SIGKILL` while shells double-fork, create grandchildren, or change process groups; the launch spike must race symlink, rename, byte replacement, config replacement, and project-root replacement.
   - Convert each spike into an ADR result with evidence and delete throwaway code not selected.

### Handoff contract

T1 architect hands T2 security, T3 devops, T4 backend, and T5 terminal-ui a reviewed package map, protocol/snapshot fixtures, state-machine definitions, frozen error codes, performance measurement points, and a decision log. Downstream lanes may not independently redefine these contracts; proposed changes return as ADR amendments under the spec's confirmation rules.

## Lane: backend

- task-id: T4-backend
- owner: backend
- depends-on: [T1-architect, T2-security, T3-devops]
- complexity_score: 5
- tier: powerful
- scope: Implement the Go daemon and shared protocol client: graph authority, PTY/VT runtime, attach/event streams, persistence/restore, CLI, notifications, context, agent adapters, and hook execution plumbing. This lane is pinned upward because it owns process correctness and the largest blast radius.
- success-criteria:
  - `amuxd` starts on an owner-only XDG Unix socket and exposes protocol negotiation, health, and clean shutdown.
  - The daemon-global control actor serializes session registry and project trust state while session actors apply graph commands deterministically; cross-session revocation tests publish contiguous committed events without deadlock or post-revoke launch.
  - The PTY supervisor passes spawn, resize, input, signal, exit, cancel, reap, process-group cleanup, and forced-daemon-termination tests with zero harness-detected orphans.
  - Raw replay retains at least 16 MiB per configured surface and the VT corpus reproduces golden cell grids deterministically.
  - Two concurrent clients pass atomic attach, ordered replay/live cutover, lease rejection/takeover/release, slow-consumer disconnect, and detach-without-stop tests.
  - Snapshot manifests, replay sidecars, notification exports, and SQLite implementations pass every partial-write ordering, migration, corruption, previous-known-good, clean-daemon restore, in-daemon live reconcile, stopped/restarted reason, and stale-security-generation test.
  - All 20 required CLI flows pass black-box end-to-end tests with both human and `--json` output contracts where applicable.
  - Multi-repository context discovery and daemon-owned notifications persist and route correctly without workspace-wide repository assumptions.
  - Initial agent adapters emit only typed lifecycle/session/attention events and pass redaction fixtures.
  - Hook execution consumes the completed security authorization contract and passes descriptor-bound launch races, 250 ms trust/revoke gates, cross-session epoch, timeout, output-cap, environment, cwd, cancellation, and audit tests.
  - `go test ./...`, targeted fuzz tests, and `go test -race ./...` pass on the supported Linux CI lane, excluding only documented architecture/tool limitations.
- autonomy-policy:
  - may act without asking: implement packages and migrations inside frozen contracts, choose internal data structures, tune bounded queues above minimums, add fixtures, and select the initial adapters using the PRD spike criteria.
  - requires confirmation: changing a frozen ADR, persisted/protocol compatibility behavior, hook authorization meaning, cgo policy, supported OS, destructive migration, or minimum replay/performance gate.
  - forbidden: exposing a network listener, allowing TUI-only durable mutation, persisting inherited secrets, running unapproved hooks, claiming process resurrection, or introducing a Rust mux authority.

### Work packages

1. **B1 — Binary wiring, config, and lifecycle**
   - Extend the T1 buildable skeleton without changing its module/toolchain/dependency policy; wire production command roots and version metadata.
   - Implement XDG resolution, JSONC decoding with locations and strict durable-boundary fields, boot identity, signal handling, single-instance/socket lifecycle, and health state.
   - Keep daemon logs off protocol and TUI streams.
2. **B2 — Global control and per-session graph actors**
   - Implement the daemon-global session registry/project trust actor plus IDs, revisions, entities, split tree, focus history, surface ordering, commands, validation, and immutable event payloads in per-session actors.
   - Use bounded actor mailboxes and one-way/revision-checked handoffs; enforce the ADR's no-nested-wait ordering. No blocking PTY, SQLite, git, or hook work runs on an actor goroutine.
   - Add model/state-machine/property tests for arbitrary valid command sequences.
3. **B3 — Protocol and local transport**
   - Implement bounded frame reader/writer, JSON header codec, optional raw body, handshake, deadlines, cancellation, mandatory Linux `SO_PEERCRED` validation, and owner-only socket permissions.
   - Validate every runtime-path component without symlink traversal and reject unsafe owner/mode/type. Remove a stale socket only after proving it is owned, is a socket, and has no live server; keep pprof/diagnostics local and owner-gated.
   - Generate or validate schemas/golden vectors and build a shared reconnecting client.
   - Add malformed-frame, oversized-body, partial-read/write, disconnect, and version-skew tests.
4. **B4 — Event replay and recovery**
   - Allocate boot/session event sequences only after commit.
   - Implement bounded replay, filtered subscriptions, heartbeats, slow-consumer policy, explicit `event_gap`, atomic state snapshot, and cursor re-establishment.
   - Prove no silent drift with injected drops and reconnect races.
5. **B5 — PTY/process supervisor**
   - Wrap `creack/pty` behind the ADR interface; launch process groups with explicit cwd/argv/environment allowlist.
   - Implement the A6-selected Linux guardian/cgroup/parent-death containment mechanism; centralize resize, input, signal, exit/wait, cancellation, graceful/forced stop, daemon shutdown, and orphan scans.
   - Cover daemon `SIGKILL`, double-fork, grandchildren, process-group escape, guard failure, kernel-feature absence, and fail-closed fallback without claiming unsupported containment.
   - Ensure each terminal's lifecycle state and exit reason are evented once.
6. **B6 — Raw replay and terminal engine**
   - Build bounded chunk storage with output sequences, memory/storage budgets, snapshot hooks, and replay-gap reporting.
   - Adapt `charmbracelet/x/ansi` if the spike passes; own cell grid, cursor, SGR/modes, wide/combining cells, alternate screen, scrolling regions, resize/reflow policy, damage sets, and unsupported-sequence diagnostics.
   - Maintain raw fixture -> golden grid and differential replay tests.
7. **B7 — Attach and input leases**
   - Linearize pane metadata/cell snapshot and raw replay ending at sequence N before delivering live output >N.
   - Implement observer fanout, one lease per surface, acquire/takeover/release/disconnect, rejected writes, lifecycle events, and lag disconnect receipts.
   - Never couple detach with process stop.
8. **B8 — Persistence and restore**
   - Implement versioned manifest, replay-sidecar and notification-export codecs; generation/checksum validation; component-first fsync; manifest-last commit; previous-good retention; migration; and diagnostic refusal.
   - Add SQLite WAL migrations/repositories for live notifications, grants/audit, metadata, and cursors. Trust epochs/grants/audit are SQLite-only, monotonic, and excluded from layout restore; stale combinations fail closed.
   - Implement exact live/restarted/stopped classification and restart-policy validation.
9. **B9 — CLI and machine contracts**
   - Implement daemon, session, workspace, pane, surface, attach, input, replay, inspect, snapshot, restore, restart, stop, event, hook, notification, and diagnostics command families.
   - Add stable JSON output schemas, exit-code table, completions, timeouts, ID targeting, and the security-approved confirmation matrix for destroy, stop, lease takeover, hook approval, and trust revocation. Missing TTY/confirmation fails closed.
   - Build the 20-flow E2E script against a real daemon and PTYs.
10. **B10 — Context, notification, and agent adapters**
    - Add bounded/debounced cwd, git-root/branch/dirty, foreground process, PID, exit, and timestamp collectors outside the actor.
    - Implement semantic notification persistence/routing/read state/latest-unread and a replaceable Linux delivery adapter.
    - Select and implement 2–3 structured agent adapters only after freezing transport, schema/size, filesystem/environment/process capabilities, timeouts, failure isolation, and redaction. Undeclared provider spawning, project reads, ambient environment, graph mutation, and project execution are denied.
11. **B11 — Hook runtime integration**
    - Implement config loading only after project opt-in, executable argv launch, scratch cwd, environment allowlist, timeout/output limits, audit, and redaction.
    - Open and validate config/executable objects race-safely, bind digest/inode to the launched descriptor with `openat2` plus `execveat`/`fexecve` or the approved equivalent, and fail closed for unsupported forms.
    - Consume the security authorization result immediately before launch; expose deterministic same-project/two-session revoke barriers; meet 250 ms absent-trust and queued-cancellation gates.
    - Keep authorization policy out of worker/process plumbing.
12. **B12 — Diagnostics and performance hooks**
    - Add `slog` correlation fields, queue/replay/PTY/attach metrics, bounded diagnostic dumps, local-only pprof controls, and optional OpenTelemetry wiring.
    - Add benchmark entry points used by QA and devops reference profiles.

### Handoff contract

T4 backend supplies T5 terminal-ui with versioned immutable client models, attach/lease APIs, cell snapshots/deltas, notification views, typed errors, and fake-client fixtures. It implements the completed T2 security contract and the T3 devops build/version interface. It supplies T6 QA with deterministic binaries, fixture controls, metrics, package inputs, and fault-injection seams.

## Lane: security

- task-id: T2-security
- owner: security
- depends-on: [T1-architect]
- complexity_score: 4
- tier: powerful
- scope: Freeze the fail-closed security contract and executable adversarial fixtures for local transport, project identity, hook grants, trust epochs, race-safe launch/revoke ordering, cwd/environment containment, redaction, and audit before backend implementation begins.
- success-criteria:
  - `docs/security/threat-model.md` covers assets, actors, trust boundaries, STRIDE threats, abuse cases, mitigations, residual risks, and explicit non-guarantees.
  - `docs/security/hook-authorization.md` specifies project opt-in, identity tuple/digest, grant binding fields, invalidation, monotonic cross-session epochs, descriptor-bound launch, revocation, 250 ms gates, and fail-closed error codes.
  - Unix socket permissions, mandatory Linux peer credentials, runtime-component owner/mode/type checks, symlink/stale-socket attacks, request limits, diagnostic endpoints, and local denial-of-service controls have testable requirements.
  - A generated trust matrix covers absent/stale/revoked project and hook grants, replaced/remounted roots, cross-project panes, all cwd scopes, digest/config changes, timeout/output bounds, and environment allowlists.
  - Deterministic fixtures require absent trust to return within 250 ms with zero children, cross-session revocation to cancel queued work within 250 ms, revoke-first to create zero children, and launch-first to terminate then kill after the 2-second boundary with audit evidence.
  - Symlink, rename, executable/config-byte replacement, and project-root replacement race fixtures prove the approved object executes or launch fails closed.
  - Restore fixtures prove old layout/notification/replay generations cannot decrease a trust epoch, reactivate a grant, erase inactive audit history, or authorize launch.
  - Secret-redaction fixtures cover config, environment, hook input/output, errors, logs, audit, snapshots, and agent adapter payloads without storing raw secrets in golden files.
  - The security design review reports no unresolved high-severity contract finding before backend dispatch; QA must execute the supplied fixtures before release promotion.
- autonomy-policy:
  - may act without asking: author threat models, test matrices, adversarial fixtures, review backend designs, and require fixes that enforce the approved trust contract.
  - requires confirmation: accepting a high-severity residual risk, widening hook cwd/environment/event authority, weakening peer validation, or changing revocation guarantees.
  - forbidden: silently changing product scope, claiming OS/network sandboxing, executing real untrusted hooks during review, or approving by documentation without executable evidence.

### Work packages

1. **S1 — Threat model and boundary inventory**
   - Model user, local process, malicious repository, compromised hook executable, stale client, malformed adapter, and corrupted persistence threats.
   - Inventory socket, filesystem, process, environment, PTY, notification, diagnostics, and release boundaries.
2. **S2 — Project identity and trust state machine**
   - Specify canonicalization without time-of-check/time-of-use ambiguity, device/inode capture, replacement/remount invalidation, global cross-session ownership, and trust epoch monotonicity.
   - Define opt-in, approve, deny, revoke, inactive history, and reapproval transitions.
3. **S3 — Grant and containment matrix**
   - Bind executable/config object identity and digests, events, cwd scope, environment keys, timeout, output cap, and epoch; require descriptor-bound validation/launch or an equivalent race-safe primitive.
   - Define `fixed`, `workspace-primary`, and `pane` resolution and cross-project denial before launch.
4. **S4 — Linearizable launch/revoke protocol**
   - Review the architect-frozen global-control/worker boundary and identify the final pre-spawn authorization point without requiring backend completion.
   - Build deterministic same-project/two-session fixtures for both legal orderings, 250 ms gates, object-replacement races, and the prohibition on launch after completed revoke.
5. **S5 — Redaction, audit, and local endpoint review**
   - Centralize data-classification/redaction rules and test structured fields, byte streams, truncation, and error paths.
   - Require `SO_PEERCRED`, no-symlink runtime traversal, owner/mode/type validation, hostile stale-socket handling, owner-only local pprof/diagnostics, bounded inputs, and second-UID/resource-exhaustion tests.
6. **S6 — Preimplementation security-readiness gate**
   - Freeze dependency/license/vulnerability scanners, secret-scan policy, malicious fixtures, manual misuse cases, severity/blocking rules, and reproducible invocation commands before backend dispatch.
   - Publish `docs/security/security-readiness.md` plus a machine-readable manifest listing every required integrated-candidate check and the security-review receipt schema.

### Handoff contract

T2 security hands T4 backend a completed authorization state machine, race-safe launch requirements, validation/confirmation matrices, error taxonomy, 250 ms/cross-session barriers, adversarial fixtures, and the security-readiness manifest. It hands T6 QA the same executable trust/readiness evidence for integrated verification. T3 devops derives its parallel dependency/provenance pipeline from T1 ADR 0007 and does not consume T2 output. Product-semantic changes return to the user confirmation boundary.

## Lane: devops

- task-id: T3-devops
- owner: devops
- depends-on: [T1-architect]
- complexity_score: 2
- tier: mid
- scope: Build the Linux development, CI, packaging, provenance, and operational evidence pipeline for Arch and Ubuntu across amd64 and arm64 without introducing runtime cgo or unsupported-platform promises.
- success-criteria:
  - The repository pins the Go toolchain and dependencies; CI verifies module integrity, formatting, static analysis, generated artifacts, licenses, and clean working tree.
  - Arch and Ubuntu 24.04 jobs compile and run the blocking unit/integration suite; amd64 runs race/fuzz/performance jobs and arm64 runs the documented feasible subset or native equivalent.
  - Release definitions can produce glibc Linux amd64/arm64 `amux` and `amuxd` tarballs with checksums, SBOMs, provenance/attestation, version metadata, and reproducible input records once backend binaries land; QA owns integrated execution evidence.
  - `packaging/aur/PKGBUILD` templates install binaries, licenses, completions, documentation, and optional user-service examples without starting untrusted services automatically.
  - Package-install smoke harnesses are runnable in clean Arch and Ubuntu environments; QA executes them against the integrated candidate.
  - The 30-minute soak is blocking and the 8-hour soak is scheduled nightly with retained logs, metrics, orphan scan, event-gap status, and memory/goroutine profiles.
  - Release documentation defines versioning, artifact verification, rollback, snapshot backup, compatibility checks, and AUR update procedure.
- autonomy-policy:
  - may act without asking: choose CI implementation details, container/VM images, caching, release script structure, artifact retention, and observability collection inside approved platforms.
  - requires confirmation: changing supported distro/architecture, enabling cgo, publishing externally, adding signing infrastructure requiring credentials, or weakening blocking gates.
  - forbidden: committing secrets, treating cross-compilation as runtime support evidence, auto-starting hooks, or marking flaky/red jobs optional to obtain a release.

### Work packages

1. **D1 — Reproducible developer toolchain**
   - Pin Go and tool versions; add `make` or `just`-style thin task entry points without hiding underlying commands.
   - Add format, vet/static analysis, test, race, fuzz-smoke, generate-check, license, and dependency-integrity targets.
2. **D2 — Linux CI matrix**
   - Define Arch rolling and Ubuntu 24.04 environments, amd64/arm64 strategy, native PTY prerequisites, retry policy, and artifact capture.
   - Separate deterministic blocking tests from scheduled long-running jobs; never mask platform failures with `continue-on-error` for supported targets.
3. **D3 — Release pipeline**
   - Evaluate GoReleaser versus a small owned script using the architect spike criteria.
   - Define version-stamped binary, tarball, checksum, SBOM, provenance, changelog, and clean-install jobs with a skeleton fixture; integrated artifact evidence is a Q8 gate after backend completion.
4. **D4 — Arch packaging**
   - Author binary PKGBUILD metadata, completions, license/docs install, optional systemd user unit example, and integrity pins.
   - Validate with clean chroot/package tooling and document AUR maintenance.
5. **D5 — Soak/performance operations**
   - Standardize 4-vCPU/8-GiB CI and Arch reference-profile metadata.
   - Retain structured logs, pprof snapshots, metrics, fixture seed, version, kernel/distro, and pass/fail summary for every soak/benchmark.
6. **D6 — Release and rollback runbooks**
   - Document backup/restore, daemon upgrade/downgrade compatibility, failed migration containment, artifact rollback, and incident evidence collection.

### Handoff contract

T3 devops supplies T6 QA with reproducible runners, package/release definitions, and evidence locations, and supplies T4 backend with build/version interfaces before backend implementation begins. T6 QA, not this pre-backend lane, produces integrated installable-artifact evidence. Publishing remains a separate explicit user-authorized operation.

## Lane: terminal-ui

- task-id: T5-terminal-ui
- owner: terminal-ui
- depends-on: [T1-architect, T4-backend]
- complexity_score: 3
- tier: powerful
- scope: Implement the Bubble Tea client strictly over frozen backend/client contracts: split-tree geometry and rendering, terminal input modes, focus/resize/surface navigation, attach/lease presentation, notification workflows, redraw efficiency, and accessible fallbacks.
- success-criteria:
  - An 8-pane concurrent PTY fixture renders correct non-overlapping geometry, borders, focus, cell content, cursor, status, unread state, and exit/restore classifications.
  - Directional focus, explicit focus, horizontal/vertical split requests, resize, equalize, surface selection, attach/detach, and input lease workflows use daemon commands and pass deterministic model tests.
  - Terminal passthrough never sends command-prefix/navigation keystrokes to the PTY; paste, mouse, resize, Unicode, wide cells, and combining marks have explicit tests.
  - Lease loss/takeover, replay gap, event gap, daemon restart, slow-consumer detach, stopped surface, and hook approval states are visible and recoverable without inventing local durable state.
  - Notification inbox, read/unread, latest-unread navigation, focus routing, dismissal, and delivery-failure presentation operate over backend semantic state.
  - Keymap conflict validation, help/discovery, monochrome focus, minimum-size fallback, reduced-motion behavior, CLI alternatives, and destructive/takeover confirmation presentation are documented and tested against the backend confirmation matrix.
  - Split/focus/resize appears in the active frame at p95 under 75 ms on the Arch reference profile, with frame time, allocations, and bytes-written evidence.
  - TUI packages contain no raw VT parser, authoritative cell grid, attach sequencing, notification persistence, hook authorization, or direct SQLite access.
- autonomy-policy:
  - may act without asking: choose Bubble Tea model decomposition, key defaults, Lip Gloss styles, damage aggregation, view caches, test fixtures, and accessible presentation within frozen contracts.
  - requires confirmation: changing protocol/client contracts, durable state, default destructive key behavior, performance threshold, or adding a new UI/platform toolkit.
  - forbidden: parsing PTY bytes authoritatively, bypassing input leases, mutating SQLite/snapshots directly, executing hooks, or building Wails/browser/desktop scope.

### Work packages

1. **U1 — Client model boundaries**
   - Define immutable app/workspace/pane/surface/cell/notification/health view models derived from the shared client.
   - Isolate I/O in Bubble Tea commands and keep `Update` deterministic under recorded message streams.
2. **U2 — Pure split-tree geometry**
   - Implement ratio allocation, borders, content rectangles, minimum dimensions, equalization, and directional-neighbor selection as pure functions.
   - Add golden/property tests for 1–8 panes, odd dimensions, nested splits, Unicode border widths, and extremely small terminals.
3. **U3 — Pane/cell/status renderer**
   - Compose backend-provided cell snapshots/deltas with focus, cursor, process, cwd/git, active-surface, unread, lease, restore, and exit decorations.
   - Preserve a plain/monochrome rendering path and avoid styling that changes cell geometry unexpectedly.
4. **U4 — Input and command modes**
   - Implement explicit passthrough, prefix, navigation, resize, surface, notification, help, and confirmation modes using the frozen confirmation contract rather than UI-local authority.
   - Validate configurable keymap conflicts and make mode/lease ownership continuously visible.
5. **U5 — Attach and recovery UX**
   - Present connecting, replaying, live, read-only, lease-owned, takeover-confirmation, gap-recovery, disconnected, and daemon-restarted states.
   - Use backend recovery APIs; do not locally stitch sequence gaps.
6. **U6 — Notification and trust presentation**
   - Build inbox, routing, latest-unread, mark/dismiss, and delivery-failure views over semantic backend state.
   - Build hook inspect/approve/deny/revoke confirmations that display project identity, executable/digest, events, cwd scope, env keys, timeout, and cap without deciding authorization.
7. **U7 — Performance and accessibility**
   - Measure full-frame and damage-aware strategies on 8-pane/20-PTY fixtures; freeze the simpler approach meeting the budget.
   - Add reduced motion, no-color/limited-color, minimum-size, keyboard-only, discoverability, and noninteractive CLI fallback documentation.
8. **U8 — Snapshot/model regression corpus**
   - Record deterministic Bubble Tea message sequences and golden frames for focus, resize, output, notifications, lease changes, gaps, exits, and restore classifications.

### Handoff contract

T5 terminal-ui supplies T6 QA with pure geometry/model tests, golden frames, performance hooks, and recorded client-event sequences. Any missing daemon semantic state becomes a T4 backend contract request; the UI may not compensate with a durable local approximation.

## Lane: qa

- task-id: T6-qa
- owner: qa
- depends-on: [T4-backend, T2-security, T3-devops, T5-terminal-ui]
- complexity_score: 4
- tier: powerful
- scope: Build and execute the release evidence system across domain invariants, protocol/VT fixtures, PTY/process recovery, security concurrency, CLI/TUI acceptance, persistence faults, latency, soak, packaging, and scope compliance.
- success-criteria:
  - `docs/testing/strategy.md` maps every spec and PRD criterion to a named automated test, runner, fixture, evidence path, and blocking/nightly classification.
  - Unit, property, fuzz, golden, integration, E2E, recovery, security, performance, soak, and package tests have deterministic seeds or captured reproduction data.
  - The 20-flow CLI suite passes against a real daemon on Arch and Ubuntu without internal package access.
  - VT corpus, 8-pane TUI, two-client attach/lease, event-gap, snapshot/restore, multi-repository trust, and zero-orphan acceptance tests pass exactly as specified.
  - Fault injection covers truncated frames, subscriber lag, disk-full/fsync/rename failure, corrupt SQLite/snapshot, daemon kill, PTY child races, hook timeout/output overflow, trust revoke barriers, and client reconnect.
  - Performance evidence demonstrates restore under 2 seconds and split/focus/resize p95 under 75 ms on the documented Arch reference profile.
  - Blocking 30-minute and nightly 8-hour soaks report no crash, unrecovered gap, orphan, unbounded queue/replay growth, or unexplained goroutine/file-descriptor trend.
  - Release candidate evidence includes supported architecture builds, clean package installs, checksums/provenance verification, dependency/security results, and non-goal/scope audit.
- autonomy-policy:
  - may act without asking: add tests/fixtures/harnesses, tighten deterministic assertions, quarantine nondeterministic test infrastructure with an owned remediation, and reject release when an approved gate lacks evidence.
  - requires confirmation: weakening or reclassifying a blocking gate, accepting flaky evidence, changing reference hardware/profile, or excluding a supported platform/architecture.
  - forbidden: changing production semantics solely to make a test pass, fabricating platform or soak evidence, deleting failing fixtures, or treating manual observation as a substitute for required automation.

### Work packages

1. **Q1 — Traceability and test architecture**
   - Create the final requirement-to-test ledger, fixture taxonomy, stable seed policy, evidence layout, and blocking/nightly rules from the plan's traceability baseline and completed lane receipts.
   - Reject any lane receipt whose claimed criterion lacks binary pass/fail observability or a reproducible command.
   - Run a deterministic plan check proving task IDs are unique, all dependency IDs exist, the DAG is acyclic, and every prose task-number/owner reference matches the lane declaration.
2. **Q2 — Domain/protocol/property/fuzz suites**
   - Generate valid and invalid graph command sequences and assert invariants, revision/event behavior, serialization round trips, and deterministic replay.
   - Fuzz frame decoding, JSON headers, ANSI parsing, snapshot/config input, hook schemas, and every selected agent-adapter payload with bounded resources and oversized/malformed cases.
3. **Q3 — PTY/VT/attach integration harness**
   - Build deterministic fixture programs for output timing, signals, child processes, resize reports, Unicode/ANSI sequences, stalls, and exit races.
   - Assert raw sequence continuity, golden cells, lease ownership, replay/live boundary, lag disconnect, and descendant cleanup after daemon `SIGKILL`, including double-fork, grandchildren, process-group escape attempts, and containment-feature fallback.
4. **Q4 — Persistence and recovery faults**
   - Inject write, fsync, rename, disk-full, truncated snapshot, migration, WAL, and daemon-kill failures.
   - Verify previous-known-good retention, refusal diagnostics, stable IDs, restore classes, absence of restored attachments, manifest/replay/notification checkpoint precedence, and that old generations never roll back trust epochs or grants.
5. **Q5 — Security acceptance**
   - Execute every scanner, policy, malicious fixture, and manual misuse case frozen by S6 against the integrated binaries and record tool versions plus reproducible commands.
   - Execute the complete project/grant/cwd/env/digest/epoch matrix with malicious roots, second-UID socket attempts, hostile runtime paths, symlinks, rename/byte/config/root replacement races, and deterministic same-project/cross-session launch/revoke barriers.
   - Assert absent trust and queued revocation meet 250 ms, zero child before authorization, termination timing after launch-first revoke, monotonic epochs, redacted audit, retained inactive history, and that adapter data cannot mutate the graph or launch project code outside approved commands/hooks.
6. **Q6 — CLI/TUI black-box acceptance**
   - Run all 20 CLI flows through installed binaries and versioned JSON output, plus the interactive/no-TTY/omitted-confirmation matrix for destroy, stop, lease takeover, hook approval, and trust revocation.
   - Replay Bubble Tea message/client fixtures and run real-terminal smoke tests for 8 panes, input modes, notifications, gaps, and daemon restart.
7. **Q7 — Performance and soak**
   - Build benchmark drivers for command-to-event-to-frame latency, attach/replay throughput, VT processing, snapshot restore, queue pressure, memory, goroutines, FDs, and child counts.
   - Run 30-minute blocking and 8-hour nightly 20-PTY profiles with reproducible metadata and leak trend analysis.
8. **Q8 — Packaging and release verification**
   - Install tarballs/AUR artifacts in clean Arch/Ubuntu environments, verify checksums/provenance/SBOM, run daemon/CLI smoke, and test rollback/backup instructions.
   - Audit the diff and binaries for non-goals, unsupported platform claims, cmux code/reuse, cgo, embedded network listeners, and secret-bearing fixtures.
   - Assemble Q5/Q8 evidence for a fresh security reviewer and require `docs/security/reviews/release-candidate.md` with severity, disposition, owner, retest evidence, and an explicit block while any high-severity finding remains unresolved.

### Final verification contract

T6 produces a release-candidate dossier that links every requirement to current evidence and names every residual risk. The plan is complete only when Guild verification can independently reproduce the blocking suite from the documented commands and all high-risk security, recovery, ordering, and process-lifecycle gates are green.
