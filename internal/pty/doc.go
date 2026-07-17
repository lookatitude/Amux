// Package pty implements the platform.PTY seam (ADR-0006 §PTY) and the
// session-side process Supervisor built on top of it (work package B5).
//
// The PTY implementation wraps github.com/creack/pty; no package above the
// platform seam sees creack/pty, termios, or ioctl types — Start returns a
// platform.PTYHandle and nothing else. Children are launched in a fresh
// session (Setsid) with the new PTY as the controlling terminal, with an
// explicit environment only (PTYSpec.Env verbatim — the daemon environment is
// never inherited; PRD least-exposed environment), and signals target the
// child's whole process group so job trees die together.
//
// The Supervisor owns one live process per id: it pumps master output to the
// OnOutput callback, reaps exactly once, and reports termination through the
// OnExit callback exactly once with a reason ("exited", "signaled", "stopped",
// "daemon_shutdown") — the evented-once exit contract of ADR-0004. Stop is
// graceful-then-forceful: SIGTERM to the process group, then SIGKILL
// escalation after the configured grace period. StopAll is the daemon-shutdown
// path and must leave zero orphans, which OrphanScan lets tests and the T6
// harness verify.
//
// Containment: when a platform.Containment is supplied, the Supervisor
// prepares a containment handle per spawn and uses KillTree on escalation, so
// on Linux the cgroup-v2 mechanism reaps double-forked grandchildren too. On
// non-Linux hosts the containment seam is absent (nil) and the Supervisor's
// portable behavior is process-group signaling only — that is a TEST baseline
// for the author host, not a platform-support claim. Linux containment
// runtime evidence is deferred per ADR-0006; see containment_linux.go for the
// exact deferred harness command.
package pty
