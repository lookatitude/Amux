//go:build linux

package pty

import "github.com/amux-run/amux/internal/platform"

// PlatformContainment wires the real Linux containment mechanism
// (cgroup-v2 subtree + PR_SET_PDEATHSIG fast path, ADR-0006 §containment)
// into Supervisor construction: pass its result as
// SupervisorConfig.Containment so every spawn is enrolled and Stop
// escalation/StopAll can KillTree the whole descendant subtree, including
// double-forked grandchildren that escaped their process group.
//
// cgroupRoot is the delegated cgroup-v2 subtree amuxd may write to (e.g.
// /sys/fs/cgroup/amux.<pid>). An empty root yields pdeathsig-only reduced
// containment whose KillTree fails closed (the Supervisor then falls back to
// group SIGKILL and logs the downgrade — never a silent degradation).
//
// LINUX RUNTIME EVIDENCE (ADR-0006): this behavior cannot be inferred from the
// macOS author host and MUST remain green on a cgroup-v2 Linux host with:
//
//	sudo AMUX_CGROUP_ROOT=/sys/fs/cgroup/amux-spike go run ./spikes/containment
//
// PASS (exit 0): the child is placed atomically in the cgroup, the inherited
// grandchild PID is captured, cgroup.kill terminates it, and the cgroup is
// removed. This is a blocking CI job; no Linux-runtime claim is inferred from
// an author-host-only run.
func PlatformContainment(cgroupRoot string) platform.Containment {
	return platform.NewLinuxContainment(cgroupRoot)
}
