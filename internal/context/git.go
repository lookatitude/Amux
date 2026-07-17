package context

import (
	stdctx "context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Git collector defaults. Each of the three probe commands gets its own
// timeout; output beyond the bound is an explicit error, never a silent
// truncation (a half-read root path must not masquerade as a real one).
const (
	gitDefaultTimeout   = 2 * time.Second
	gitDefaultMaxOutput = 64 * 1024
)

// ErrGitOutputTruncated reports a git probe whose output exceeded the bound.
var ErrGitOutputTruncated = errors.New("context: git output exceeded bound")

// GitInfo is one pane's git discovery result. Present=false is the clean
// "no git here" answer for a cwd outside any repository — it is NOT an error,
// because panes are probed independently and a workspace carries no
// repository assumption (a single workspace may span several repos or none).
type GitInfo struct {
	Present bool
	Root    string
	Branch  string // empty when undeterminable (e.g. unborn HEAD)
	Dirty   bool
}

// GitCollector discovers a pane's repository context by running the real git
// binary against the pane's OWN cwd:
//
//	git -C <cwd> rev-parse --show-toplevel   → Present + Root
//	git -C <cwd> rev-parse --abbrev-ref HEAD → Branch
//	git -C <cwd> status --porcelain -unormal → Dirty
//
// Every invocation is bounded: per-command timeout, bounded output
// (truncation is an error), no stdin, and an explicit minimal environment
// (PATH and HOME only) so nothing secret-shaped from the daemon environment
// reaches the child.
type GitCollector struct {
	// Timeout bounds each git command (default 2s).
	Timeout time.Duration
	// MaxOutput bounds each command's stdout in bytes (default 64 KiB).
	MaxOutput int
}

// Collect probes cwd. A cwd outside any repository yields GitInfo{Present:
// false} with a nil error; a missing git binary, timeout, or truncated output
// is an error.
func (g GitCollector) Collect(ctx stdctx.Context, cwd string) (GitInfo, error) {
	root, ok, err := g.run(ctx, cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return GitInfo{}, err
	}
	if !ok {
		return GitInfo{}, nil // clean "no git" result
	}
	info := GitInfo{Present: true, Root: root}

	branch, ok, err := g.run(ctx, cwd, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return GitInfo{}, err
	}
	if ok {
		info.Branch = branch
	}

	status, ok, err := g.run(ctx, cwd, "status", "--porcelain", "-unormal")
	if err != nil {
		return GitInfo{}, err
	}
	if ok {
		info.Dirty = status != ""
	}
	return info, nil
}

// run executes one bounded git command. ok=false with a nil error means git
// answered "no" (non-zero exit — e.g. not a repository, unborn HEAD); an
// error means the probe itself failed (missing binary, deadline, truncation).
func (g GitCollector) run(ctx stdctx.Context, cwd string, args ...string) (out string, ok bool, err error) {
	timeout := g.Timeout
	if timeout <= 0 {
		timeout = gitDefaultTimeout
	}
	maxOut := g.MaxOutput
	if maxOut <= 0 {
		maxOut = gitDefaultMaxOutput
	}
	cctx, cancel := stdctx.WithTimeout(ctx, timeout)
	defer cancel()

	path, err := exec.LookPath("git")
	if err != nil {
		return "", false, fmt.Errorf("context: git not found: %w", err)
	}
	stdout := &boundedBuffer{max: maxOut}
	cmd := exec.CommandContext(cctx, path, append([]string{"-C", cwd}, args...)...)
	cmd.Stdin = nil
	cmd.Stdout = stdout
	cmd.Stderr = io.Discard
	cmd.Env = minimalGitEnv()

	runErr := cmd.Run()
	if stdout.truncated {
		return "", false, fmt.Errorf("%w: git %s produced more than %d bytes", ErrGitOutputTruncated, args[0], maxOut)
	}
	if runErr != nil {
		if cctx.Err() != nil {
			return "", false, fmt.Errorf("context: git %s timed out after %s: %w", args[0], timeout, cctx.Err())
		}
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			return "", false, nil // structured "no" (not a repo / unborn HEAD)
		}
		return "", false, fmt.Errorf("context: git %s: %w", args[0], runErr)
	}
	return strings.TrimRight(stdout.String(), "\n"), true, nil
}

// minimalGitEnv is the explicit child environment: locating git helpers (PATH)
// and per-user git config (HOME) only.
func minimalGitEnv() []string {
	env := make([]string, 0, 2)
	if v, ok := os.LookupEnv("PATH"); ok {
		env = append(env, "PATH="+v)
	}
	if v, ok := os.LookupEnv("HOME"); ok {
		env = append(env, "HOME="+v)
	}
	return env
}

// boundedBuffer accepts up to max bytes and then errors, aborting the exec
// output copy so an over-limit child is cut off rather than buffered.
type boundedBuffer struct {
	buf       strings.Builder
	max       int
	truncated bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if b.buf.Len()+len(p) > b.max {
		b.truncated = true
		return 0, ErrGitOutputTruncated
	}
	return b.buf.Write(p)
}

func (b *boundedBuffer) String() string { return b.buf.String() }
