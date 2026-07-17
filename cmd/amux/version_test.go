// version_test.go pins the version identity contract: the root `amux
// --version` flag (required by the frozen install smoke and the rollback
// runbook) reports the same stamped version/protocol truth as the `amux
// version` subcommand and `amuxd --version`, exits zero WITHOUT dialing the
// daemon, and prints deterministically to stdout.
package main

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"testing"

	"github.com/amux-run/amux/internal/version"
)

// runRoot executes the CLI in-process with args and returns stdout, stderr,
// and the Execute error. No daemon exists behind these tests: any code path
// that dials would fail, so success proves the daemon is never contacted.
func runRoot(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	a := &app{}
	root := a.rootCmd()
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), errb.String(), err
}

func TestRootVersionFlag(t *testing.T) {
	out, stderr, err := runRoot(t, "--version")
	if err != nil {
		t.Fatalf("amux --version: %v (stderr %q)", err, stderr)
	}
	if want := version.String() + "\n"; out != want {
		t.Fatalf("amux --version stdout = %q, want exactly %q", out, want)
	}
}

// TestRootVersionFlagMatchesVersionSubcommand pins the single-truth rule: the
// flag, the subcommand, and the --json schema all report the same stamped
// identity (internal/version), so amux and amuxd can never disagree.
func TestRootVersionFlagMatchesVersionSubcommand(t *testing.T) {
	flagOut, _, err := runRoot(t, "--version")
	if err != nil {
		t.Fatal(err)
	}
	subOut, _, err := runRoot(t, "version")
	if err != nil {
		t.Fatal(err)
	}
	if flagOut != subOut {
		t.Fatalf("--version prints %q but the version subcommand prints %q", flagOut, subOut)
	}

	jsonOut, _, err := runRoot(t, "version", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Version  string `json:"version"`
		Protocol string `json:"protocol"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &v); err != nil {
		t.Fatalf("version --json output %q: %v", jsonOut, err)
	}
	if v.Version != version.Version || v.Protocol != version.Protocol {
		t.Fatalf("version --json = %+v, want version %q protocol %q", v, version.Version, version.Protocol)
	}
}

// TestRootVersionFlagBlackBox drives the REAL binary with no daemon and no
// runtime dir — the frozen install-smoke invocation (`amux --version` on a
// fresh host, packaging/smoke/smoke-install.sh) must exit zero on stdout.
func TestRootVersionFlagBlackBox(t *testing.T) {
	if testing.Short() {
		t.Skip("execs the built binary")
	}
	base := t.TempDir()
	cmd := exec.Command(amuxBin, "--version")
	cmd.Env = []string{"HOME=" + base, "XDG_RUNTIME_DIR=" + base}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("amux --version (no daemon): %v (stderr %q)", err, errb.String())
	}
	if want := version.String() + "\n"; out.String() != want {
		t.Fatalf("amux --version stdout = %q, want exactly %q", out.String(), want)
	}
}
