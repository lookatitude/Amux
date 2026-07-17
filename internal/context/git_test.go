package context

import (
	stdctx "context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// requireGit skips the integration test gracefully when no git binary exists
// on the host.
func requireGit(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available on this host; integration test skipped")
	}
	return path
}

// gitCmd runs a git setup command for fixture building (test-only; the
// collector under test builds its own bounded invocations).
func gitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{"-C", dir,
		"-c", "user.name=amux-test", "-c", "user.email=amux-test@example.invalid"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// makeRepo creates a real repository at dir with one commit on branch.
func makeRepo(t *testing.T, dir, branch string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, dir, "init", "--quiet")
	gitCmd(t, dir, "symbolic-ref", "HEAD", "refs/heads/"+branch)
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("tracked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, dir, "add", "file.txt")
	gitCmd(t, dir, "commit", "--quiet", "-m", "init")
}

// realPath resolves symlinks so macOS /tmp → /private/tmp indirection cannot
// break root comparisons.
func realPath(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TestGitCollectorTwoReposOneWorkspace is the B10 integration proof: two
// separate real repositories inside ONE workspace directory are discovered
// independently per pane cwd (no workspace-wide repository assumption), and a
// non-repo cwd yields the clean "no git" result rather than an error.
func TestGitCollectorTwoReposOneWorkspace(t *testing.T) {
	requireGit(t)
	ctx := stdctx.Background()
	workspace := t.TempDir()
	repoClean := filepath.Join(workspace, "repo-clean")
	repoDirty := filepath.Join(workspace, "repo-dirty")
	makeRepo(t, repoClean, "main")
	makeRepo(t, repoDirty, "feature")
	// Dirty the second repo: modify a tracked file and add an untracked one
	// (-unormal counts untracked files as dirt).
	if err := os.WriteFile(filepath.Join(repoDirty, "file.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDirty, "untracked.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var g GitCollector

	clean, err := g.Collect(ctx, repoClean)
	if err != nil {
		t.Fatalf("collect clean repo: %v", err)
	}
	if !clean.Present || clean.Root != realPath(t, repoClean) || clean.Branch != "main" || clean.Dirty {
		t.Fatalf("clean repo = %+v, want Present root=%s branch=main dirty=false", clean, realPath(t, repoClean))
	}

	// Probe from a subdirectory too: root discovery must walk up within the
	// pane's own repo only.
	sub := filepath.Join(repoDirty, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	dirty, err := g.Collect(ctx, sub)
	if err != nil {
		t.Fatalf("collect dirty repo: %v", err)
	}
	if !dirty.Present || dirty.Root != realPath(t, repoDirty) || dirty.Branch != "feature" || !dirty.Dirty {
		t.Fatalf("dirty repo = %+v, want Present root=%s branch=feature dirty=true", dirty, realPath(t, repoDirty))
	}

	// The workspace root itself is not a repository: clean "no git", nil error.
	none, err := g.Collect(ctx, workspace)
	if err != nil {
		t.Fatalf("non-repo cwd must not error: %v", err)
	}
	if none != (GitInfo{}) {
		t.Fatalf("non-repo cwd = %+v, want zero GitInfo (Present=false)", none)
	}
}

func TestGitCollectorTruncationIsErrorNotSilent(t *testing.T) {
	requireGit(t)
	repo := filepath.Join(t.TempDir(), "repo")
	makeRepo(t, repo, "main")

	g := GitCollector{MaxOutput: 4} // any real toplevel path exceeds this
	_, err := g.Collect(stdctx.Background(), repo)
	if !errors.Is(err, ErrGitOutputTruncated) {
		t.Fatalf("err = %v, want ErrGitOutputTruncated (truncation must be loud)", err)
	}
}

func TestGitCollectorMissingDirectoryIsNoGit(t *testing.T) {
	requireGit(t)
	// git -C <missing> exits non-zero → structured "no git", not an error is
	// too generous; -C on a missing dir is a usage failure (exit status), which
	// the collector maps to the clean no-git result. Either way it must not
	// wedge or panic.
	info, err := GitCollector{}.Collect(stdctx.Background(), filepath.Join(t.TempDir(), "gone"))
	if err != nil {
		t.Fatalf("missing cwd: %v", err)
	}
	if info.Present {
		t.Fatalf("missing cwd reported a repository: %+v", info)
	}
}
