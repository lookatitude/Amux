# Dependency and license manifest

Frozen by ADR-0007 (work package A1). This file inventories every third-party
module reachable from `go.mod`/`go.sum`, its exact pin, its license as verified
from the `LICENSE`/`COPYING` file **inside the downloaded module** (never from
registry metadata), and whether it is compiled into Amux binaries.

- Toolchain: `go 1.26.0` directive, `toolchain go1.26.5` (pinned in `go.mod`).
- Policy: exact pins, permissive licenses only, `CGO_ENABLED=0`, no cgo modules
  — see `docs/adr/0007-dependency-and-compatibility-policy.md`.
- Staleness rule: regenerate this manifest whenever `go.mod` changes; a stale
  manifest is a review-blocking defect.

## Regeneration commands

```sh
# 1. Full module graph (everything below must appear here):
go list -m all

# 2. Modules compiled into Linux binaries (the release build graph):
GOOS=linux GOARCH=amd64 go list -deps \
  -f '{{if not .Standard}}{{.Module.Path}}@{{.Module.Version}}{{end}}' ./... \
  | grep -v '^$' | grep -v '^github.com/amux-run' | sort -u

# 3. Modules additionally reachable from tests:
GOOS=linux GOARCH=amd64 go list -deps -test \
  -f '{{if not .Standard}}{{.Module.Path}}@{{.Module.Version}}{{end}}' ./... \
  | grep -v '^$' | grep -v '^github.com/amux-run' | sort -u

# 4. License verification (per module, from the module cache):
find "$(go env GOMODCACHE)/<module>@<version>" -maxdepth 1 \
  \( -iname 'LICENSE*' -o -iname 'COPYING*' \) -exec head -4 {} \;

# 5. cgo prohibition (must print nothing):
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go list -deps -test \
  -f '{{if .CgoFiles}}CGO: {{.ImportPath}}{{end}}' ./... | grep -v '^$'
```

## Deliberate update record (2026-07-16, T4 contract completion)

Per ADR-0007 Decision 3 (deliberate updates with full gates), the T4 rework
that delivers the T5 contract updated two pins:

- `github.com/charmbracelet/x/ansi` **v0.4.5 → v0.11.7** — the exact version
  `charm.land/bubbletea/v2@v2.0.8` and `charm.land/lipgloss/v2@v2.0.5` require.
  The backend `internal/terminal` engine and `spikes/ansi` were migrated to the
  v0.11.7 parser API (zero-arg `NewParser` + `SetParamsSize`/`SetDataSize`,
  accessor methods replacing the exported `Params`/`ParamsLen`/`Cmd`/`Data`/
  `DataLen` fields, `Cmd.Final()`/`Cmd.Prefix()` replacing `Command()`/
  `Marker()`). Behavior preservation is pinned by the full pre-existing VT
  golden/differential-replay/fuzz/wide/combining/mode/damage/unsupported test
  suite plus a new adapter in `internal/terminal/decode.go` (`incompleteTailLen`)
  that holds a valid-but-incomplete trailing UTF-8 rune out of the decoder:
  v0.11.7's grapheme segmentation would otherwise swallow it into the preceding
  cluster and break chunk-size determinism (regression-pinned by
  `TestSplitRuneClusterBoundary`). The CSI hostile-params guard remains
  required (the unguarded params indexing still panics at v0.11.7;
  `TestEngineHostileParamsNoPanic`).
- `golang.org/x/sys` **v0.44.0 → v0.46.0** — Bubble Tea v2.0.8's floor,
  pre-aligned so T5's toolkit adoption causes no further graph churn.

**Bubble Tea v2 / Lip Gloss v2 co-resolution proof (author host, darwin/arm64):**
with `charm.land/bubbletea/v2@v2.0.8` + `charm.land/lipgloss/v2@v2.0.5` added
to this module, `go mod tidy` resolved `x/ansi` to exactly **v0.11.7** (no
backend bump), `go build ./...` succeeded across the repo, and
`go test ./internal/terminal/` passed with both toolkit modules importable
alongside the engine. **T5 has since adopted the toolkit**: both modules are
now `require`d in `go.mod` (imported by `internal/tui/teabridge` and
`cmd/amux/tui.go`), their transitive graph is frozen in the manifests, and the
full license/no-cgo gate is green — see "Adopted by T5 — PINNED" below.

## Direct dependencies (9)

