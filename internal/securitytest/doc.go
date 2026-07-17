// Package securitytest is the executable T2-security contract: the frozen
// trust-decision matrix, the deterministic adversarial fixtures, and the
// conformance harness the T4 backend must pass and T6 QA must execute before
// release promotion (docs/security/security-readiness.md).
//
// Nothing in this package implements production behavior. It defines the
// implementation-neutral SystemUnderTest surface (contract.go), generates the
// trust matrix golden (matrix.go -> testdata/security/trust-matrix.json), and
// runs the fixture suite (harness.go) against whatever implementation is
// handed to RunConformance. With no implementation registered the conformance
// test SKIPS with an explicit prerequisite — it never pretends backend
// behavior exists. Everything else in the package (matrix golden, fixture
// vector validation, redaction-golden hygiene, readiness-manifest schema,
// gate-constant cross-checks) is executable today and gates this contract
// itself against drift.
//
// Normative prose: docs/security/threat-model.md, hook-authorization.md,
// local-transport-hardening.md, redaction-and-audit.md. Requirement IDs
// (HA-*, STR-*, RED-*, AUD-*, TM-*) referenced by fixtures and matrix rows
// resolve there.
package securitytest
