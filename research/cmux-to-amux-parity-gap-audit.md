# cmux to Amux parity gap audit

Research snapshot: 2026-07-17  
cmux evidence baseline: refreshed temporary clone at main commit `673ee6f65` from 2026-07-16; latest stable release `v0.64.19`, checked against the public README and releases  
Amux evidence baseline: live workspace plus Guild run `run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0`

## Bottom line

Amux is currently a strong clean-room implementation of cmux's daemon-first terminal workspace foundation, not a full cmux port. It has the hard architectural core: a Go authority, PTY supervision, a versioned local protocol, attach/replay/input leases, snapshots, notifications, project-scoped hook trust, context discovery, CLI flows, and a real Bubble Tea split-tree TUI.

Three different finish lines must not be confused:

1. **Finish the approved Linux MVP.** T5 has passed its independent round-2 review. T6 has executed once and exposed real backend, security, and release-pipeline defects; those lanes are under final rework and the complete release candidate must be requalified afterward.
2. **Reach terminal-centric cmux parity.** This adds terminal UX fidelity, the rich workspace/sidebar system, deeper agent integrations, broader automation, and desktop notification polish.
3. **Reach full cmux product parity.** This additionally requires an embedded scriptable browser, SSH remote workspaces, a native desktop shell, macOS/Windows implementations, and the broader companion/gated systems.

Calling the current project a “full port” would be inaccurate. Calling it a secure Linux-first cmux-inspired runtime is accurate.

## Current Amux position

| Area | Current state | Remaining gate |
|---|---|---|
| Architecture and contracts | Complete and independently reviewed | Preserve compatibility while adding surfaces |
| Security and hook trust | Core implemented; atomic transition rework active after T6 review | Rebuild cleanly, pass independent G-lane review, rerun frozen security gates |
| CI, packaging design, release policy | Release/TMPDIR/race fixes active after T6 review | Clean final release check, three-platform install smoke, independent review |
| Go daemon, PTY, protocol, persistence, attach, CLI | Core implemented; replay concurrency and deterministic PTY-drain fixes active | Clean focused/race/Linux gates and independent review |
| Bubble Tea TUI | Real attach lifecycle and reachable trust flows | Independently satisfied in G-lane round 2 |
| Release qualification | First full T6 execution complete; 30-minute Linux soak and three install smokes produced useful evidence | Discard stale candidate conclusions and rerun T6 against the fixed integrated tree |

## Missing before the approved Linux MVP is releasable

### 1. Close the reopened T2, T3, and T4 findings

The first integrated T6 pass did its job: it found defects that lane-local suites had missed. The final candidate still needs atomic SQLite trust transitions including audit records, one-lock replay snapshots that cannot skip concurrent output, a deterministic PTY drain barrier, truthful Linux race evidence, and a clean pinned GoReleaser/TMPDIR/package path. At the instant of this refresh, the trust-store interface rework is intentionally mid-integration, so the live tree is not a releasable baseline.

### 2. Rerun the complete T6 release-evidence lane

T6 is not optional polish. It must still deliver:

- final requirement-to-test traceability;
- property, fuzz, golden, protocol, VT, PTY, persistence, and security suites;
- all 20 CLI flows against installed binaries;
- real-terminal eight-pane TUI acceptance;
- fault injection for frames, disks, snapshots, SQLite, daemon death, hook limits, and reconnects;
- Arch reference performance evidence;
- a fresh blocking 30-minute 20-PTY soak and nightly 8-hour soak;
- clean Arch and Ubuntu package installation tests for both supported architectures;
- checksums, SBOM/provenance, rollback, security review, and non-goal audit.

Cross-compilation from macOS does not prove Linux runtime behavior. The cgroup-v2 containment, descriptor-bound launch, AUR install, D-Bus notification, PTY behavior, and soak claims need Linux execution evidence.

The first T6 pass already proved that the harnesses are substantive: it completed a 30-minute Arch-container soak with no event gaps or orphans and exercised install/daemon/CLI smoke on Arch amd64, Ubuntu amd64, and Ubuntu arm64. Those receipts belong to the superseded candidate and must be regenerated after the reopened fixes.

## Missing for terminal-centric cmux parity

### 3. Ghostty-class terminal experience

Amux deliberately chose a pure-Go VT engine and Bubble Tea renderer. That is suitable for the MVP, but cmux ships a GPU-accelerated Ghostty terminal experience. Amux still lacks:

