package securitytest

import "testing"

// TestSecurityConformance is the manifest's "security-conformance" entry
// point. This package registers no implementation, so today it validates the
// vectors and then SKIPS with the explicit prerequisite — it never pretends
// backend behavior exists. T4 backend re-invokes RunConformance from its own
// test package with a real Factory.
func TestSecurityConformance(t *testing.T) {
	RunConformance(t, nil)
}
