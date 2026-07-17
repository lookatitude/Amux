# Local transport and diagnostics hardening (T2-security, frozen)

Status: frozen 2026-07-15 (run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0, lane T2-security).
Testable requirements for the control socket, runtime paths, request limits,
diagnostics, and local denial-of-service controls. Each `STR-n` names its test
obligation; T4 implements behind the frozen `platform.LocalTransport` /
`platform.PeerCredentials` seams, T6 executes the integrated checks
(`readiness-manifest.json` maps them). Threat context: threat-model.md §B1/§B7.

## Socket and runtime path

- **STR-1 (owner-only endpoint)** The control socket lives beneath the private
  runtime directory (`$XDG_RUNTIME_DIR/amux/`). The daemon creates the directory
  `0700` and the socket `0600`, owned by the daemon UID. *Test:* stat the
  created dir/socket; assert owner==daemon UID, mode exactly 0700/0600.
- **STR-2 (mandatory peer credentials)** On Linux, every accepted connection is
  verified with `SO_PEERCRED` (via `LocalConn.Control` →
  `PeerCredentials.PeerUID`) **before the first protocol byte is read**. UID ≠
  daemon UID ⇒ the connection is closed without protocol response and the
  rejection is audited. There is no configuration to disable this check.
  *Test:* second-UID connect attempt is closed pre-`hello`; audit record exists.
- **STR-3 (runtime component validation)** Before bind, every path component of
  the runtime directory is validated: owned by the daemon UID (or root for the
  `$XDG_RUNTIME_DIR` parent), not group/world-writable, correct type, and the
  walk performs no symlink traversal (`O_NOFOLLOW`-style component descent).
  A failed component check aborts startup of the transport, fail closed with an
  actionable diagnostic. *Test:* pre-created hostile dir (wrong owner, 0777,
  symlinked component) ⇒ listen refused.
- **STR-4 (stale socket)** An existing path at the socket location is removed
  only after proving: it is a socket (not a symlink or other type), it is owned
  by the daemon UID, and no live daemon owns it (liveness probe: connect
  attempt / lock protocol). A hostile object at the path (symlink, foreign
  owner, live foreign listener) ⇒ refuse to bind, fail closed. *Test:* each
  hostile-object variant refuses; a genuine stale same-owner socket is
  reclaimed.

## Request limits and DoS controls

- **STR-5 (frame limits pre-allocation)** Length prefixes are validated against
  the frozen `api/v1` bounds (header 1 MiB, body 8 MiB) *before* any buffer
  allocation. Oversized ⇒ typed `resource_exhausted` (or connection close on an
  unframeable stream) with a bounded error path. *Test:* hostile 2 GiB length
  prefix allocates nothing beyond a fixed header buffer.
- **STR-6 (strict durable decode)** Command params and every persisted structure
  reject unknown fields and trailing data (`api/v1.DecodeStrict`). *Test:*
  golden vectors + unknown-field probes ⇒ `invalid_argument`.
- **STR-7 (read deadlines)** Every connection has bounded read/handshake
  deadlines; a peer that connects and stalls is disconnected within the bound.
  *Test:* silent peer disconnected; daemon goroutine count returns to baseline.
- **STR-8 (bounded queues, slow consumers, connection cap)** Event queues and
  per-connection buffers are bounded; a lagging subscriber is disconnected with
  its last-delivered sequence (ADR-0004) rather than buffered unboundedly; a
  configurable connection cap yields `resource_exhausted` for excess connects.
  *Test:* resource-exhaustion harness — N greedy clients cannot grow daemon RSS
  unboundedly or starve an existing session's event loop.
- **STR-9 (descriptor hygiene)** All daemon-held descriptors are close-on-exec;
  hook and PTY children receive only their intended descriptor set. *Test:*
  child `/proc/self/fd` enumeration in the integration harness shows the
  minimal set.

## Diagnostics

- **STR-10 (default off, owner-only)** `pprof` and any diagnostic endpoint are
  disabled by default and, when explicitly enabled, are exposed only through
  the owner-only local mechanism (same runtime-dir + peer-cred discipline as
  STR-1/STR-2) — never a TCP listener. *Test:* default build exposes nothing;
  enabled build refuses second-UID access.
- **STR-11 (read-only)** Diagnostic surfaces are read-only inspection; no
  diagnostic endpoint mutates graph, trust, or hook state. *Test:* diagnostic
  surface enumeration against the command table.
- **STR-12 (bounded + redacted)** Diagnostic output is bounded (no unbounded
  dumps) and passes the redaction classes (`RED-5`) before leaving the daemon.
  *Test:* redaction fixtures cover the diagnostics context.

## Second-UID / exhaustion acceptance

The integrated suite (T6) must include: a second-UID actor exercising STR-1
through STR-4 and STR-10 on a Linux host, and a resource-exhaustion run pinning
STR-5, STR-7, STR-8. These appear as blocking checks
`integration-second-uid` and `integration-resource-exhaustion` in
`docs/security/readiness-manifest.json`; they cannot run on the authoring macOS
host and are honestly deferred to the Linux CI matrix (T3 lane provides the
runners; T6 executes).
