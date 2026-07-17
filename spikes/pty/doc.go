// Package ptyspike is a throwaway architectural spike (work package A6). Its
// CONCLUSION is frozen into docs/adr/0006-platform-interfaces.md (the narrow PTY
// interface backed by github.com/creack/pty) and
// docs/adr/0007-dependency-and-compatibility-policy.md. It is retained as
// executable evidence that creack/pty opens a real PTY, spawns a child, and
// round-trips output on the author host — the small, cgo-free Unix primitive the
// PTY interface wraps.
package ptyspike