- GPU rendering and Ghostty configuration compatibility;
- mature selection, copy semantics, terminal and directory search;
- IME and complex input polish;
- hyperlinks, file/path drops, image protocols, upload flows, and remote uploads;
- font/theme controls, scroll-speed controls, clear-scrollback and current-directory actions;
- terminal debugging, resource inspection, memory-pressure reclamation, and renderer lifecycle controls;
- the long-tail VT fidelity and performance corpus of a mature terminal engine.

This is not a small Go task. A native GPU frontend or libghostty integration would reopen the current no-cgo boundary. The safer sequence is to keep raw PTY bytes and the protocol stable, then add a replaceable renderer/client later.

### 4. Rich workspace and sidebar product model

Amux has sessions, workspaces, split panes, ordered terminal surfaces, names, descriptions, status, focus history, git/cwd/process context, and notifications. cmux goes much further:

- multiple desktop windows and window restore;
- vertical workspace tabs plus richer horizontal surface tabs;
- workspace groups, collapse, anchors, pinning, color, icon, reorder, and drag/drop;
- pane/surface move, swap, join, break, zoom, equalize, and richer resize interactions;
- linked pull-request state, listening ports, progress, logs, todos, and auto-naming;
- file explorer, global search, command palette, right sidebar modes, and task manager;
- project-defined custom commands;
- Dock layouts for dashboards, logs, watchers, and reference tools;
- data-driven custom sidebars with safe live bindings.

The current daemon graph can grow into many of these, but windows, groups, todos, dock/sidebar definitions, and richer metadata require new versioned domain entities and migration work.

### 5. Agent integration depth

Amux has a provider-neutral adapter boundary, initial Claude/Codex parsing, typed attention events, and a security-first project hook system. It does not yet match cmux's agent product:

- broad installer/adaptor catalog across Claude, Codex, OpenCode, Gemini, Grok, Pi, Amp, Cursor and others;
- durable native session identity and provider-specific resume commands;
- conversation fork flows;
- automatic session restoration with secret-scrubbed bindings;
- agent hibernation and focus-triggered resume;
- Feed for approvals, questions, plans, audit history, and jump-to-terminal;
- native Claude/Codex teams launch and pane attribution;
- a configurable vault of custom detection/resume/fork templates;
- workspace auto-naming, agent state presentation, and richer notification policy hooks.

This should be a separate “agent operations” wave after the Linux terminal runtime is stable.

### 6. Full automation and compatibility surface

Amux already has a strong versioned local protocol and CLI. Missing relative to cmux are:

- cmux CLI/config/socket compatibility, intentionally excluded from the current spec;
- the much larger command vocabulary for groups, todos, windows, dock, browser, SSH, remotes, themes, diagnostics, and custom sidebars;
- partial tmux compatibility such as capture, pipe, buffers, wait-for, swap/break/join, hooks, respawn, and display messages;
- published multi-language SDKs and a WebSocket gateway;
- deep links and desktop file/service integrations;
- authenticated remote control and multi-client network policy.

Compatibility should remain an explicit product decision. Copying cmux command shapes casually would create a long-term contract without guaranteeing behavioral parity.

### 7. Notification and attention polish

Amux has the important semantic core: persistent in-app notifications, unread routing, navigation, and an optional Linux notification adapter. It still lacks cmux's complete attention experience:

- pane rings and richer sidebar/tab badges across every surface;
- a full notification panel with policy controls and defer/mark-unread workflows;
- native tray/badge/menu integrations;
- notification hooks and policy routing;
- desktop-specific focus and jump behavior;
- polished crash and diagnostics notifications.

## Missing for full cmux product parity

### 8. Embedded, agent-scriptable browser

This is the largest missing differentiator. Amux has no browser surface. Full parity requires:

- browser surfaces inside the same pane/tab/split graph;
- omnibar, navigation, history, reload, find, downloads, uploads, file drops, and devtools;
- persistent profiles, cookies, storage, and browser import;
- accessibility/DOM snapshots and stable element references;
- click, hover, focus, type, fill, keyboard/mouse, JavaScript, wait, screenshot, cookie, storage, network, and proxy automation;
- remote-workspace network routing so remote localhost works;
- lifecycle, memory-pressure, crash recovery, security, and audit policy.

For Linux-first cross-platform parity, the earlier research still points to two defensible choices:

- **Qt 6/QML + Qt WebEngine** when same-pane browser parity is mandatory;
- **external Chrome/Chromium over CDP** for a lighter first browser beta, accepting that it is not a fully native embedded browser.

