# Amux documentation

This index separates current operator and contributor guidance from design
decisions, verification evidence, and historical research.

## Start here

- [`../README.md`](../README.md) — product overview, build, and quick start.
- [`../CONTRIBUTING.md`](../CONTRIBUTING.md) — contribution requirements and
  the `feature -> next -> main` workflow.
- [`development-workflow.md`](development-workflow.md) — branch roles,
  promotion rules, and release-channel operations.
- [`tui.md`](tui.md) — interactive client controls, accessibility, and backend
  projection model.
- [`testing/strategy.md`](testing/strategy.md) — requirement-to-test
  traceability and evidence expectations.

## Architecture decisions

The ADRs are normative for architecture and compatibility decisions:

1. [`adr/0001-authority-and-package-boundaries.md`](adr/0001-authority-and-package-boundaries.md)
2. [`adr/0002-domain-graph-and-identifiers.md`](adr/0002-domain-graph-and-identifiers.md)
3. [`adr/0003-local-protocol-v1.md`](adr/0003-local-protocol-v1.md)
4. [`adr/0004-event-and-attach-ordering.md`](adr/0004-event-and-attach-ordering.md)
5. [`adr/0005-persistence-and-restore.md`](adr/0005-persistence-and-restore.md)
6. [`adr/0006-platform-interfaces.md`](adr/0006-platform-interfaces.md)
7. [`adr/0007-dependency-and-compatibility-policy.md`](adr/0007-dependency-and-compatibility-policy.md)

## Operations and security

- [`operations/reference-profile.md`](operations/reference-profile.md) — soak
  and benchmark reference environments.
- [`security/security-readiness.md`](security/security-readiness.md) — security
  gate and receipt contract.
- [`security/threat-model.md`](security/threat-model.md) — threats and trust
  boundaries.
- [`security/hook-authorization.md`](security/hook-authorization.md) — hook
  grants, confirmation, and revocation.
- [`security/local-transport-hardening.md`](security/local-transport-hardening.md)
  — local socket ownership and peer checks.
- [`security/redaction-and-audit.md`](security/redaction-and-audit.md) — safe
  diagnostics and audit handling.

## Release and recovery

- [`release/versioning-and-release.md`](release/versioning-and-release.md)
- [`release/artifact-verification.md`](release/artifact-verification.md)
- [`release/aur-maintenance.md`](release/aur-maintenance.md)
- [`release/rollback-and-recovery.md`](release/rollback-and-recovery.md)
- [`dependencies.md`](dependencies.md) — dependency pins, licenses, and update
  evidence.

## Provenance

`research/` contains clean-room research and parity analysis. `.guild/`
contains the approved product/specification artifacts and execution evidence.
When prose and implementation disagree, reopen the current code, tests, and
normative ADR rather than relying on an older research snapshot.
