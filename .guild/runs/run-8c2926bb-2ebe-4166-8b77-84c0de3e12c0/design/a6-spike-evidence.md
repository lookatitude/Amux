# A6 spike evidence — T1-architect (resume completion, 2026-07-15)

Author host: macOS (darwin/arm64), go1.26.5. Every claim below is either an
executed command with its observed result on this host, or an **explicitly
deferred** Linux-only check with the exact command and pass/fail criteria a
Linux host must run. No Linux runtime behavior is claimed from macOS.

## Executed on the author host

| Command | Result |
|---|---|
| `go test -v -count=1 ./spikes/...` | 8 named tests pass, 0 fail (list below) |
| `gofmt -l .` | no output (all files formatted) |
| `go vet ./...` | clean |
| `GOOS=linux GOARCH=amd64 go vet ./...` | clean |
| `go test ./...` | 77 tests pass in 14 packages |
| `go test -race ./...` | 77 tests pass in 14 packages |
| `go test -count=1 ./internal/archtest/` | 3 tests pass (dependency-rule gate) |
| `go build ./...` (darwin) | succeeds |
| `GOOS=linux GOARCH=amd64 go build ./...` | succeeds (compile-only) |
| `GOOS=linux GOARCH=arm64 go build ./...` | succeeds (compile-only) |
| `go mod verify` | `all modules verified` |

### Spike test names (from `go test -v -count=1 ./spikes/...`)

- `spikes/ansi` — VT decoding (charmbracelet/x/ansi v0.4.5):
  `TestCorpusDecodesWithoutCrashOrDesync`,
  `TestTruncatedTailDoesNotCrashOrDropContent`, `TestFullBufferConsumed` — PASS.
- `spikes/pty` — PTY primitive (creack/pty v1.1.24):
  `TestPTYRoundTrip`, `TestPTYResize` — PASS.
- `spikes/uuidv7` — ID generation (google/uuid v1.6.0):
  `TestGoogleUUIDMonotonicWithinMillisecond`, `TestGoogleUUIDConcurrentUnique`,
  `TestClampSurvivesClockRegression` — PASS.

## Spike → decision closure

| A6 spike | Outcome | Frozen in |
|---|---|---|
| VT parsing (`spikes/ansi`) | charmbracelet/x/ansi selected as decoder behind the `TerminalEngine` seam; raw bytes stay authoritative; malformed input degrades to bounded diagnostics | ADR-0006, ADR-0007 |
| UUIDv7 (`spikes/uuidv7`) | google/uuid selected; monotonic-floor clamp documented as the owned fallback if swapped | ADR-0002, ADR-0007 |
| Bubble Tea damage inputs | Closed at the input boundary: ansi spike proves classified tokens + uniseg grapheme widths — the cell-grid damage inputs a renderer diff consumes. Bubble Tea selected (MIT, pure Go) but **not pinned**; T5 pins it. No Bubble Tea code executed in T1 | ADR-0007 Decision 1 |
| Release tooling | GoReleaser selected for tarballs/checksums (build-time tool, never in `go.mod`); AUR PKGBUILD hand-authored; deb/rpm out of MVP. Decision-level closure; pipeline runtime validation owned by T3 | ADR-0007 Decision 1 |
| Daemon-death containment (`spikes/containment`) | cgroup-v2 subtree + pdeathsig fast path; fail-closed reduced mode without a delegated cgroup | ADR-0006; runtime evidence **deferred** (below) |
| Descriptor-bound launch (`spikes/launch`) | `openat2(RESOLVE_NO_SYMLINKS\|NO_MAGICLINKS\|BENEATH)` + exec via `/proc/self/fd/N`; path-only recheck rejected as TOCTOU-unsafe | ADR-0006; runtime evidence **deferred** (below) |

Per the A6 contract, no unselected throwaway code exists to delete: every spike
directory under `spikes/` corresponds to a *selected* outcome and is retained as
executable evidence, each with a `doc.go`/header naming the ADR that froze it.

## Deferred Linux-only runtime checks (honest deferral — cannot run on macOS)

Both harnesses compile under `GOOS=linux` amd64 and arm64 (verified above), but
their runtime claims are **not** made from this host. They become blocking
Arch/Ubuntu CI jobs in T3 and are consumed by T2's S3/S4 adversarial fixtures.

1. **`spikes/containment`** — requires a cgroup-v2 Linux host with a delegated
   writable subtree.
   RUN: `sudo mkdir -p /sys/fs/cgroup/amux-spike && sudo
   AMUX_CGROUP_ROOT=/sys/fs/cgroup/amux-spike go run ./spikes/containment`
   PASS (exit 0): the double-forked, process-group-escaping, init-reparented
   grandchild PID is captured; after `cgroup.kill` it is no longer alive; the
   cgroup is removed. FAIL: the grandchild survives the daemon's SIGKILL.
2. **`spikes/launch`** — requires Linux kernel ≥ 5.6 (`openat2`).
   RUN: `go run ./spikes/launch`
   PASS (exit 0): symlink resolution is refused under `RESOLVE_NO_SYMLINKS`;
   across symlink-swap / rename / byte-replacement / config-replacement /
   project-root-replacement races the bound descriptor's `{dev,ino}` is
   unchanged and executing it prints `ORIGINAL`. FAIL: any substituted object
   executes (`SUBSTITUTED`).
3. **Linux full test suite** — `go test ./...` and `go test -race ./...` on
   Arch and Ubuntu 24.04 (T3 CI matrix). Only compile-only + vet evidence
   exists from this host.
