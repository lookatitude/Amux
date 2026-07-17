# cmux deep dive and Linux-first replication architecture

Research snapshot: 2026-07-15  
Repository: `manaflow-ai/cmux` at commit [`3822f1dd`](https://github.com/manaflow-ai/cmux/commit/3822f1dd475f0c5ddcf961df9f17308c3066ffa1)  
Local research clone: `/tmp/cmux-research.Oqg7m3/cmux`

## TL;DR

- **[Well-established] cmux is a programmable workspace runtime, not merely a terminal.** Its stable product combines Ghostty terminals, a hierarchical window/workspace/pane/tab model, an embedded and agent-scriptable browser, agent lifecycle hooks, notifications, session restoration, SSH workspaces, custom sidebars/Dock, and a large CLI/socket/event API. The public feature summary is only the top of a much larger implementation surface ([README](https://github.com/manaflow-ai/cmux/blob/3822f1dd475f0c5ddcf961df9f17308c3066ffa1/README.md#features), [CLI contract](https://github.com/manaflow-ai/cmux/blob/3822f1dd475f0c5ddcf961df9f17308c3066ffa1/docs/cli-contract.md)).
- **[Well-established] Porting the Swift/AppKit app is the wrong Linux strategy.** The macOS UI is deeply coupled to AppKit, SwiftUI, WKWebView, Sparkle, Apple Services, menu-bar/Dock APIs, and Apple-specific window/focus behavior. The reusable product concepts are the state model, protocols, hooks, and remote daemon—not the UI code.
- **[Well-established] The repo already contains the best Linux seed: `cmux-tui`.** It is a Rust multiplexer with workspaces/screens/split panes/tabs, `portable-pty`, Ghostty's VT engine, a JSONL control socket, attach clients, WebSocket and multi-language SDKs, and Chrome/CDP browser panes. Linux and macOS run the full test matrix; release workflows build Linux x86_64 and aarch64; Windows is explicitly experimental ([TUI README](https://github.com/manaflow-ai/cmux/blob/3822f1dd475f0c5ddcf961df9f17308c3066ffa1/cmux-tui/README.md), [CI](https://github.com/manaflow-ai/cmux/blob/3822f1dd475f0c5ddcf961df9f17308c3066ffa1/.github/workflows/cmux-tui.yml), [release build](https://github.com/manaflow-ai/cmux/blob/3822f1dd475f0c5ddcf961df9f17308c3066ffa1/.github/workflows/cmux-tui-build-package.yml)).
- **[Recommendation] Build a Rust-first core around the `cmux-tui-core` architecture and preserve its protocol boundary.** For a terminal-centric MVP, use Tauri 2 + React/Solid + xterm.js. If a same-pane, production-grade, automatable browser is non-negotiable, use Qt 6/QML + Qt WebEngine for the desktop shell instead. Do not mix both shells in v1.
- **[Recommendation] Target Arch first as a native package, then portable binaries.** Ship an AUR/PKGBUILD package plus tarball; test Wayland and X11 separately. Pin Zig 0.15.2 initially because the repository build pins it while Arch currently ships Zig 0.16.0. Add macOS after Linux architecture stabilizes; treat Windows as phase 3 because ConPTY, named-pipe/socket semantics, WebView/Chromium packaging, and shell behavior are separate product work.

## Scope and methodology

This brief maps the repository's user-visible and platform capabilities, identifies which parts are shipped, gated, partial, or separate experiments, and recommends a Linux-first architecture that can later support macOS and Windows. It does not estimate team size or delivery dates because those depend on the desired parity tier and licensing strategy.

Evidence came from the cloned source tree, public docs, build workflows, source-level feature gates, package manifests, and official documentation for Ghostty, Tauri, Qt, and Arch Linux. A feature is marked “shipped” only when it is in the README/CLI contract or has a concrete implementation path without an off-by-default release gate. Design documents and prototype directories are not treated as shipped merely because code exists.

## What cmux actually is

The architectural center is an authoritative workspace graph:

```text
application
  -> windows
    -> workspaces (vertical sidebar, metadata, groups)
      -> split-tree panes
        -> surfaces/tabs (terminal, browser, viewers)
```

Around that graph are five systems:

1. A terminal runtime based on `libghostty`.
2. A browser runtime based on `WKWebView` in the macOS app.
3. A control plane (CLI, local authenticated socket, JSON-RPC-style v2 methods, event stream).
4. An agent-awareness layer (hooks, session identity, notifications, restore, hibernation, Feed).
5. Remote and companion layers (SSH daemon/proxy, cloud VMs, iOS pairing/transport).

This separation is the key replication lesson. The pane UI is replaceable; stable IDs, lifecycle transitions, events, and capability boundaries are the durable product.

## Feature map

### 1. Terminal and process runtime — shipped

- GPU-accelerated Ghostty rendering and Ghostty configuration compatibility: fonts, themes, colors, terminal behavior.
- PTY-backed interactive shells, multiple terminal surfaces, scrollback, selection, copy/paste, IME, links, file/path drops, terminal search, directory search, clear scrollback, font size, configurable scroll speed, and current-directory actions.
- Per-workspace working directory and environment, with inheritance into future panes and restored sessions; protected `CMUX_*` identity variables.
- Surface health/debug commands, process/resource inspection (`top`), memory-pressure reclamation, and renderer realization controls.
- Custom terminal upload commands and remote image/file upload behavior.

Linux implication: the terminal engine is portable; AppKit hosting is not. Ghostty officially describes `libghostty` as a cross-platform C-ABI library used by both its Swift/AppKit macOS frontend and Zig/GTK4 Linux frontend ([Ghostty architecture](https://ghostty.org/docs/about)). For initial reuse, `cmux-tui` narrows this further to `libghostty-vt` plus a frontend renderer.

### 2. Window, workspace, pane, and tab management — shipped

- Multiple windows, vertical workspace tabs, horizontal surface tabs, horizontal/vertical split panes, directional focus, resizing/equalizing, drag/reorder/move, pinning, names, descriptions, colors, and recent-focus behavior.
- Workspace groups with collapsible sections, anchor workspaces, pinning, group-aware creation, drag order, persistence, and CLI operations ([workspace groups](https://github.com/manaflow-ai/cmux/blob/3822f1dd475f0c5ddcf961df9f17308c3066ffa1/docs/workspace-groups.md)).
- Saved layouts/session snapshots and restoration of window/workspace/pane structure, working directories, scrollback best-effort, and browser history.
- Workspace todos/checklists with lifecycle status lanes and CLI mutation.
- A right-side Dock that reuses the terminal/browser split system for project dashboards, TUIs, logs, test watchers, and reference pages; project configs have a trust gate ([Dock](https://github.com/manaflow-ai/cmux/blob/3822f1dd475f0c5ddcf961df9f17308c3066ffa1/docs/dock.md)).
- File explorer, command palette, notifications panel, right-sidebar modes, status/progress/log metadata, and custom command actions.

### 3. Context-rich sidebar — shipped, with one experiment

- Workspace cwd, git branch/status, linked PR information, listening ports, recent notification text, unread state, agent state, description, custom color/icon, progress/status/logs, and todos.
- Workspace auto-naming through supported agent CLIs, with manual-name protection and throttling.
- Data-driven custom sidebars authored in a constrained SwiftUI-like DSL, with live workspace/notification data, commands, drag reorder, and in-process or isolated rendering. Two-way binding and arbitrary Swift are explicitly not supported ([custom sidebars](https://github.com/manaflow-ai/cmux/blob/3822f1dd475f0c5ddcf961df9f17308c3066ffa1/docs/custom-sidebars.md)).
- The workspace agent spinner is an off-by-default experiment; do not count it as stable parity ([feature flags](https://github.com/manaflow-ai/cmux/blob/3822f1dd475f0c5ddcf961df9f17308c3066ffa1/Sources/FeatureFlags.swift)).

### 4. Notifications and attention routing — shipped

- Terminal OSC notifications (9/99/777) and `cmux notify` hooks.
- Blue pane rings, workspace/tab unread state, notification list/panel, latest-unread navigation, mark read/unread, clear/dismiss, and open/focus routing.
- macOS native banners, Dock icon count, main menu and menu-bar-extra notification controls.
- Structured notification metadata in the local event stream and persistent recent logs.

Linux replication must split the semantic notification store from delivery. Use a portable in-app store and event stream; add `notify-rust`/D-Bus and tray badges as adapters. Wayland compositors vary, so native notifications cannot be the sole attention surface.

### 5. Agent integrations — mostly shipped

- Hook installers and lifecycle/session restoration for Claude Code, Codex, Grok, OpenCode, Pi, OMP, Campfire, Amp, Cursor CLI, Gemini, Kiro, Kimi, Rovo Dev, Copilot, CodeBuddy, Factory, Qoder, and related adapters; exact capabilities differ by agent ([agent hooks](https://github.com/manaflow-ai/cmux/blob/3822f1dd475f0c5ddcf961df9f17308c3066ffa1/docs/agent-hooks.md)).
- Native resume identity attached to terminal surfaces, trusted custom resume commands, fork conversation support where providers expose it, secret scrubbing, and automatic session restoration.
- Agent Hibernation: opt-in resource reclamation after strict idle/visibility/session gates and a confirmation period, followed by native resume on focus.
- `claude-teams`, `codex-teams`, and integrations for OMO/OMX/OMC-style orchestrators.
- Feed: keyboard-first approval/question/plan stream with audit history and jump-back-to-terminal behavior.
- Vault-style registration for custom agent detection and resume/fork templates.

The web-based Agent Chat is **not stable product parity**. Its README calls it an MVP; the release UI gate is off by default and the document lists missing persistence, native permission routing, terminal/worktree attachment, and terminal escape hatch ([Agent Chat](https://github.com/manaflow-ai/cmux/blob/3822f1dd475f0c5ddcf961df9f17308c3066ffa1/agent-chat/README.md), [feature gate](https://github.com/manaflow-ai/cmux/blob/3822f1dd475f0c5ddcf961df9f17308c3066ffa1/Sources/FeatureFlags.swift)).

### 6. Browser — shipped on macOS; alternate implementation in TUI

- Browser surfaces can sit beside terminals, share tab/split behavior, provide an omnibar, navigation/history/reload/find, downloads/uploads, file drops, devtools, profiles/cookies, and browser import from many installed browsers.
- Agent-browser-style CLI automation includes accessibility/DOM snapshots and refs, click/double-click/hover/focus, type/fill, keyboard/mouse, JS evaluation, waits, screenshots, cookies/storage, network and proxy controls, and explicit unsupported mappings where WKWebView lacks CDP behavior ([browser port contract](https://github.com/manaflow-ai/cmux/blob/3822f1dd475f0c5ddcf961df9f17308c3066ffa1/docs/agent-browser-port-spec.md)).
- Remote SSH workspaces automatically proxy browser traffic through the remote host.

`cmux-tui` uses a different, portable browser design: it launches or attaches to Chrome/Chromium over CDP, captures `Page.screencastFrame`, draws frames using Kitty graphics, and forwards input over CDP. Profiles persist per session; Linux Chrome discovery is implemented. This is portable and automatable but visually depends on Kitty graphics and is not equivalent to a native embedded browser ([TUI browser panes](https://github.com/manaflow-ai/cmux/blob/3822f1dd475f0c5ddcf961df9f17308c3066ffa1/cmux-tui/docs/browser-panes.md)).

### 7. Programmability — shipped and unusually extensive

- CLI operations span windows, displays, workspaces/groups/todos, panes/surfaces, terminal input/screen reads, browser automation, notifications, sidebars, themes, SSH, cloud VMs, remotes, hooks, auth, and diagnostics.
- A local authenticated socket exposes legacy text commands and structured v2 requests. Stable refs and UUIDs identify resources.
- Reconnectable event stream with sequence numbers, boot IDs, bounded replay, heartbeats, filters, durable JSONL logging, slow-consumer handling, and snapshot refresh on gaps ([events](https://github.com/manaflow-ai/cmux/blob/3822f1dd475f0c5ddcf961df9f17308c3066ffa1/docs/events.md)).
- Partial tmux compatibility: pane capture/resize/pipe, wait-for, swap/break/join, window navigation, hooks, buffers, respawn, and display messages; some commands remain placeholders.
- AppleScript, deep links, Finder/Services actions, project custom commands, and configuration reload.

### 8. SSH, remote execution, cloud, and mobile — mixed maturity

- `cmux ssh` is substantial: remote workspaces, a versioned remote daemon for Linux/macOS arm64/amd64, authenticated CLI relay, persistent PTY work, reconnect/disconnect, SOCKS5/CONNECT browser egress over daemon RPC, scp uploads, SSH-agent forwarding controls, and tmux-like resize coordination ([remote spec](https://github.com/manaflow-ai/cmux/blob/3822f1dd475f0c5ddcf961df9f17308c3066ffa1/docs/remote-daemon-spec.md)). Detachable persistent SSH sessions remain marked in progress.
- Cloud VM commands/backend exist, but release UI defaults off; treat this as beta/gated rather than core parity.
- Mobile Connect is visible by default. The iOS/iPadOS app has auth, pairing, device/workspace lists, terminal selection/input, and evolving Iroh/Tailscale transport. It is a companion product, not a portable desktop layer ([iOS status](https://github.com/manaflow-ai/cmux/blob/3822f1dd475f0c5ddcf961df9f17308c3066ffa1/ios/README.md)).

### 9. Customization, distribution, and operations — shipped on macOS

- `cmux.json`/JSONC settings, Ghostty config reuse, customizable shortcuts, themes, actions, layouts, env, upload commands, Dock, sidebars, agent behavior, and restore controls.
- English/Japanese string catalogs plus many translated READMEs.
- Sparkle stable/nightly update channels, Homebrew cask, DMG signing/notarization, Sentry/PostHog, feedback collection, extensive unit/UI/integration/VM tests.
- Freeform canvas code/design exists, but it is DEBUG-gated in routing; classify it as experimental.

## The existing portable subsystem: `cmux-tui`

`cmux-tui` should be evaluated as a product seed, not a curiosity.

Implemented today:

- Rust 2024 workspace, MSRV 1.88, MIT metadata for the TUI workspace.
- `portable-pty`, `crossterm`, Ratatui, `libghostty-vt`, Serde/JSON, Unix-domain sockets with a Windows compatibility implementation, and raw CDP.
- Authoritative tree: session → workspaces → screens → split-tree panes → tabs/surfaces.
- PTY and browser surfaces, resize/focus/move/close operations, attach/replay, subscriptions, browser frames, and headless mode.
- Protocol-v6 SDKs/bindings for Rust, TypeScript, Python, Go, and Java.
- A proof web frontend using xterm.js and WebSockets; it currently renders only the active pane rather than the complete split tree ([web frontend](https://github.com/manaflow-ai/cmux/blob/3822f1dd475f0c5ddcf961df9f17308c3066ffa1/cmux-tui/frontends/web/README.md)).
- Full Linux and macOS CI/smoke tests; Linux x86_64/aarch64 release artifacts; experimental Windows GNU build.

Important limitations:

- It does not have the macOS app's full sidebar metadata, notifications UX, agent hook catalog, Dock, browser-import UI, session persistence depth, remote daemon integration, or native desktop polish.
- Browser rendering via CDP screenshots/Kitty graphics is clever but not a replacement for a desktop browser widget.
- Windows builds are `continue-on-error`; support is not production-grade.
- Licensing needs clarification before reuse: the repository root is GPL-3.0-or-later/commercial while the TUI Cargo workspace declares MIT. Obtain an explicit answer from the maintainers; do not assume the subdirectory metadata overrides the repository license.

## Architecture options

| Option | Strengths | Weaknesses | Fit |
|---|---|---|---|
| Rust core + Tauri 2 + xterm.js | Fastest cross-platform GUI path; small shell; Rust backend; good settings/sidebar/palette productivity; official Arch prerequisites and AUR/AppImage support | Terminal rendering is web-based; Linux uses WebKitGTK while macOS/Windows use different engines; arbitrary embedded-browser automation is not uniformly CDP-capable; multiple native webviews and focus can be tricky | **Best terminal-centric MVP** |
| Rust/C++ core + Qt 6/QML + Qt WebEngine | One mature UI/browser stack on Linux/macOS/Windows; Chromium engine; strong profiles, cookies, devtools, accessibility, GPU rendering, windows/docking | Large installed footprint; Qt/Chromium packaging and LGPL obligations; Rust bridge complexity; custom native terminal surface still required | **Best if browser parity is mandatory** |
| Electron + React + xterm.js | Fastest UI and browser tooling; Chromium/CDP everywhere; huge ecosystem | Highest RAM/disk cost; duplicates Chromium; directly conflicts with cmux's performance motivation | Good prototype, poor final fit |
| Pure Rust winit/wgpu/Slint/egui + libghostty + CEF | Maximum rendering/control and potentially excellent performance | You own accessibility, IME, menus, drag/drop, web embedding, focus, packaging, and platform bugs; longest route | Only for a well-funded systems team |
| GTK4/libadwaita + libghostty | Most native Arch/Linux result; follows Ghostty's Linux architecture | macOS/Windows require separate frontends; WebKitGTK automation and browser parity remain hard | Best Linux-only product, not best cross-platform product |
| Go core + Bubble Tea/Wails + xterm.js | Excellent concurrency and process orchestration; simple services and cross-compilation; strong TUI and CDP libraries; productive web frontend bridge | Cannot reuse the Rust `cmux-tui-core`; libghostty requires cgo/C ABI work; Wails has the same system-webview browser mismatch as Tauri; weaker native GPU terminal ecosystem | **Strong clean-room alternative, second choice overall** |

Tauri is built from Rust plus HTML in OS webviews and officially supports macOS, Windows, and Linux; its Arch prerequisites include `webkit2gtk-4.1`, `base-devel`, OpenSSL, appindicator, librsvg, and xdotool ([Tauri architecture](https://v2.tauri.app/concept/architecture/), [Arch prerequisites](https://v2.tauri.app/start/prerequisites/)). Qt supports all three desktop OS families, and Qt WebEngine embeds Chromium, but distribution must account for Qt and Chromium licensing and a much larger runtime ([Qt platforms](https://doc.qt.io/qt-6/supported-platforms.html), [Qt WebEngine](https://doc.qt.io/qt-6/qtwebengine-overview.html), [licensing](https://doc.qt.io/qt-6/qtwebengine-licensing.html)). Arch's current `qt6-webengine` installed size is about 282 MiB ([Arch package](https://archlinux.org/packages/extra/x86_64/qt6-webengine/)).

## Recommended stack

### Primary recommendation: terminal-centric, Linux-first

Use:

- **Core language:** Rust.
- **Starting architecture:** extract/fork the ideas and, if licensing permits, code from `cmux-tui-core` and `ghostty-vt` bindings.
- **PTY:** `portable-pty` initially; retain a thin platform adapter for Unix PTYs and Windows ConPTY edge cases.
- **Terminal frontend:** xterm.js in v1 for delivery speed; retain VT bytes/replay and cell geometry in the backend so a native libghostty renderer can replace it later without changing the protocol.
- **Desktop shell:** Tauri 2 with a small React or Solid frontend. Solid is attractive for fine-grained updates; React has the larger hiring/testing ecosystem. Either is adequate if rows consume immutable snapshots rather than a global reactive store.
- **State model:** normalized Rust entities with stable opaque IDs and an explicit split tree. All UI, CLI, hooks, and restore paths call the same commands.
- **IPC/API:** JSON-RPC-like requests over Unix sockets on Linux/macOS and named pipes on Windows; WebSocket gateway for web/remote clients; sequence-numbered event stream with snapshot-on-gap semantics.
- **Persistence:** atomic versioned JSON for layout snapshots plus SQLite/WAL for notifications, agent sessions, event cursors, and audit history.
- **Browser MVP:** launched Chromium via raw CDP, using the existing `cmux-tui-cdp` design. Open the real headful window or render screencast frames as an explicitly labeled beta. Do not promise WKWebView-level embedded parity.
- **Agent integration:** executable hook adapters writing structured lifecycle events to the socket. Keep provider-specific parsing outside the core. Add ACP as one adapter family, but preserve native adapters where they expose richer approvals/resume data.
- **SSH:** invoke the user's OpenSSH binary so ssh_config, ProxyJump, keys, and agents behave normally; upload a versioned Rust remote daemon for persistent PTYs, proxy streams, and CLI relay.
- **Git/process/ports:** prefer invoking `git`/`gh` for behavior parity and auth; use `sysinfo` plus `/proc`/platform adapters for process and listening-port metadata.
- **Notifications:** in-app authoritative store; `notify-rust`/D-Bus and Tauri notification/tray plugins as optional delivery adapters.
- **Packaging:** `cargo-dist` or Tauri bundler for tarballs/AppImage/deb/rpm; hand-maintained PKGBUILD/AUR for Arch; Homebrew and MSI/NSIS later.

Why this wins: it delivers the differentiating agent/workspace control plane quickly, keeps Linux first-class, and avoids locking the core to any GUI toolkit. It also matches the repo's own emerging portable architecture.

## Go implementation evaluation

Go is fully viable for a cmux-like product, but it changes the best reuse strategy.

### Where Go is excellent

- **Mux/control plane:** goroutines and channels fit PTY readers, event subscribers, hook ingestion, process monitors, browser workers, reconnect loops, and bounded queues naturally.
- **TUI:** Bubble Tea uses an Elm-style model/update/view architecture and ships keyboard, mouse, clipboard, and a high-performance cell renderer. It is a strong fit for a first Linux frontend ([Bubble Tea](https://charm.land/bubbletea)).
- **Desktop shell:** Wails combines a Go backend with React/Svelte/Vue-style frontends, supports Linux amd64/arm64, macOS, and Windows, and produces Go↔TypeScript bindings ([Wails](https://wails.io/docs/introduction/), [platform requirements](https://wails.io/docs/gettingstarted/installation/)). Architecturally it is the Go analogue of Tauri.
- **Browser automation:** `chromedp` implements CDP in Go without an external Go runtime dependency and is mature enough for target management, DOM operations, screenshots, network control, and event subscriptions ([chromedp](https://github.com/chromedp/chromedp)).
- **Remote/SSH:** `golang.org/x/crypto/ssh`, `creack/pty` on Unix, Windows ConPTY wrappers, `os/exec`, and the standard networking stack are well suited to a remote daemon and CLI relay. A static-ish Go remote daemon is easier to deploy than a GUI runtime.
- **Distribution:** the headless/TUI binary can be a simple Arch package with no Rust/Zig toolchain. Go's build and profiling tools are excellent for a long-running daemon.

### Where Go is weaker

- The repository's most reusable portable code—`cmux-tui-core`, its Ghostty VT wrapper, protocol implementation, tests, and SDK contract—is Rust. Choosing Go means a clean-room port or running a Rust sidecar; a Go/Rust split at the mux boundary adds complexity without a clear benefit.
- There is no Go-native equivalent to the full `libghostty` renderer. Using Ghostty means cgo/C ABI integration and platform-specific graphics hosting. That harms cross-compilation and makes Windows packaging harder. Using xterm.js avoids that issue but gives up native GPU terminal rendering.
- Wails uses platform webviews rather than one consistent Chromium. Windows uses WebView2; Linux uses WebKitGTK. Consequently, Wails does not solve the embedded, uniformly automatable browser problem any more than Tauri does.
- Fyne/Gio can build cross-platform native Go GUIs, but neither supplies the browser, terminal, docking, accessibility, and devtools stack needed here. They would force substantial custom widget and renderer work.

### Recommended Go stack

If Go is selected, use a coherent Go architecture rather than a Go wrapper around the Rust mux:

- **Core:** Go modules with immutable command inputs, a single authoritative tree, opaque IDs, and serialized mutation through an event-loop goroutine per session.
- **PTY:** `github.com/creack/pty` on Unix and a ConPTY adapter on Windows; hide both behind one interface.
- **Terminal state:** either embed `libghostty-vt` through a very small cgo wrapper, or start with frontend xterm.js and store raw VT/replay streams. The latter is the safer cross-platform v1.
- **TUI:** Bubble Tea + Lip Gloss + Bubbles.
- **Desktop:** Wails v3 + React or Svelte + xterm.js. On Arch, package GTK/WebKitGTK dependencies explicitly and test Wayland/X11 separately.
- **IPC:** JSONL/JSON-RPC over Unix sockets; named pipes on Windows; WebSocket gateway using `nhooyr.io/websocket`/its maintained successor or Gorilla WebSocket.
- **Browser:** `chromedp` with a dedicated Chromium profile per session. Use real headful Chrome initially; label screencast embedding experimental.
- **CLI/config:** Cobra for commands, `encoding/json` plus JSONC preprocessing, and generated shell completions.
- **Persistence:** atomic JSON snapshots plus SQLite through `modernc.org/sqlite` for a cgo-free build, or `mattn/go-sqlite3` if cgo is already accepted.
- **Notifications/keyring:** D-Bus/portal adapters on Linux, native platform adapters elsewhere, and `99designs/keyring` or OS-specific secret stores.
- **Observability:** `slog`, OpenTelemetry, `pprof`, and bounded local event logs.

### Go verdict

**Choose Go if** the project is a clean-room implementation, the team is substantially stronger in Go, the first product is a headless/TUI multiplexer plus daemon, or rapid SSH/CDP/control-plane work matters more than reusing cmux's portable subsystem.

**Choose Rust if** the plan is to reuse or closely track `cmux-tui`, embed Ghostty at a low level, build a native renderer later, or minimize cross-language boundaries. For this particular repository and goal, Rust remains the stronger primary recommendation because the portable reference implementation already exists in Rust.

**Do not choose a Go core plus a Rust `cmux-tui` core.** Pick one authority. A sidecar is appropriate for optional browser or cloud services, not for splitting ownership of workspaces, panes, PTYs, and session state.

### Browser-first variant

If the product promise includes “a real embedded browser beside each terminal, with profiles, cookies, devtools, proxying, and reliable automation,” choose **Qt 6/QML + Qt WebEngine** instead of Tauri. Keep the same Rust daemon and protocol, but bridge the shell with CXX-Qt or a narrow C ABI. Use one `QWebEngineProfile` per isolation scope and Qt's Chromium remote-debugging/devtools facilities. Accept the footprint and licensing work as the cost of browser parity.

Do not build browser parity on WebKitGTK and assume it will match WKWebView/WebView2. The engines, devtools protocols, profile storage, proxy APIs, and accessibility behavior differ. cmux itself already maintains unsupported mappings even within WKWebView.

## Arch Linux plan

1. Make Arch/Wayland the developer reference: GNOME and KDE plus Sway or Hyprland; also test X11/XWayland.
2. Publish a prebuilt `x86_64-unknown-linux-gnu` tarball and an AUR binary package first; add a source PKGBUILD after toolchains stabilize.
3. Pin Zig 0.15.2 for the Ghostty VT build. The repo pins 0.15.2, while Arch currently ships Zig 0.16.0 ([Arch Zig](https://archlinux.org/packages/extra/x86_64/zig/)). Rust is not a blocker: the TUI MSRV is 1.88 and Arch currently ships a newer toolchain ([Arch Rust](https://archlinux.org/packages/extra/x86_64/rust/)).
4. If using Tauri, depend on Arch's `webkit2gtk-4.1` stack and test GPU/IME/clipboard behavior under both Wayland and X11. If using Qt, depend on `qt6-base`/`qt6-webengine`; do not bundle a second Qt unless producing a fully self-contained commercial distribution.
5. Integrate XDG paths, desktop files, MIME/deep-link registration, D-Bus notifications, Secret Service/keyring, xdg-desktop-portal file dialogs, and systemd user units where useful.
6. Avoid global-shortcut assumptions. Wayland requires portal/compositor cooperation; all essential actions must remain accessible in-app and from the CLI.

## Delivery sequence

### Phase 1: durable Linux core

- Workspace/split/tab model, PTYs, terminal attach/replay, config, CLI/socket/events, snapshots, notifications, hooks for 2–3 agents, git/cwd metadata.
- TUI frontend first, then the desktop shell against the same protocol.
- Arch package and Linux CI across x86_64/aarch64.

### Phase 2: cmux differentiators

- Agent resume/fork, attention rings, notification inbox, todos, auto-naming, Dock/sidecars, custom commands, session restore, tmux compatibility subset.
- SSH workspace plus remote daemon and browser proxy.
- Chromium/CDP browser beta and automation subset.

### Phase 3: platform and browser depth

- macOS shell/package, then Windows ConPTY/named-pipe/package work.
- Embedded browser only after deciding between Qt WebEngine and a deliberately less uniform native-webview approach.
- Cloud/mobile companion only after local/SSH reliability is strong.

## Risks and decisions to settle before implementation

1. **License:** confirm whether `cmux-tui` is independently MIT or covered by the root GPL/commercial license.
2. **Browser parity tier:** external/headful Chrome, CDP screencast, system webview, or embedded Chromium. This single decision determines Tauri vs Qt.
3. **Terminal renderer tier:** xterm.js speed-to-market vs native libghostty performance. Preserve protocol independence either way.
4. **Windows promise:** experimental buildability is not product support. Define whether Windows is “best effort” or parity.
5. **Agent data security:** session IDs, prompts, hooks, environment variables, and browser cookies require threat modeling, redaction, and explicit trust gates.
6. **Feature boundary:** do not attempt cloud VMs, iOS, freeform canvas, Agent Chat, and every hook in v1. They are separate products or gated experiments even in cmux.

## Contested and open questions

- **[Contested] Tauri vs Qt:** Tauri is the better product-development tool for a terminal-first app; Qt is the better browser-embedding tool. There is no honest universal winner without fixing the browser parity requirement.
- **[Emerging] `libghostty` as an embedding SDK:** Ghostty's architecture is explicitly designed for alternative frontends, but API stability and downstream integration burden still need a proof-of-concept on Linux, macOS, and Windows.
- **[Emerging] Windows:** the TUI contains Windows platform code and release jobs, but CI treats the Windows artifact as experimental and non-blocking.
- **[Speculative] Reusing the existing TUI code commercially:** Cargo metadata says MIT while the repository root says GPL/commercial. Only an explicit maintainer statement or legal review can resolve that contradiction.
- Which cmux parity target matters most: the terminal/workspace/agent loop, the embedded browser, or the remote/mobile ecosystem?
- Is a Chromium dependency acceptable for consistent automation, footprint notwithstanding?
- Should the project be a clean-room implementation of concepts or a fork? Licensing and desired compatibility determine the answer.

## Sources

1. cmux README · Manaflow · repository snapshot 2026-07-14 · https://github.com/manaflow-ai/cmux/blob/3822f1dd475f0c5ddcf961df9f17308c3066ffa1/README.md · primary.
2. cmux CLI contract, events, agent hooks, Dock, workspace groups, remote daemon, browser port spec · Manaflow · 2026 snapshot · repository links above · primary.
3. cmux-tui README, concepts, browser panes, protocol, Cargo manifests, platform code, CI and release workflows · Manaflow · 2026 snapshot · repository links above · primary.
4. About Ghostty / libghostty architecture · Ghostty project · accessed 2026-07-15 · https://ghostty.org/docs/about · primary.
5. Tauri architecture and prerequisites · Tauri project · updated 2026-06-30 / accessed 2026-07-15 · https://v2.tauri.app/concept/architecture/ and https://v2.tauri.app/start/prerequisites/ · primary.
6. Qt supported platforms, Qt WebEngine overview and licensing · Qt project · Qt 6.11 docs, accessed 2026-07-15 · https://doc.qt.io/qt-6/supported-platforms.html, https://doc.qt.io/qt-6/qtwebengine-overview.html, https://doc.qt.io/qt-6/qtwebengine-licensing.html · primary.
7. Arch Linux package database: Zig, Rust, Qt WebEngine · Arch Linux · accessed 2026-07-15 · package links above · primary distribution metadata.

## Confidence summary

- Repository feature and architecture claims: **high**, based on the pinned source and contracts.
- Linux/macOS TUI build support: **high**, based on blocking CI and release matrices.
- Windows readiness: **medium for buildability, low for production parity**, because its release job is experimental/non-blocking.
- Tauri recommendation: **high for terminal-centric scope, medium for browser-heavy scope**.
- Qt recommendation: **high for consistent embedded-browser scope, medium for native terminal integration effort**.
- Arch packaging details: **high for current package versions, medium over time** because Arch is rolling release.