Wails alone does not solve browser parity because system webviews differ by OS and do not provide one uniform CDP-capable browser surface.

### 9. SSH and remote workspaces

Amux is explicitly single-user and local-only today. cmux includes a substantial remote subsystem:

- `ssh` workspace creation;
- remote Linux/macOS daemon deployment and version negotiation;
- persistent remote PTYs and reconnect;
- authenticated CLI relay;
- SOCKS5/CONNECT browser egress;
- scp upload and drag/drop flows;
- SSH-agent forwarding controls;
- remote resize and lifecycle coordination.

This needs a new threat model, network protocol, authentication/key lifecycle, remote upgrade story, and failure model. It should not be bolted onto the owner-only local socket.

### 10. Native desktop shell and non-Linux platforms

The current Darwin and Windows code is compile-only or placeholder infrastructure, not product support. Full cross-platform parity still needs:

- a desktop shell, multiple windows, menus, global shortcuts, tray, drag/drop, clipboard, IME, accessibility, and native focus behavior;
- macOS PTY/launch/notification/runtime implementations and signed/notarized packaging;
- Windows ConPTY, named pipes, ACL/peer validation, process-tree containment, path/config semantics, and installer/update support;
- GUI packaging for Arch and other Linux desktops, including Wayland/X11 validation;
- automatic stable/nightly update channels;
- platform-specific crash diagnostics and restore behavior.

A pure Bubble Tea client can support macOS after platform adapters are implemented, but it cannot reproduce cmux's native desktop product by itself.

### 11. Restore and operational depth

Amux's terminal snapshot and trust model is intentionally stronger and more explicit than a superficial clone, but full product parity still adds:

- browser URLs/history/profile restoration;
- multi-window and sidebar/dock restoration;
- agent-native resume/fork bindings;
- richer scrollback/search state;
- crash diagnostics UX, feedback, telemetry policy, and update recovery;
- localization and translated product/documentation surfaces.

### 12. Companion and gated cmux systems

If “full” includes every repository surface rather than stable desktop parity, Amux also lacks cloud VM management, mobile pairing/iOS clients, Agent Chat, voice/founder experiments, and freeform canvas work. Several of these are gated or explicitly experimental in cmux, so they should be tracked as optional parity tiers rather than blocking a credible desktop release.

The refreshed cmux snapshot also includes active work on password-authorized control sockets, remote tmux mirroring, a native diff sidecar/viewer, browser viewport and automation watchdogs, Iroh-backed mobile connectivity, and richer iOS terminal/theme behavior. These reinforce the need for a stable-vs-experimental parity boundary: mirroring every directory on `main` is a moving-target program, not a finite desktop port.

## Recommended implementation tools by missing subsystem

| Subsystem | Recommended tools | Go's role | Key caution |
|---|---|---|---|
| Daemon, state, protocol | Existing Go authority, SQLite, versioned local RPC/event protocol | Primary implementation | Keep it the only durable authority; do not fork state into clients |
| Portable terminal desktop | Tauri 2 + xterm.js for the fastest cross-platform shell, or Qt 6/QML for a heavier native shell | Daemon/client SDK and PTY authority | Neither automatically gives Ghostty compatibility |
| Ghostty-class terminal | libghostty behind a narrow native renderer process, or retain the Go VT engine until the API/tooling stabilizes | Own raw bytes, replay, cells, lifecycle; call renderer over IPC | Direct cgo expands packaging and crash blast radius |
| Browser beta | External Chromium plus `chromedp` or `go-rod` over CDP | Browser lifecycle, policy, automation API, audit | A controlled external browser is not same-pane native parity |
| Embedded browser parity | Qt WebEngine in a C++/QML client, driven by Amux RPC | Go remains workspace/security authority | Qt packaging size, codec licensing, sandboxing, Wayland and GPU matrix |
| SSH/remotes | `golang.org/x/crypto/ssh`, `pkg/sftp`, an SSH-stdio transport to a remote `amuxd`, SOCKS5/CONNECT proxying | Excellent fit for deployment, reconnect, relay, PTY and policy | Never expose the owner-only local socket as the network protocol |
| Linux desktop integration | D-Bus, systemd user services, XDG portals, Wayland/X11 test matrix | Good fit for services and adapters | GUI focus, clipboard, drag/drop and IME belong in the desktop client |
| macOS port | Unix PTY plus `getpeereid`, native notification/keychain helpers, signed/notarized app bundle | Core daemon is reusable | Current Darwin security/containment adapters are not product-complete |
| Windows port | ConPTY, `go-winio` named pipes, `x/sys/windows` ACLs and Job Objects | Strong for daemon and process control | Terminal input, process trees, paths and installer behavior need a separate port |
| Agent adapters | Declarative JSON manifests plus built-in Go adapters; `wazero` for untrusted extension logic | Strong fit for parsers, hooks, resume policy and audit | Provider-specific session semantics cannot be flattened into one regex |
| Custom sidebars/Dock | Versioned JSON schema + out-of-process WASI extensions + QML/web renderer | Validate, authorize and stream data | Do not let extension UI become a second authority or arbitrary project executor |
| SDK/gateway | Generated Go/TypeScript/Python SDKs; authenticated WebSocket or Connect gateway | Contract source and server | Remote authentication/capabilities must precede network exposure |

