# ADR-0003 — Local control protocol v1

- Status: Accepted
- Date: 2026-07-15
- Deciders: architect (T1)
- Significance: high (persisted public contract)
- Enforced by: `api/v1` golden vectors + codec tests (`go test ./api/v1/`), vectors under `api/v1/testdata/`

## Context

The daemon, CLI, and TUI speak one versioned protocol over a Unix socket beneath
`$XDG_RUNTIME_DIR`. It must carry both JSON control messages and raw,
already-sequenced terminal output without base64 bloat; bound every read to fail
closed on a hostile peer; and evolve without breaking older clients. This ADR
freezes the framing, envelopes, error taxonomy, negotiation, unknown-field
policy, and compatibility windows. The wire shapes are pinned as golden vectors.

## Decision drivers

- Efficient raw-output transport (spec F4: no base64 expansion).
- Fail-closed bounded framing (PRD F4; least local DoS surface).
- Additive minor evolution without breaking peers; strict durable boundaries.
- A machine-stable error taxonomy independent of human strings.

## Decision

### Framing (`api/v1/frame.go`)

```
frame  := headerLen(uint32 BE) || header || bodyLen(uint32 BE) || body
header := UTF-8 JSON object, 1..MaxHeaderBytes (1 MiB)
body   := opaque bytes, 0..MaxBodyBytes (8 MiB); 0 for control frames
```

The header is always JSON; terminal-output frames put raw PTY bytes in `body`
with no base64. Both length prefixes are validated against the limits **before
allocation**: an oversize prefix returns a `resource_exhausted` `FrameError`; a
mid-frame EOF returns `io.ErrUnexpectedEOF`; a clean inter-frame EOF returns
`io.EOF` so a graceful close is distinguishable from truncation. `MaxBodyBytes`
(per-frame, 8 MiB) is independent of the per-surface 16 MiB replay floor, which
spans many frames.

### Envelopes (`api/v1/protocol.go`)

The header `type` selects: `hello`, `welcome`, `request`, `response`, `event`.
Canonical examples are frozen as golden vectors in `api/v1/testdata/`:

- `hello` / `welcome`: negotiation (major, minor, boot id, client/server tags).
- `request`: `id`, `method`, optional `deadline_ms`, raw `params`.
- `response`: `id` + exactly one of `result` (revision-bearing) or `error`.
- `event`: `boot_id`, `session`, monotonic `seq`, `event`, `time_ms`, `payload`.
- `error`: `code`, `message`, `retryable`, optional structured `details`.

### Error taxonomy (frozen, exhaustive)

`invalid_argument`, `not_found`, `conflict`, `unsupported_version`,
`not_input_lease_holder`, `event_gap`, `replay_gap`, `project_trust_required`,
`hook_grant_required`, `hook_grant_stale`, `scope_denied`, `resource_exhausted`,
`internal`. Pinned by `TestErrorTaxonomyFrozen`; adding/removing a code requires
editing `AllErrorCodes` and is therefore review-visible. Human `message` strings
are diagnostics, never automation contracts.

### Negotiation and compatibility

- Every connection begins with `hello`/`welcome`. Major mismatch is rejected with
  `unsupported_version` **before** any request is accepted (`Negotiate`).
- **Major** = breaking. **Minor** = additive/backward-compatible; a newer peer
  negotiates down to the older peer's minor (`min`). Compatibility matrix pinned
  by `TestNegotiationMatrix`.
- Compatibility window: within major 1, all minors interoperate by down-negotiation.
  A major bump requires a new ADR and a migration/deprecation note.

### Unknown-field policy

- **Envelope headers**: decode leniently — unknown additive fields are ignored so
  a v1.N peer tolerates a v1.(N+1) peer's extra header fields (`DecodeLenient`).
- **Durable payloads** (command `params`, persisted structures): decode strictly
  — unknown fields are rejected because they are contract boundaries, not
  evolvable envelopes (`DecodeStrict`). Pinned by `TestUnknownFieldPolicy`.

### Limits and deadlines

`MaxHeaderBytes` and `MaxBodyBytes` bound every frame. `deadline_ms` is an
optional client deadline hint. Heartbeats, replay-gap boundaries, and
slow-consumer disconnects are specified in ADR-0004. This build advertises
`Major=1, Minor=0`; `internal/version.Protocol` is the single source of the
version string.

## Consequences

**Positive**

- Wire shapes are frozen as reviewable golden files; a change is a visible diff
  and an ADR amendment. Regenerate deliberately with `go test ./api/v1/ -run
  TestGoldenVectors -update`.
- `api/v1` depends only on the standard library, so it can be vendored or
  code-generated for other clients without runtime coupling.

**Negative**

- Two decode modes (lenient/strict) add a small discipline cost; the split is
  necessary to reconcile forward-compat with durable-boundary strictness.

## Alternatives considered

- **gRPC/JSON-RPC framework** — rejected: a network-facing RPC stack is
  unnecessary for an owner-only local socket and adds heavy dependencies; the
  standard library plus Amux framing keeps evolution testable (PRD tooling).
- **base64 output frames** — rejected: ~33% bandwidth/CPU overhead on the hottest
  path; raw bodies with a separate length prefix avoid it.

## Follow-ups

- T4 backend B3 implements the reader/writer and reconnecting client over this
  contract and adds malformed-frame/partial-read/version-skew tests.
- T2 security reviews socket permissions, `SO_PEERCRED`, and runtime-path
  validation (contract surface referenced here; mechanism in ADR-0006).
