package securitytest

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestFixtureVectorsAreWellFormed(t *testing.T) {
	fixtures, err := LoadFixtures(FixtureDir())
	if err != nil {
		t.Fatalf("loading fixture vectors: %v", err)
	}
	if err := ValidateFixtures(fixtures); err != nil {
		t.Fatal(err)
	}
}

// TestRedactionFixturesContainNoRawSecrets proves the derived-secret
// discipline (secrets.go): no durable artifact under docs/security or
// testdata/security contains a derived candidate secret value, nor any
// credential-shaped AMUXTEST_ remnant. Golden files carry placeholders and
// markers only.
func TestRedactionFixturesContainNoRawSecrets(t *testing.T) {
	fixtures, err := LoadFixtures(FixtureDir())
	if err != nil {
		t.Fatalf("loading fixture vectors: %v", err)
	}
	var derived []string
	for _, f := range fixtures.Redaction {
		derived = append(derived, DeriveCandidateSecret(f.Label))
	}
	remnant := regexp.MustCompile(CandidateSecretPrefix + `[0-9a-fA-F]{8,}`)

	roots := []string{
		filepath.Join(sourceDir(), "..", "..", "docs", "security"),
		filepath.Join(sourceDir(), "..", "..", "testdata", "security"),
	}
	checked := 0
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			s := string(raw)
			for _, sec := range derived {
				if strings.Contains(s, sec) {
					t.Errorf("%s: contains a derived candidate secret value", path)
				}
			}
			if m := remnant.FindString(s); m != "" {
				t.Errorf("%s: contains credential-shaped remnant %q", path, m)
			}
			checked++
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", root, err)
		}
	}
	if checked == 0 {
		t.Fatal("scanned zero files — walk roots are wrong")
	}
}