### Desktop-shell decision

- Choose **Go daemon + Tauri 2 + xterm.js + external Chromium/CDP** if the priority is the fastest credible Linux/macOS/Windows product. This maximizes portability and keeps browser automation feasible, but gives up exact Ghostty and embedded-browser parity initially.
- Choose **Go daemon + Qt 6/QML + Qt WebEngine** if browser panes inside the same split graph are non-negotiable. This is the closest technical route to full desktop parity, at the cost of a much heavier native toolchain and packaging matrix.
- Do **not** force an all-Go GUI requirement. Go is an excellent authority, process, protocol, security, SSH, and automation language; it is not currently the strongest ecosystem for a GPU terminal plus uniform embedded browser across all three desktop OSes.

## Scale of the remaining program

This is no longer one implementation wave. A credible stable desktop parity program is approximately six major streams: desktop shell/terminal, workspace product, agent operations, browser, remote, and cross-platform/release engineering. With experienced specialists these can overlap, but browser security, SSH, platform adapters, and release qualification remain serial risk gates. “Every current cmux repository surface” additionally includes moving mobile/cloud/experimental work and should be treated as a continuing product program rather than a one-time port milestone.

## Recommended roadmap after T6

| Wave | Outcome | Why this order |
|---|---|---|
| P0 | Close reopened T2/T3/T4 reviews and rerun all T6 Linux release gates | Establish a truthful, installable Arch/Ubuntu terminal runtime |
| P1 | Workspace/sidebar depth, command palette, groups, todos, richer metadata and notifications | Highest daily UX value without changing the core technology |
| P2 | Agent operations: adapters, resume/fork, Feed, teams, hibernation | Makes Amux meaningfully agent-native while reusing the secure hook/event model |
| P3 | Browser beta using external Chromium/CDP, then decide whether Qt WebEngine parity is required | Tests demand before committing to a heavy desktop/browser stack |
| P4 | SSH remote daemon and proxying | Depends on stable local contracts and a dedicated network threat model |
| P5 | Native desktop shell plus macOS support | Reuses the mature daemon/protocol while avoiding an early UI-driven architecture fork |
| P6 | Windows platform implementation | ConPTY, named pipes, ACLs, containment, packaging, and renderer work are a distinct port |
| P7 | Cloud/mobile/experimental companion systems | Valuable only after the core desktop product is proven |

## Architectural recommendation

Keep Go as the sole daemon/state/security authority. Do not rewrite the core merely to chase desktop parity. Add new clients and surface adapters around the existing versioned protocol:

```text
Go daemon authority
  -> local Unix socket / future authenticated remote protocol
  -> Bubble Tea client (Linux-first, low dependency)
  -> future desktop client (Qt/QML if browser parity is mandatory)
  -> future web/remote gateway (explicitly authenticated)
```

The first major architecture decision after the Linux release should be the browser tier. If browser parity is a core promise, select Qt WebEngine and accept its packaging/licensing footprint. If it is secondary, ship external Chromium/CDP automation first and keep the terminal product lean.

## Sources

- [cmux repository and current public feature summary](https://github.com/manaflow-ai/cmux)
- [cmux releases](https://github.com/manaflow-ai/cmux/releases)
- [Pinned cmux deep-dive source](../.guild/runs/019f6360-6c58-7d41-ba38-c4498e3c719d/research/cmux-linux-replication-deep-dive.md)
- [Approved Amux specification](../.guild/spec/amux-go-linux-runtime.md)
- [Approved Amux implementation plan](../.guild/plan/amux-go-linux-runtime.md)
- [Current T5 specialist receipt](../.guild/runs/run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0/handoffs/terminal-ui-T5-terminal-ui.md)
