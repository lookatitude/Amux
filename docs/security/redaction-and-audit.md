# Redaction and audit requirements (T2-security, frozen)

Status: frozen 2026-07-15 (run-8c2926bb-2ebe-4166-8b77-84c0de3e12c0, lane T2-security).
Centralized data-classification and redaction rules plus the audit-record
contract. T4 implements one redaction engine used by every egress; scattering
per-callsite regexes is a contract violation. Fixtures:
`testdata/security/fixtures/redaction.json` via `securitytest.RunConformance`;
golden expectations never contain raw candidate secret values (enforced today
by `TestRedactionFixturesContainNoRawSecrets`).

## Data classification

| Class | Examples | Egress rule |
|-------|----------|-------------|
| `secret` | values of denylisted env keys, credential-shaped tokens, private keys | Never egresses any boundary; replaced by `[REDACTED:<label>]`. Never persisted outside the PTY replay ring (RR-3). |
| `operator` | raw PTY bytes, pane titles, cwd paths | Egress to clients/UI freely; egress to hooks/notifications/diagnostics only via typed, redacted fields. |
| `structural` | IDs, sequence numbers, epochs, digests, error codes | Free egress; these are the automation contract. |

## Redaction requirements

- **RED-1 (single engine, every egress)** One centralized redaction engine is
  applied at each of these egress contexts, enumerated as the frozen context
  set: `config`, `environment`, `hook_input`, `hook_output`, `error`, `log`,
  `audit`, `snapshot`, `agent_adapter`, `notification`, `diagnostics`.
  The conformance fixture set covers every context; a context added later must
  extend the fixtures in the same change.
- **RED-2 (deny-by-pattern + deny-by-key)** The engine redacts (a) values of
  configured secret env keys and any key matching the default denylist
  (`*TOKEN*`, `*SECRET*`, `*PASSWORD*`, `*KEY*` unless allowlisted as
  non-secret, `*CREDENTIAL*`), and (b) credential-shaped values (long
  high-entropy tokens, PEM blocks, `Authorization:`-style headers) regardless of
  key. Structured fields are redacted field-wise; byte streams are scanned as
  streams.
- **RED-3 (environment)** Hook environment assembly redacts nothing — it simply
  never includes non-allowlisted keys (HA-8). Redaction applies to *reporting*
  of environments (audit, logs, diagnostics), where values of secret-classified
  keys appear only as `[REDACTED:<key>]`.
- **RED-4 (PTY replay ring)** Raw replay bytes are exempt from content
  redaction (product truth, RR-3) but the ring files must be owner-only mode
  0600 in the owner-only data dir, and no other subsystem may copy ring bytes
  into a redaction-free context.
- **RED-5 (errors, logs, diagnostics)** Error messages, `slog` output, and
  diagnostic dumps pass the engine. Truncation of a payload (e.g. the 1 MiB
  hook output cap) must be redaction-safe: truncate first, then redact, then
  append the truncation marker — a secret split across the truncation boundary
  must not leak its prefix (fixture `redaction.truncation`).
- **RED-6 (snapshots and notifications)** Snapshot components (graph JSON,
  notify export) and desktop notification payloads pass the engine before
  write/delivery. Snapshots additionally persist env values only for
  allowlisted non-secret keys (ADR-0005).
- **RED-7 (agent adapters)** Adapter payloads (both directions) pass the engine
  before entering daemon state or leaving toward a provider process.
- **RED-8 (fail closed)** If the engine errors on a payload, the payload is
  dropped or replaced by a redaction-failure marker — never passed through raw.

## Audit requirements

Audit records are control-actor-owned, SQLite-only, append-only (ADR-0005).

- **AUD-1 (identity)** Every audited action records: monotonic audit sequence,
  wall time (advisory), acting connection identity (peer UID, client name),
  project key, and epoch at action time.
- **AUD-2 (hook lifecycle)** Audited events: project register/opt-in/approve/
  deny/revoke/reapprove; grant approve/invalidate/inactive; activation
  enqueue/deny (with error code); descriptor validation result (digest actually
  verified on the executed object); spawn (PID + containment label); terminate;
  kill escalation; exit (classification); output truncation; timeout.
- **AUD-3 (denials are first-class)** Every fail-closed denial (all §8 codes in
  hook-authorization.md) produces an audit record; absence of a record for a
  denial is itself a conformance failure.
- **AUD-4 (both orderings visible)** The revoke-first and launch-first fixtures
  assert their full audit trails: revoke-first shows revoke → activation denied,
  zero spawns; launch-first shows spawn → revoke → terminate → kill(2 000 ms) →
  exit.
- **AUD-5 (ordering authority)** Audit order is the audit sequence + epoch;
  wall-clock timestamps are advisory only and make no ordering claims.
- **AUD-6 (retention)** Revocation and grant invalidation retain full history
  (`inactive`, never deleted); restore never erases or truncates audit
  (`restore.audit-retained`). Bounded audit growth is handled by an explicit,
  audited archival policy — never silent truncation.
- **AUD-7 (redacted)** Audit payloads pass RED-1 (`audit` context); audit is a
  reporting surface, never a secret store.
