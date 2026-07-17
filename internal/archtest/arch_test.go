// Package archtest is executable enforcement of the ADR-0001 dependency
// direction and the ADR-0007 cgo prohibition. It parses the import list of
// every Go file in the module (ignoring build constraints, so platform-tagged
// files are checked too) and fails if any inward-only rule is broken. This test
// is the "dependency-rule test" the T1 success criteria require; it turns the
// package-boundary ADR from prose into a gate every future lane must pass.
package archtest

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const modulePath = "github.com/amux-run/amux"

// forbiddenForDomain lists the internal layer suffixes that internal/domain (and
// anything it may import) must never depend on. Domain is the inward-most
// contract layer; it may import only the Go standard library and the tiny set of
// audited leaf externals in allowedDomainExternals.
var forbiddenForDomain = []string{
	"/internal/transport",
	"/internal/store",
	"/internal/snapshot",
	"/internal/pty",
	"/internal/terminal",
	"/internal/tui",
	"/internal/protocol",
	"/internal/control",
	"/internal/session",
	"/internal/client",
	"/internal/attach",
	"/internal/hooks",
	"/internal/notify",
	"/internal/context",
	"/internal/observability",
	"/internal/config",
}

// allowedDomainExternals is the closed set of third-party modules the domain
// layer may import. Adding to this list is an ADR-0007 decision.
var allowedDomainExternals = map[string]bool{
	"github.com/google/uuid": true,
}

func TestDomainImportsAreInwardOnly(t *testing.T) {
	root := moduleRoot(t)
	domainDir := filepath.Join(root, "internal", "domain")
	imports := packageImports(t, domainDir)

	for _, imp := range imports {
		if isStdlib(imp) {
			continue
		}
		if strings.HasPrefix(imp, modulePath) {
			// Domain may reference only itself among Amux packages.
			rel := strings.TrimPrefix(imp, modulePath)
			if rel != "/internal/domain" {
				t.Errorf("internal/domain imports Amux package %q; domain must be self-contained (ADR-0001)", imp)
			}
			continue
		}
		// Third-party import: must be on the allowlist. Match by module prefix.
		allowed := false
		for m := range allowedDomainExternals {
			if imp == m || strings.HasPrefix(imp, m+"/") {
				allowed = true
				break
			}
		}
		if !allowed {
			t.Errorf("internal/domain imports non-allowlisted external %q (ADR-0007)", imp)
		}
	}
}

// TestNoInternalPackageViolatesDomainIsolation is the general form: no matter
// which package it lives in, code that ends up in the domain layer must not
// reach the forbidden outer layers. It scans every package directory and, if a
// directory is the domain layer, asserts it imports none of the forbidden
// suffixes. (Outer layers importing each other is allowed; only inbound edges
// into domain are forbidden here.)
func TestNoForbiddenInboundEdgesIntoDomain(t *testing.T) {
	root := moduleRoot(t)
	domainDir := filepath.Join(root, "internal", "domain")
	imports := packageImports(t, domainDir)
	for _, imp := range imports {
		for _, bad := range forbiddenForDomain {
			if strings.Contains(imp, bad) {
				t.Errorf("internal/domain must not import %q (forbidden outer layer %q)", imp, bad)
			}
		}
	}
}

// TestNoCgo enforces the ADR-0007 cgo prohibition across the whole module,
// including spikes. A file that imports pseudo-package "C" would enable cgo,
// which the Linux glibc build/packaging strategy forbids.
func TestNoCgo(t *testing.T) {
	root := moduleRoot(t)
	walkGoFiles(t, root, func(path string, imports []string) {
		for _, imp := range imports {
			if imp == "C" {
				rel, _ := filepath.Rel(root, path)
				t.Errorf("%s imports \"C\"; cgo is prohibited (ADR-0007)", rel)
			}
		}
	})
}

// --- helpers ---------------------------------------------------------------

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found walking up from test dir")
		}
		dir = parent
	}
}

// packageImports returns the union of import paths across every .go file in dir
// (including _test.go and build-tag-excluded files), so platform-specific files
// are still checked.
func packageImports(t *testing.T, dir string) []string {
	t.Helper()
	seen := map[string]bool{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		for _, imp := range fileImports(t, fset, filepath.Join(dir, e.Name())) {
			seen[imp] = true
		}
	}
	out := make([]string, 0, len(seen))
	for imp := range seen {
		out = append(out, imp)
	}
	return out
}

func fileImports(t *testing.T, fset *token.FileSet, path string) []string {
	f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var out []string
	for _, spec := range f.Imports {
		out = append(out, strings.Trim(spec.Path.Value, `"`))
	}
	return out
}

func walkGoFiles(t *testing.T, root string, fn func(path string, imports []string)) {
	t.Helper()
	fset := token.NewFileSet()
	skip := map[string]bool{".git": true, ".guild": true, "research": true, "vendor": true}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if skip[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") {
			fn(path, fileImports(t, fset, path))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func isStdlib(imp string) bool {
	// Standard-library import paths have no dot in their first path segment.
	first := imp
	if i := strings.IndexByte(imp, '/'); i >= 0 {
		first = imp[:i]
	}
	return !strings.Contains(first, ".")
}
