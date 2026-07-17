package securitytest

import (
	"path/filepath"
	"testing"
)

func repoRoot() string { return filepath.Join(sourceDir(), "..", "..") }

// TestManifestRunPatternsBindToRealTests is the F5 self-gate: every readiness
// check whose command carries a `-run` expression must bind to at least one
// real test function under the check's build tags on the Linux release
// target. A pattern that matches nothing lets `go test` exit 0 with "no tests
// to run" — a vacuous pass this gate makes impossible to freeze again.
func TestManifestRunPatternsBindToRealTests(t *testing.T) {
	m := loadManifest(t)
	bound := 0
	for _, c := range m.Checks {
		b, ok := ParseRunBinding(c.Command)
		if !ok {
			continue
		}
		bound++
		names, err := EnumerateBoundTests(repoRoot(), b, "linux")
		if err != nil {
			t.Errorf("check %s: enumerating bound tests: %v", c.ID, err)
			continue
		}
		if len(names) == 0 {
			t.Errorf("check %s: -run %q (tags %v) binds to ZERO tests — the frozen gate would pass vacuously", c.ID, b.Pattern, b.Tags)
			continue
		}
		t.Logf("check %s: -run %q binds to %d test(s): %v", c.ID, b.Pattern, len(names), names)
	}
	if bound == 0 {
		t.Fatal("no -run-bearing checks found; the manifest shape changed under the self-gate")
	}
}

// TestRetiredForeignUIDStubStaysRetired pins the F5 remediation: WITHOUT the
// integration tag, the `SecondUID` pattern must bind to nothing on any
// platform. The retired TestSecondUIDVariantsDeferred stub skipped
// unconditionally, so an untagged (or wrongly-tagged) run of the frozen
// command could count it and pass vacuously; this gate fails if any such
// untagged binding ever reappears.
func TestRetiredForeignUIDStubStaysRetired(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		names, err := EnumerateBoundTests(repoRoot(),
			RunBinding{Pattern: "SecondUID", Packages: []string{"./..."}}, goos)
		if err != nil {
			t.Fatalf("%s: %v", goos, err)
		}
		if len(names) != 0 {
			t.Errorf("%s: untagged SecondUID bindings exist (%v); the second-UID gate must only ever count the real integration harness", goos, names)
		}
	}
	// And WITH the tag on Linux the real harness must be there.
	names, err := EnumerateBoundTests(repoRoot(),
		RunBinding{Pattern: "SecondUID", Tags: []string{"integration"}, Packages: []string{"./..."}}, "linux")
	if err != nil {
		t.Fatal(err)
	}
	if len(names) < 4 {
		t.Errorf("integration-tagged SecondUID harness shrank: %v (want the 4 STR-3/STR-4 foreign-owner cases)", names)
	}
}

// TestParseRunBinding pins the command-parsing half of the self-gate.
func TestParseRunBinding(t *testing.T) {
	b, ok := ParseRunBinding("go test -count=1 -tags integration -run 'TrustMatrixReplay' ./...")
	if !ok || b.Pattern != "TrustMatrixReplay" || len(b.Tags) != 1 || b.Tags[0] != "integration" ||
		len(b.Packages) != 1 || b.Packages[0] != "./..." {
		t.Fatalf("parsed binding = %+v ok=%v", b, ok)
	}
	if _, ok := ParseRunBinding("go mod verify"); ok {
		t.Fatal("non-test command parsed as a run binding")
	}
	if _, ok := ParseRunBinding("go test -count=1 ./internal/securitytest/"); ok {
		t.Fatal("run-less test command parsed as a run binding")
	}
	if _, ok := ParseRunBinding("manual: execute threat-model.md §5 rows"); ok {
		t.Fatal("manual check parsed as a run binding")
	}
}
