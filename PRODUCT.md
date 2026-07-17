# Product

## Product name

Amux

## Product purpose

Amux is a clean-room, Go-authoritative terminal workspace runtime for Linux,
with Arch Linux as the reference distribution. It gives developers and coding
agents durable, inspectable terminal workspaces through one local daemon, a
scriptable CLI, and an interactive split-pane TUI.

## Primary users

- Linux developers who want persistent multi-pane workspaces without making a
  UI process the source of truth.
- Coding-agent operators who need stable JSON commands, deterministic errors,
  replayable output, explicit input leases, and inspectable process/git context.
- Maintainers who need auditable hook trust, reproducible state recovery, and
  verifiable Linux release artifacts.

## Product promise

Detaching a client does not stop the work. Reattaching does not guess what was
missed. Every mutation goes through one daemon authority, every attach has a
defined snapshot/replay/live boundary, and risky project hooks execute only
inside an explicit, revocable trust contract.

## Platform position

Arch Linux rolling is the reference platform. Ubuntu 24.04 LTS is the stability
lane. Linux amd64 and arm64 are supported release targets. Non-Linux builds are
useful portability seams, not equivalent runtime-support claims.

## Current scope

- Sessions, workspaces, nested split panes, and terminal surfaces.
- PTY supervision, terminal emulation, replay, ordered attach, and input leases.
- Durable SQLite state, snapshots, restore classification, notifications, and
  diagnostics.
- CLI and Bubble Tea v2 TUI over the same versioned local protocol.
- Project identity, hook approval/revocation, scope enforcement, redaction, and
  audit history.
- Arch/Ubuntu verification, systemd user service, AUR packaging, tarballs,
  checksums, SBOMs, and rollback procedures.

## Non-goals for the pre-1.0 Linux-first release

- Reproducing cmux's macOS Swift/AppKit application or depending on its code.
- Treating browser panes, mobile clients, cloud synchronization, collaboration,
  or a plugin marketplace as MVP requirements.
- Letting the CLI or TUI maintain an authoritative shadow state.
- Claiming support from cross-compilation alone.

## Design principles

1. One authority: the daemon owns durable mutation and ordering.
2. Evidence before claims: runtime support and release readiness require actual
   gates, not inferred compatibility.
3. Fail closed: ambiguous trust, ownership, replay, or recovery states produce
   explicit errors.
4. Automation and accessibility: every interactive operation has a stable CLI
   path and machine-readable output.
5. Linux first, portable at seams: isolate platform mechanisms without
   weakening the reference-platform contract.
