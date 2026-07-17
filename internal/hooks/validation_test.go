package hooks_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	v1 "github.com/amux-run/amux/api/v1"

	"github.com/amux-run/amux/internal/control"
	"github.com/amux-run/amux/internal/hooks"
	"github.com/amux-run/amux/internal/platform"
)

// pinnedFS pins (dev, ino) so the durable key and the registered identity
// tuple stay IDENTICAL across a physical root replacement — the deterministic
// reproduction of the overlayfs inode-reuse class. Everything else in this
// test is real: real directories, real hook/config files, the real
// replacement validator, the real actor, and the real launch pipeline.
type pinnedFS struct{ dev, ino uint64 }

func (f pinnedFS) Identify(path string) (string, platform.FSIdentity, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", platform.FSIdentity{}, err
	}
	return abs, platform.FSIdentity{Dev: f.dev, Ino: f.ino}, nil
}

// TestPreLaunchRevalidationDetectsReplacedRoot pins the G-lane F2 pre-launch
// half: an EVIL-TWIN replacement — the root recreated with byte-identical
// hook and config content, under a reused (dev, ino) — must still be denied
// at the final pre-spawn authorization, because the root's birth-time
// discriminator no longer matches the one captured at registration. Digest
// checks cannot catch this (the bytes are identical); only replacement
// validation can.
func TestPreLaunchRevalidationDetectsReplacedRoot(t *testing.T) {
	ctx := context.Background()
	fs := pinnedFS{dev: 7, ino: 42}
	ctrl := control.New(control.Deps{
		Store: control.NewMemStore(),
		Clock: platform.NewFakeClock(1_000),
		FS:    fs,
		// Validator defaults to the production resolver.
	})
	ctrl.Start()
	t.Cleanup(ctrl.Stop)

	clock := hooks.NewSchedClock(1_000_000)
	rt, err := hooks.New(ctx, hooks.Config{
		Control:    ctrl,
		Clock:      clock,
		Launcher:   hooks.NewSpyLauncher(),
		FS:         fs,
		ScratchDir: filepath.Join(t.TempDir(), "scratch"),
	})
	if err != nil {
		t.Fatal(err)
	}

	base := t.TempDir()
	root := filepath.Join(base, "project")
	hookBody := []byte("#!/bin/sh\necho hi\n")
	cfgBody := []byte("{\n  \"hooks\": []\n}\n")
	writeRoot := func() {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(root, ".amux"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "hook.sh"), hookBody, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, ".amux", "hooks.jsonc"), cfgBody, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeRoot()

	key, err := ctrl.RegisterProject(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ctrl.ApproveProject(ctx, "s1", key); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.ApproveHook(ctx, "s1", key, hooks.GrantRequest{
		HookID:        "h1",
		ExecPath:      filepath.Join(root, "hook.sh"),
		AllowedEvents: []string{"pane_exit"},
		Scope:         control.ScopeNone,
		TimeoutMS:     2000,
		OutputCap:     1 << 20,
	}); err != nil {
		t.Fatal(err)
	}

	activate := func() hooks.Outcome {
		t.Helper()
		id, err := rt.Activate(ctx, hooks.ActivationRequest{
			Project: key, Hook: "h1", Event: "pane_exit", Session: "s1",
		})
		if err != nil {
			t.Fatal(err)
		}
		out, err := awaitOutcome(ctx, rt, id)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	if out := activate(); !out.Allow {
		t.Fatalf("baseline activation denied: %+v", out)
	}

	// Evil twin: replace the root, recreate byte-identical content. The
	// pinned FS keeps (dev, ino) — and therefore the key and the registered
	// identity tuple — unchanged; only the birth time differs.
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	writeRoot()

	out := activate()
	if out.Allow {
		t.Fatal("evil-twin replaced root was authorized; pre-launch replacement validation must fail closed")
	}
	if out.Code != v1.ErrProjectTrustRequired {
		t.Fatalf("deny code = %s, want %s", out.Code, v1.ErrProjectTrustRequired)
	}
}

// awaitOutcome bounds Await so a wedged pipeline fails the test instead of
// hanging it.
func awaitOutcome(ctx context.Context, rt *hooks.Runtime, id string) (hooks.Outcome, error) {
	var (
		out  hooks.Outcome
		err  error
		done = make(chan struct{})
	)
	go func() {
		defer close(done)
		out, err = rt.Await(ctx, id)
	}()
	select {
	case <-done:
		return out, err
	case <-time.After(10 * time.Second):
		return hooks.Outcome{}, context.DeadlineExceeded
	}
}
