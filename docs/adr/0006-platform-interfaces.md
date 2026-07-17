# ADR-0006 — Platform interfaces (Linux-first, portable behind seams)

- Status: Accepted
- Date: 2026-07-15
- Amended: 2026-07-15 (G-lane round 1 — materialized the `PTY`, `LocalTransport`, and `Notifier` interfaces in `internal/platform/platform.go` and froze their signatures below; no semantic or platform-support change)
- Deciders: architect (T1)
- Significance: high
- Enforced by: `internal/platform` (macOS + Linux build), `GOOS=linux go build ./...`, `go test ./internal/platform/` (including the seam-freeze tests in `seam_test.go`, which pin the full interface set's method names and signatures); deferred Linux harnesses in `spikes/containment`, `spikes/launch`

## Context

Amux is Linux-first but must not bind the domain to OS specifics. Every
OS-touching capability lives behind a narrow interface in `internal/platform`, so
Linux is the only implemented platform in the MVP and Darwin/Windows are
compile-time placeholders that fail closed. This ADR freezes those interfaces and
records which behaviors are proven on the author host versus deferred to a Linux
host.

## Decision drivers

- "Linux is the product, not a port"; "portability lives behind narrow
  interfaces" (PRD principles 7–8).
- Never claim a capability the platform doesn't implement — fail closed.
- Keep OS types (syscall structs) out of the domain.

## Decision: the interface set (`internal/platform/platform.go`)

- **PTY** — narrow spawn/resize/input/output/signal/reap surface, backed by
  `github.com/creack/pty`. Proven on the author host: `spikes/pty` opens a real
  PTY, round-trips output, and resizes (runs on Darwin and Linux). Frozen as
  `PTY.Start(PTYSpec) (PTYHandle, error)`; `PTYHandle` is `io.Reader`/`io.Writer`
  (output/input) plus `Resize(PTYSize)`, `Signal(os.Signal)`,
  `Wait() (PTYExit, error)` (reap), `MasterFD() uintptr` (feeds
  `ProcessInspector.ForegroundPID`), and `Close()`. The T4 implementation owns
  the mechanism; nothing above the seam sees creack/pty or termios types.
- **FilesystemIdentity** — `Identify(path)` → canonical realpath + `{dev, ino}`.
  Implemented for Darwin and Linux via `stat` (`fsid_unix.go`); Windows is a
  fail-closed placeholder. Underpins project trust.
- **Project identity** — `ComputeProjectKey` = hex SHA-256 of length-prefixed
  (`"amux-project-v1"`, realpath, dev, ino). Proven on the author host
  (`identity_test.go`): stable per root, identical through a symlink
  (canonicalization), distinct across roots, and fails closed on a missing path.
- **Clock** — injectable `NowUnixMilli` / `MonotonicNanos`; `systemClock` for
  production and `FakeClock` for deterministic deadline/heartbeat/250 ms-gate
  tests (`clock_test.go`), including "wall regression does not move monotonic
  backward".
- **PeerCredentials** — `PeerUID(fd)` via `SO_PEERCRED` on Linux
  (`peercred_linux.go`); fail-closed elsewhere. Mandatory for the owner-only
  socket.
- **Containment** — daemon-death descendant containment (below).
- **DescriptorLaunch** — race-safe descriptor-bound executable launch (below).
- **ProcessInspector** — foreground PID / liveness for context collectors
  (Linux `/proc`; interface frozen, Linux impl is a T4/context concern).
- **LocalTransport** — owner-only control-socket lifecycle, frozen as
  `LocalTransport.Listen/Dial(TransportSpec)` returning `LocalListener` /
  `LocalConn`. `Listen` validates every runtime-path component (no symlink
  traversal, expected owner, safe mode) and removes a stale socket only after
  proving ownership/type and the absence of a live owner; `Dial` fails closed
  on an owner mismatch. `LocalConn` is `io.Reader`/`io.Writer` plus
  `Control(func(fd uintptr) error)`, which exposes the raw descriptor solely
  for the mandatory `PeerCredentials.PeerUID` check before the first protocol
  byte; framing/negotiation stay in the protocol layer (ADR-0003). Mechanism
  lands in T4 `internal/transport/local`.
- **Notifier** — best-effort desktop delivery, frozen as
  `Notifier.Notify(Notification) error` (`Title`, `Body`, `NotifyUrgency`
  hint). Errors are advisory: delivery failure never creates, removes, or
  marks the daemon-owned in-app notification (ADR-0005 keeps live SQLite the
  sole notification authority). Mechanism lands in T4 `internal/notify`;
  non-Linux fails closed.

Non-Linux builds get fail-closed placeholders (`fsid_other.go`,
`unsupported_linuxonly.go`) returning `ErrUnsupportedPlatform`; a real non-Linux
implementation is a supported-platform change requiring spec confirmation.

## Linux-only mechanisms (design frozen; native runtime evidence required)

### Descendant containment (`containment_linux.go`)

Strategy: a **cgroup-v2 subtree** as the robust mechanism plus
`PR_SET_PDEATHSIG(SIGKILL)` as a fast path. A double-forked grandchild that
escapes its process group and reparents to init remains a cgroup member, so
`echo 1 > cgroup.kill` reaps the whole subtree atomically. If no delegated
cgroup is available the daemon runs in reduced-containment (pdeathsig-only) mode
and `KillTree` reports it cannot guarantee grandchild reaping (fail-closed
fallback, not a silent downgrade).

The supported PTY path opens the prepared cgroup directory and passes its file
descriptor through `SysProcAttr.UseCgroupFD`, placing the child in the cgroup
atomically during clone. Post-start `cgroup.procs` enrollment is retained only
as a fallback for alternate PTY seams because it has an unavoidable
fork-before-enrollment escape window.

### Descriptor-bound launch (`launch_linux.go`)

`OpenBound` uses `openat2` with
`RESOLVE_NO_SYMLINKS | RESOLVE_NO_MAGICLINKS | RESOLVE_BENEATH` and captures the
resolved inode's `{dev, ino}`; the caller re-validates digest + trust epoch
immediately before `LaunchBound`, which execs the **already-open descriptor** via
`/proc/self/fd/N` (fexecve semantics). A symlink swap, rename, or byte
replacement between validation and exec cannot substitute a different object;
path-only revalidation is explicitly insufficient and unsupported executable
forms fail closed.

### Native Linux evidence (cannot be inferred from the macOS author host)

Both harnesses compile under `GOOS=linux` (verified: `GOOS=linux GOARCH=amd64`
and `arm64` `go build ./...` succeed), and their runtime behavior MUST be proven
on the native T4/T6 CI matrix:

- **`spikes/containment`** — RUN: `sudo AMUX_CGROUP_ROOT=/sys/fs/cgroup/amux-spike
  go run ./spikes/containment`. PASS (exit 0): clone-time cgroup placement is
  confirmed, the inherited grandchild PID is captured, `cgroup.kill`
  terminates it, and the cgroup is removed. FAIL if the grandchild survives.
- **`spikes/launch`** — RUN: `go run ./spikes/launch` (kernel ≥ 5.6 for openat2).
  PASS (exit 0): symlink refused under `RESOLVE_NO_SYMLINKS`; after a
  rename/byte-replacement race the bound descriptor's inode is unchanged and
  executing it prints `ORIGINAL`, never `SUBSTITUTED`. FAIL if a substituted
  object executes.

These become blocking T3/T6 CI jobs (spec success criteria 7, 11; the S3/S4
security fixtures and Q3/Q5 acceptance suites consume them). No claim of passing
containment/launch runtime behavior is made from the author host.

## Consequences

**Positive**

- Every OS concern is one narrow, swappable interface; the domain never sees a
  syscall type.
- The author host proves PTY, filesystem-identity, project-key, and clock
  behavior today; the Linux-only surfaces compile and carry exact,
  reproducible deferred-evidence commands.

**Negative**

- Two of the highest-risk behaviors (containment, launch race-safety) cannot be
  validated on the author host; this is inherent to a Linux-only mechanism and is
  handled by explicit deferral, not by an unproven claim.

## Alternatives considered

- **Process-group-only containment** — rejected: double-forked grandchildren
  escape; cgroup is required for the guarantee.
- **Path-recheck-before-exec launch** — rejected: TOCTOU-vulnerable; descriptor
  binding is the only race-safe design.

## Follow-ups

- T4 B5/B11 implement supervision and hook launch atop these interfaces; T2
  S3/S4 freeze the adversarial fixtures; T3/T6 run the deferred harnesses on the
  Arch/Ubuntu matrix and attach the evidence.
