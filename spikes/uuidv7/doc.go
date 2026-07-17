// Package uuidv7spike is a throwaway architectural spike (work package A6). Its
// CONCLUSION is frozen into docs/adr/0002-domain-graph-and-identifiers.md (opaque
// sortable IDs) and docs/adr/0007-dependency-and-compatibility-policy.md
// (google/uuid pin). The code is retained only as executable evidence for two
// invariants — same-millisecond monotonicity and clock-regression safety — and
// documents the exact monotonic-floor clamp Amux would own if the dependency
// were ever swapped.
package uuidv7spike
