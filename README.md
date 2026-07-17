# Amux

Amux is a clean-room, Go-authoritative terminal workspace runtime built for
Linux, with Arch Linux as its reference distribution. It combines a durable
local daemon, a scriptable CLI, and an interactive split-pane TUI so terminal
workspaces can survive client detaches, be inspected by automation, and recover
from snapshots without giving the UI a second source of truth.

> **Project status:** active pre-1.0 development. The implementation and test
> suite are substantial, but interfaces and storage formats may still change.
> Use `next` for the tested development channel and `main` for promoted
> release-ready history.

## Highlights

- Linux-first runtime with native Arch and Ubuntu CI on amd64 and arm64.
- Daemon-owned sessions, workspaces, pane trees, terminal surfaces, events,
  input leases, notifications, snapshots, and hook trust.
- Interactive Bubble Tea v2 TUI with tmux-style split navigation.
- Stable JSON output and deterministic exit codes for scripts and agents.
- Ordered attach streams: snapshot, retained replay, then live output.
- XDG paths, owner-only local transport, explicit trust grants, redaction, and
  audit records.
- CGO-free release binaries, GoReleaser packaging, SBOMs, checksums, systemd
  user-unit support, and an AUR package definition.

## Requirements

- Linux is the supported runtime target. Arch Linux rolling is the reference;
  Ubuntu 24.04 LTS is the compatibility lane.
- Go 1.26.5, pinned by `go.mod` and `scripts/tools.env`.
- A terminal with PTY support. A cgroup v2 host is required for the complete
  descendant-containment runtime gate.

The code compiles on some non-Linux hosts through explicit platform seams, but
that is portability support, not a runtime-support claim.

## Build from source

```bash
git clone git@github.com:lookatitude/Amux.git
cd Amux

mkdir -p build
go build -o build/amux ./cmd/amux
go build -o build/amuxd ./cmd/amuxd
```

Run the fast blocking suite with:

```bash
make test
```

Run the deterministic contributor gate with:

```bash
make tools
make verify
make build-linux
```

`make help` lists every supported build, test, soak, and release target.

## Quick start

Start the daemon in one terminal:

```bash
./build/amux daemon start
```

Then create a session and workspace from another terminal. Commands that create
objects support `--json`; use the returned IDs in later commands.

```bash
./build/amux session create --name development --json
./build/amux workspace create \
  --session <session-id> \
  --name amux \
  --root "$PWD" \
  --cwd "$PWD" \
  --json

./build/amux tui --session <session-id> --workspace <workspace-id>
```

Explore the complete command surface with `amux --help`. The TUI uses
`Ctrl+b` as its prefix; press `Ctrl+b ?` for keybinding discovery. See the
[TUI operator guide](docs/tui.md) for the full interaction and accessibility
model.

## Architecture

```text
amux CLI / TUI
      │ local protocol v1 over owner-only Unix socket
      ▼
    amuxd
      ├─ control + session actors (ordering and authority)
      ├─ PTY + terminal engine (processes, replay, cell projections)
      ├─ SQLite + snapshots (durability and restore)
      ├─ attach + event streams (replay/live cutover and backpressure)
      └─ hooks + trust + audit (project-scoped execution policy)
```

The daemon is the only durable mutation authority. CLI and TUI clients consume
the same protocol and projections; the TUI does not parse terminal output or
own a shadow workspace model. The decisions behind this design are recorded in
the [architecture decision records](docs/adr/).

## Documentation

Start with the [documentation index](docs/README.md). Important references:

- [TUI operator guide](docs/tui.md)
- [Testing and traceability](docs/testing/strategy.md)
- [Security readiness](docs/security/security-readiness.md)
- [Versioning and release](docs/release/versioning-and-release.md)
- [Development and promotion workflow](docs/development-workflow.md)
- [Dependency and license manifest](docs/dependencies.md)

Research and clean-room provenance live under `research/` and `.guild/`. They
support the implementation but are not substitutes for the current code and
operator documentation.

## Development workflow

Create a short-lived branch from `next`, develop and test there, then open a PR
back to `next`. Promotion is a separate PR from `next` to `main`; it must update
the human-readable [CHANGELOG.md](CHANGELOG.md). Both protected branches require
their GitHub checks to pass. See [CONTRIBUTING.md](CONTRIBUTING.md) for the exact
commands and branch policy.

## License

Amux is available under the [MIT License](LICENSE).