| Module | Version | License (verified) | In Linux build graph | Imported by |
|---|---|---|---|---|
| `charm.land/bubbletea/v2` | v2.0.8 | MIT | yes | `internal/tui/teabridge` (production `amux tui` runtime), `cmd/amux` |
| `charm.land/lipgloss/v2` | v2.0.5 | MIT | yes | `internal/tui/teabridge` chrome (status bar, min-size fallback — geometry-safe styles) |
| `github.com/charmbracelet/x/ansi` | v0.11.7 | MIT | yes | `internal/terminal` engine (seam: ADR-0006 `TerminalEngine`; no other package may see it), `spikes/ansi` |
| `github.com/creack/pty` | v1.1.24 | MIT | yes | `internal/pty` supervisor (production PTY behind `internal/platform.PTY`), `spikes/pty` |
| `github.com/google/uuid` | v1.6.0 | BSD-3-Clause | yes | `internal/domain` (UUIDv7 IDs), `spikes/uuidv7` |
| `github.com/rivo/uniseg` | v0.4.7 | MIT | yes | `internal/tui/render` (grapheme-cluster widths, U3) |
| `github.com/spf13/cobra` | v1.8.1 | Apache-2.0 | yes | `cmd/amux`; `internal/archtest` bars it from `internal/domain` |
| `golang.org/x/sys` | v0.46.0 | BSD-3-Clause | yes | `internal/platform` Linux files (`openat2`, `SO_PEERCRED`), `spikes/containment`, `spikes/launch`, `cmd/amux` TUI raw-mode/termios (Linux+darwin) |
| `modernc.org/sqlite` | v1.53.0 | BSD-3-Clause | yes | `internal/store` (durable trust/grant/audit store; pure-Go SQLite, no cgo) |

## Indirect dependencies in the build graph (18)

| Module | Version | License (verified) | Pulled by | Note |
|---|---|---|---|---|
| `github.com/charmbracelet/colorprofile` | v0.4.3 | MIT | bubbletea/lipgloss | terminal color-capability negotiation |
| `github.com/charmbracelet/ultraviolet` | v0.0.0-20260703014108-f5a850f9c2b7 | MIT | bubbletea | cell/event model (Bubble Tea v2 core) |
| `github.com/charmbracelet/x/term` | v0.2.2 | MIT | bubbletea | terminal state (raw mode) |
| `github.com/charmbracelet/x/termios` | v0.1.1 | MIT | bubbletea | termios syscalls (pure-Go via x/sys) |
| `github.com/charmbracelet/x/windows` | v0.2.2 | MIT | bubbletea | Windows console API (not in Linux binary at runtime) |
| `github.com/muesli/cancelreader` | v0.2.2 | MIT | bubbletea | cancelable stdin reader |
| `github.com/xo/terminfo` | v0.0.0-20220910002029-abceb7e1c41e | MIT | colorprofile | terminfo capability lookup |
| `golang.org/x/sync` | v0.21.0 | BSD-3-Clause | bubbletea | `errgroup` |
| `github.com/clipperhouse/displaywidth` | v0.11.0 | MIT | x/ansi | grapheme display-width tables |
| `github.com/clipperhouse/uax29/v2` | v2.7.0 | MIT | x/ansi | UAX #29 grapheme segmentation |
| `github.com/dustin/go-humanize` | v1.0.1 | MIT | modernc.org/sqlite | size formatting |
| `github.com/lucasb-eyer/go-colorful` | v1.4.0 | MIT | x/ansi | color conversions |
| `github.com/mattn/go-runewidth` | v0.0.23 | MIT | x/ansi | wcwidth mode tables (engine uses grapheme mode) |
| `github.com/remyoudompheng/bigfft` | v0.0.0-20230129092748-24d4a6f8daec | BSD-3-Clause | modernc.org/mathutil | big-int FFT |
| `github.com/spf13/pflag` | v1.0.5 | BSD-3-Clause | cobra | flag parsing |
| `modernc.org/libc` | v1.73.4 | BSD-3-Clause | modernc.org/sqlite | pure-Go libc shim (no cgo) |
| `modernc.org/mathutil` | v1.7.1 | BSD-3-Clause | modernc.org/libc | math utilities |
| `modernc.org/memory` | v1.11.0 | BSD-3-Clause | modernc.org/libc | allocator |

## Module-graph-only (27) — in `go list -m all`, in **no** build or test binary

`github.com/aymanbagabas/go-udiff@v0.4.1`,
`github.com/bits-and-blooms/bitset@v1.24.4`,
`github.com/charmbracelet/x/exp/golden@v0.0.0-20250806222409-83e3a29d542f`,
`github.com/clipperhouse/stringish@v0.1.1`,
`github.com/cpuguy83/go-md2man/v2@v2.0.4`, `github.com/google/go-cmp@v0.5.8`,
`github.com/google/pprof@v0.0.0-20250317173921-a4b03ec1a45e`,
`github.com/hashicorp/golang-lru/v2@v2.0.7`,
`github.com/inconshreveable/mousetrap@v1.1.0` (cobra's Windows-only import —
not in the Linux graph), `github.com/mattn/go-isatty@v0.0.20`,
`github.com/ncruces/go-strftime@v1.0.0`,
`github.com/russross/blackfriday/v2@v2.1.0`,
`golang.org/x/exp@v0.0.0-20231110203233-9a3e6036ecaa`, `golang.org/x/mod@v0.36.0`,
`golang.org/x/tools@v0.45.0`,
`gopkg.in/check.v1@v0.0.0-20161208181325-20d25e280405`, `gopkg.in/yaml.v3@v3.0.1`,
`modernc.org/cc/v4@v4.28.4`, `modernc.org/ccgo/v4@v4.34.4`,
`modernc.org/fileutil@v1.4.0`, `modernc.org/gc/v2@v2.6.5`,
`modernc.org/gc/v3@v3.1.3`, `modernc.org/goabi0@v0.2.0`, `modernc.org/opt@v0.2.0`,
`modernc.org/sortutil@v1.2.1`, `modernc.org/strutil@v1.2.1`,
`modernc.org/token@v1.1.0`.

