package securitytest

// gatebind.go is the deterministic self-gate behind the readiness manifest's
// `-run` commands (G-lane F5). The frozen `trust-matrix-replay` check once
// shipped a pattern that matched ZERO tests, and `integration-second-uid`
// once pointed at a stub that skipped unconditionally — `go test` exits 0 in
// both situations, so the gates passed vacuously. This file turns "the
// command binds to real tests" into a machine check: it parses each manifest
// command, enumerates the test functions the pattern matches by scanning
// source files under the module root with build constraints applied for the
// check's target GOOS (no test execution, no toolchain subprocess), and the
// tests in gatebind_test.go fail the securitytest self-gate when any bound
// pattern resolves to zero test functions. Receipt guidance
// (security-readiness.md §6) adds the runtime half: a `pass` receipt must
// show at least one substantive PASS and zero unexpected skips.

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// RunBinding is the test-binding half of a manifest `go test` command: the
// -run pattern, the build tags it runs under, and the package patterns it
// targets.
type RunBinding struct {
	Pattern  string
	Tags     []string
	Packages []string
}

// ParseRunBinding extracts a RunBinding from a manifest command. ok is false
// for commands that are not `go test` invocations with a -run pattern
// (scanner commands, manual checks, full-suite runs) — those have no
// pattern-binding to validate.
func ParseRunBinding(command string) (RunBinding, bool) {
	fields := strings.Fields(command)
	if len(fields) < 2 || fields[0] != "go" || fields[1] != "test" {
		return RunBinding{}, false
	}
	var b RunBinding
	for i := 2; i < len(fields); i++ {
		switch {
		case fields[i] == "-tags" && i+1 < len(fields):
			b.Tags = strings.Split(fields[i+1], ",")
			i++
		case fields[i] == "-run" && i+1 < len(fields):
			b.Pattern = strings.Trim(fields[i+1], "'\"")
			i++
		case strings.HasPrefix(fields[i], "./"):
			b.Packages = append(b.Packages, fields[i])
		}
	}
	if b.Pattern == "" {
		return RunBinding{}, false
	}
	return b, true
}

// EnumerateBoundTests returns the top-level Test functions the binding's
// pattern matches, scanning the binding's package patterns under moduleRoot
// with build constraints evaluated for goos (linux for release-gate checks).
// Only the pattern's top-level segment is matched, mirroring `go test -run`
// semantics for subtests.
func EnumerateBoundTests(moduleRoot string, b RunBinding, goos string) ([]string, error) {
	top := strings.SplitN(b.Pattern, "/", 2)[0]
	re, err := regexp.Compile(top)
	if err != nil {
		return nil, fmt.Errorf("securitytest: -run pattern %q: %w", b.Pattern, err)
	}

	ctxt := build.Default
	ctxt.GOOS = goos
	ctxt.CgoEnabled = false // ADR-0007: every gate runs without cgo
	ctxt.BuildTags = append([]string(nil), b.Tags...)

	var dirs []string
	for _, pkg := range b.Packages {
		switch {
		case pkg == "./...":
			all, err := moduleGoDirs(moduleRoot)
			if err != nil {
				return nil, err
			}
			dirs = append(dirs, all...)
		default:
			dirs = append(dirs, filepath.Join(moduleRoot, filepath.FromSlash(strings.TrimPrefix(pkg, "./"))))
		}
	}

	seen := map[string]bool{}
	var out []string
	fset := token.NewFileSet()
	for _, dir := range dirs {
		p, err := ctxt.ImportDir(dir, 0)
		if err != nil {
			if _, ok := err.(*build.NoGoError); ok {
				continue
			}
			return nil, fmt.Errorf("securitytest: scan %s: %w", dir, err)
		}
		for _, file := range append(append([]string(nil), p.TestGoFiles...), p.XTestGoFiles...) {
			path := filepath.Join(dir, file)
			f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if err != nil {
				return nil, fmt.Errorf("securitytest: parse %s: %w", path, err)
			}
			for _, name := range testFuncNames(f) {
				if re.MatchString(name) && !seen[dir+":"+name] {
					seen[dir+":"+name] = true
					out = append(out, name)
				}
			}
		}
	}
	return out, nil
}

// moduleGoDirs walks moduleRoot collecting every directory that holds .go
// files, skipping VCS/tooling trees and nested modules (their tests are not
// reachable from this module's `./...`).
func moduleGoDirs(moduleRoot string) ([]string, error) {
	skip := map[string]bool{".git": true, ".guild": true, ".tools": true, ".amux-artifacts": true,
		"research": true, "vendor": true, "node_modules": true}
	var dirs []string
	err := filepath.WalkDir(moduleRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skip[d.Name()] && path != moduleRoot {
				return filepath.SkipDir
			}
			if path != moduleRoot {
				if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
					return filepath.SkipDir // nested module
				}
			}
			hasGo, err := dirHasGoFiles(path)
			if err != nil {
				return err
			}
			if hasGo {
				dirs = append(dirs, path)
			}
		}
		return nil
	})
	return dirs, err
}

func dirHasGoFiles(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") {
			return true, nil
		}
	}
	return false, nil
}

// testFuncNames returns the top-level TestXxx function names of a parsed file
// (mirroring `go test` discovery: a niladic-receiver func whose name starts
// with Test followed by a non-lowercase rune, taking exactly one parameter).
func testFuncNames(f *ast.File) []string {
	var out []string
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil {
			continue
		}
		name := fn.Name.Name
		if !strings.HasPrefix(name, "Test") {
			continue
		}
		if len(name) > 4 {
			if r := rune(name[4]); r >= 'a' && r <= 'z' {
				continue
			}
		}
		if fn.Type.Params == nil || len(fn.Type.Params.List) != 1 {
			continue
		}
		out = append(out, name)
	}
	return out
}
