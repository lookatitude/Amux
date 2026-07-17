# T4 backend — handoff (reopened: T6 G-lane findings F1 + F4)

- task-id: T4-backend
- owner: backend
- depends-on: T1-architect (frozen contracts), T2-security (trust/redaction contracts)
- status: done (F1 bounded replay + F4 root --version fixed; all accepted T4/T5 behavior preserved)
- reopened-for: `.guild/runs/run-8c2926bb-2ebe-4166-8b77-<HIGH_ENTROPY_REDACTED>-lane:T6-qa/result-1.json` findings F1, F4 (F2/F3/F5/F6 are other lanes' scope and were NOT touched)

## Finding F1 — `Engine.ReplayRead` now validates and enforces `MaxBytes`

**Defect:** flow 14 base64-encoded the whole retained window (up to the 16 MiB
floor) into one unary response; past ~768 KiB retained the encoded reply
exceeded `v1.MaxHeaderBytes` (1 MiB) and the daemon severed the connection
with an untyped EOF.

**Contract (documented on `rpcapi.ReplayReadParams`/`ReplayReadResult`):**

- `max_bytes == 0` → server default page bound. `max_bytes > 0` → the page is
  capped at `min(max_bytes, server bound)`; the raw DECODED payload never
  exceeds the caller's ask. `max_bytes < 0` → typed `invalid_argument`.
- Server bounds (`internal/daemon/surface.go`): `replayReadMaxBytes` =
  512 KiB decoded + `replayReadMaxChunks` = 4096 per page. Worst-case encoded
  cost ≈ 512 KiB·4/3 base64 (~683 KiB) + 4096·~64 B JSON framing (~256 KiB)
  ≈ 940 KiB — conservatively under the 1 MiB `v1.MaxHeaderBytes` frame cap.
  These bound the READ page only; the 16 MiB retention floor
  (`terminal.MinRetentionBytes`) is untouched.
- **Chunks are never split** (a split would present two wire chunks under one
  sequence number — sequence truth broken). A positive bound smaller than the
  next whole chunk fails typed `invalid_argument` with structured
  `rpcapi.ReplayBoundDetails{max_bytes, next_chunk_bytes}` — the typed-bound-
  error branch of the contract; no new chunk representation was invented.
- **Partial-page sequence truth:** `next_seq` = last-returned-seq + 1 (the
  first sequence NOT returned); on an empty page (caller current) it is
  `latest_seq + 1`. Continuation with `from_seq = next_seq` is contiguous and
  duplicate-free; a sequence evicted between pages surfaces as a typed
  `replay_gap` on the next call, never a silent skip (gap-aware continuation).
- **Structured gap details:** the `replay_gap` error's `details` now carry
  `rpcapi.ReplayGapDetails{from_seq, oldest_retained, latest_seq}`. Plumbing:
  `engineError` gained an optional `details json.RawMessage` (emitted through
  the existing `ErrorBody.Details` wire field — additive, no protocol bump;
  same method, same result shape) and `client.Error` now carries `Details`
  verbatim so automation branches on code + details, never message text. The
  attach-snapshot path already presented a structured `replay_gap` object and
  is unchanged.
- Mechanism: new `terminal.Ring.ReplayLimitBytes(fromSeq, maxChunks, maxBytes)`
  (`ReplayLimit` delegates; `Replay` semantics untouched) returns the longest
  contiguous whole-chunk prefix within budget and copies ONLY the selected
  page — allocations are proportional to the page, not the 16 MiB window.
  New typed `terminal.ErrBoundTooSmall` / `*BoundTooSmallError`.

**Red→green evidence (TDD; every test below was written first and failed
against the old code — the new-API tests failed to compile, the black-box
`--version`/flood tests failed at runtime):**

- Ring: `<HIGH_ENTROPY_REDACTED>`, `…BoundTooSmall`, `…GapTyped`
  (internal/terminal/ring_test.go) — byte budget, no-split, typed bound,
  gap/cursor semantics preserved.
- Engine: `<HIGH_ENTROPY_REDACTED>` (3 MiB retained: default
  bound, encoded page < `MaxHeaderBytes`, 64 KiB caller bound honored,
  oversized ask clamped, byte-exact contiguous reassembly across pages,
  negative + tiny bounds typed) and `<HIGH_ENTROPY_REDACTED>`
  (17 MiB through the 16 MiB floor: typed gap, decodable details, continuation
  from `oldest_retained` succeeds) — internal/daemon/engine_test.go.
- RPC (real socket, production dispatch): `<HIGH_ENTROPY_REDACTED>`
  (2 MiB retained: the connection ANSWERS instead of severing, pages ≤ cap,
  contiguous to completion, tiny bound typed over the wire, connection healthy
  after) — internal/daemon/server_test.go.
- Client: `<HIGH_ENTROPY_REDACTED>` (`ErrorBody.Details` →
  `client.Error.Details` verbatim) — internal/client/client_test.go.
- CLI flow 14: e2e now drives `replay read --max-bytes 65536` (decoded ≤ bound,
  `next_seq` = last+1, continuation never re-serves a sequence) and
  `--max-bytes=-1` → exit 2 — cmd/amux/e2e_test.go.
- Resource exhaustion (the blocking `integration-resource-exhaustion` check):
  `go test -count=1 -tags integration -run 'ResourceExhaustion'
  ./internal/daemon` **PASSES on the production `Run` assembly**: 48 MiB PTY
  flood, heap bounded, 100-client burst all served, flooded `replay.read
  MaxBytes=1MiB` answers on a surviving connection, the cursor-1 gap is
  consumed via STRUCTURED details (the regex message-parse in the test was
  replaced — automation never parses a message), continuation pages the whole
  window contiguously, `MaxBytes=1` fails typed, and the same connection
  serves health afterwards. The KNOWN-DEFECT pin comment was removed with the
  defect.

## Finding F4 — root `amux --version` flag

`cmd/amux/main.go`: the root command now sets cobra `Version =
version.String()` with template `{{.Version}}\n` — the SAME stamped
identity line (`internal/version.String()`: version, protocol, commit, date)
that `amux version` (human) and `amuxd --version` print. Handled by cobra
before any RunE: no daemon dial, exit 0, deterministic single line on stdout.
The `version` subcommand and its `--json` schema (`{version, protocol}`) are
byte-for-byte preserved (pinned by test).

Tests (red first: `unknown flag: --version`): `TestRootVersionFlag`,
`<HIGH_ENTROPY_REDACTED>`,
`<HIGH_ENTROPY_REDACTED>` (real binary, empty HOME/XDG, no daemon,
captured stdout) — cmd/amux/version_test.go.

**Frozen install smoke:** `packaging/smoke/smoke-install.sh` (unmodified)
**PASSES end-to-end in a clean `ubuntu:24.04` container** against a tarball
assembled in the release layout (CGO_ENABLED=0 GOOS=linux GOARCH=arm64 builds
of both binaries + `scripts/gen-completions.sh` + packaging/systemd unit):
binaries executable, `amux --version` and `amuxd --version` both print
`amux 0.0.0-dev (protocol 1.1, commit unknown, built unknown)` and exit 0,
both binaries provably static (cgo-free), completions present → `smoke: PASS`.
Honest boundary: the tarball was hand-assembled because the pinned GoReleaser
config cannot build (finding F3 — devops scope, deliberately untouched here);
no release-container evidence is claimed or fabricated.

## Gates (author host darwin/arm64; Linux cross-checks as noted)

- `go test -count=1 ./...` → **744 passed / 47 packages** (includes the
  20-flow E2E; was 690/44 at the prior receipt — growth is new tests plus
  other lanes' packages).
- `go test -race -count=1 ./...` → 744 passed / 47 packages.
- 20-flow E2E `TestTwentyFlows` (real binary + real daemon + real PTYs,
  now including the flow-14 bounded-page/continuation/typed-bound steps) →
  PASS, focused run.
- `go test -count=1 -tags integration -run 'ResourceExhaustion'
  ./internal/daemon` → PASS (see F1 evidence).
- Attach stress: `go test -race ./internal/attach/` → 15 passed (fanout /
  slow-consumer suites; `ReplayLimit` call site untouched, delegation proven
  by the unchanged suite).
- Security conformance: `.<HIGH_ENTROPY_REDACTED> ./internal/hooks/` →
  33 passed; frozen securitytest sources untouched.
- `make verify` → exit 0 (fmt-check, vet, staticcheck, mod-verify,
  tidy-check, deps-manifest, license, generate-check, linkage fixtures,
  backup-restore selftest).
- `go vet ./...` and `GOOS=linux go vet ./...` clean; gofmt clean on every
  touched file; staticcheck clean on touched packages.
- `go mod verify` clean; `go mod tidy` no-op.
- `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./...` and
  `…GOARCH=arm64 go build ./...` both succeed (no cgo).

## Scope

Modified (production): `internal/terminal/ring.go` (ReplayLimitBytes +
typed bound error), `internal/rpcapi/rpcapi.go` (flow-14 contract docs +
<HIGH_ENTROPY_REDACTED>), `internal/daemon/engine.go`
(engineError details plumbing), `internal/daemon/surface.go` (bounded
ReplayRead), `internal/client/client.go` (Error.Details passthrough),
`cmd/amux/main.go` (root --version), `cmd/amux/surface.go` (--max-bytes help
text only).
Modified (tests): `internal/terminal/ring_test.go`,
`internal/daemon/engine_test.go`, `internal/daemon/server_test.go`,
`internal/daemon/exhaustion_integration_test.go`,
`internal/client/client_test.go`, `cmd/amux/e2e_test.go`.
Added: `cmd/amux/version_test.go`.
Did NOT modify: the project-identity/security fix (control actor, trust
store, trust-matrix work — F2/F5 lanes), GoReleaser/CI files (F3/F6 lanes),
`.gitignore`/history concerns (F6), TUI packages, research, frozen
securitytest sources, ADR texts, packaging scripts, protocol version
(additive details ride the existing `ErrorBody.Details` field; no new method,
no result-shape change, minor stays 1).

```guild.handoff.v2
{
  "schema_version": "guild.handoff.v2",
  "task_id": "T4-backend",
  "tier": "mid",
  "status": "done",
  "summary": "Reopened T4 closed T6 G-lane findings F1+F4. F1: Engine.ReplayRead now validates/enforces MaxBytes — pages are whole-chunk prefixes capped at min(caller bound, 512 KiB server cap, 4096 chunks) so the encoded unary response stays conservatively under v1.MaxHeaderBytes; zero=server default, negative=typed invalid_argument, below-next-chunk=typed invalid_argument with structured ReplayBoundDetails (chunks are never split); next_seq=last-returned+1 makes continuation contiguous, duplicate-free, and gap-aware; replay_gap errors carry structured ReplayGapDetails (from_seq/oldest_retained/latest_seq) through ErrorBody.Details into client.Error.Details so automation never parses a message; 16 MiB retention floor untouched; page-proportional allocations via new Ring.ReplayLimitBytes. F4: root `amux --version` prints the identical stamped version.String() as `amux version` and `amuxd --version`, exits 0 without dialing, deterministic stdout; `version` subcommand + --json preserved. TDD red→green at <HIGH_ENTROPY_REDACTED> layers; frozen smoke-install.sh PASSES in a clean ubuntu:24.04 container. Full suite 744/47 + race green; integration resource-exhaustion PASS; make verify green; linux amd64+arm64 no-cgo builds green.",
  "artifacts": [
    "internal/terminal/ring.go",
    "internal/terminal/ring_test.go",
    "internal/rpcapi/rpcapi.go",
    "internal/daemon/engine.go",
    "internal/daemon/surface.go",
    "internal/daemon/engine_test.go",
    "internal/daemon/server_test.go",
    "internal/daemon/exhaustion_integration_test.go",
    "internal/client/client.go",
    "internal/client/client_test.go",
    "cmd/amux/main.go",
    "cmd/amux/surface.go",
    "cmd/amux/version_test.go",
    "cmd/amux/e2e_test.go",
    ".guild/runs/run-8c2926bb-2ebe-4166-8b77-<HIGH_ENTROPY_REDACTED>-T4-backend.md"
  ],
  "issues": [],
  "learnings": [
    "A unary result that travels in a frame header must be budgeted in ENCODED terms: a decoded-byte cap alone is unsafe because base64 (4/3) plus per-chunk JSON framing dominate with many small chunks — cap decoded bytes AND chunk count so the worst case is provably under the header limit.",
    "Never split a replay chunk to satisfy a byte bound: two wire chunks under one sequence number breaks sequence truth for every cursor consumer; the safe contract is whole-chunk pages plus a typed bound-too-small error carrying the minimum viable bound.",
    "Partial pages change cursor semantics: next_seq must be last-returned+1, not latest+1, or bounded continuation silently skips the un-returned suffix; eviction between pages then surfaces naturally as the typed replay_gap.",
    "Structured error details already had a wire slot (ErrorBody.Details); the gap was plumbing — the daemon's internal error type and the shared client both dropped them, which is what forced tests to regex-parse human messages."
  ],
  "notes": "F2 (overlayfs identity), F3 (GoReleaser pin), F5 (trust-matrix gate binding), F6 (release-promotion gate) belong to other lanes and were not touched. The smoke-install PASS used a hand-assembled release-layout tarball (linux/arm64 no-cgo + gen-completions.sh + systemd unit) because the pinned GoReleaser config cannot build (F3); no release-container evidence is claimed. internal/hooks/trustmatrix_integration_test.go appeared concurrently from the security lane and was left untouched.",
  "injection_clean": "clean"
}
```
