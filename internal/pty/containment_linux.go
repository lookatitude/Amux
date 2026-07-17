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
// DEFERRED RUNTIME EVIDENCE (ADR-0006 §"Deferred Linux evidence"): this file
// compiles under GOOS=linux, but the kill-tree runtime behavior cannot be
// proven on the macOS author host and MUST be validated on a cgroup-v2 Linux
// host with:
//
//	sudo AMUX_CGROUP_ROOT=/sys/fs/cgroup/amux-spike go run ./spikes/containment
//
// PASS (exit 0): grandchild PID captured; after cgroup.kill the grandchild is
// not alive; cgroup removed. FAIL if the grandchild survives. This becomes a
// blocking T3/T6 CI job; no claim of passing containment runtime behavior is
// made from the author host.
func PlatformContainment(cgroupRoot string) platform.Containment {
	return platform.NewLinuxContainment(cgroupRoot)
}