These never enter any `go build`/`go test` graph (verified by commands 2–3
above); at most their module hashes appear in `go.sum` (graph pruning keeps only
the go.mod hashes the build actually needs, so some — e.g. `go-cmp`,
`clipperhouse/stringish` — carry no `go.sum` entry at all). The former
staticcheck-only residents (`honnef.co/go/tools`, `golang.org/x/exp/typeparams`,
`github.com/BurntSushi/toml`, `golang.org/x/tools/go/expect`) are **absent** from
this set: staticcheck is a pinned build-time tool installed via
`go install …@version` into `./.tools/bin` (`scripts/tools.env`,
`Makefile:tools`), never a `go.mod` require, so it does not appear in
`go list -m all`. Honest deferral per ADR-0007: these modules' licenses are
**not** verified here and MUST be verified the moment any of them enters a build
graph.

## Verification evidence (2026-07-16, author host)

- Command 2 (Linux amd64 build graph) returns exactly the 27 modules frozen in
  `scripts/expected-modules-build.txt` (enforced by `make deps-manifest`) — the
  T4 graph of 17 plus the 10 Bubble Tea / Lip Gloss toolkit modules T5 adopted
  (`charm.land/bubbletea/v2`, `charm.land/lipgloss/v2`, `colorprofile`,
  `ultraviolet`, `x/term`, `x/termios`, `x/windows`, `muesli/cancelreader`,
  `xo/terminfo`, `golang.org/x/sync`).
- Command 3 (test graph) returns the same 27 modules — no module is reachable
  from tests alone (`scripts/expected-modules-test.txt`).
- Command 4 was run for all 27 build/test-graph modules via
  `scripts/check-license.sh`: every license file matches the permissive
  allowlist (MIT / BSD-2/3-Clause / Apache-2.0 / ISC). `cobra` ships
  Apache-2.0; the toolkit (`bubbletea`, `lipgloss`, `colorprofile`,
  `ultraviolet`, `x/term`, `x/termios`, `x/windows`, `cancelreader`,
  `terminfo`), `x/ansi`, `displaywidth`, `uax29`, `creack/pty`, `go-humanize`,
  `go-colorful`, `go-runewidth`, `uniseg` ship MIT; `uuid`, `x/sys`, `x/sync`,
  `pflag`, `bigfft`, `libc`, `mathutil`, `memory`, `sqlite` ship BSD-3-Clause.
- Command 5 (cgo prohibition) prints nothing: no cgo files anywhere in the
  Linux test graph (`internal/archtest` `TestNoCgo` pins the same rule).

## Build-time tools (never in `go.mod`)

| Tool | License | Pinned by | Role |
|---|---|---|---|
| GoReleaser `v2.12.7` | MIT | T3 (`scripts/tools.env` `GORELEASER_VERSION`) | Reproducible `CGO_ENABLED=0` linux amd64/arm64 tarballs + checksums + SBOMs (ADR-0007 Decision 1). Pin and config move together: the archive `ids`/`formats` keys need GoReleaser ≥ 2.6. |
| `syft` `v1.18.1` | Apache-2.0 | T3 (`scripts/tools.env` `SYFT_VERSION`) | CycloneDX SBOM per released tarball, invoked by GoReleaser's `sboms` stage |
| `PKGBUILD` (hand-authored) | n/a (ours) | `packaging/aur/` (T3) | AUR binary package consuming released tarballs |

## Adopted by T5 — PINNED

| Module | License (verified) | Status |
|---|---|---|
| `charm.land/bubbletea/v2` (v2.0.8) | MIT, pure Go | **Pinned in `go.mod` and imported by `internal/tui/teabridge` + `cmd/amux`. `go mod tidy` resolves x/ansi to exactly v0.11.7 (no backend bump); the full graph is frozen in `scripts/expected-modules-build.txt` and license-verified.** |
| `charm.land/lipgloss/v2` (v2.0.5) | MIT, pure Go | **Pinned in `go.mod` and imported by the teabridge chrome for geometry-safe status/min-size styling.** |

The former "frozen-contract conflict" (T5 attempt 1 ask-gate) is resolved: the
backend VT engine no longer depends on the removed v0.4.5 streaming-parser API,
and T5 has now adopted the toolkit with the full deterministic gate green
(gofmt, vet, staticcheck-equivalent scope audit, `go test ./...` + `-race`,
deps-manifest, license, linux amd64/arm64 no-cgo builds).
