//go:build darwin

package main

import "golang.org/x/sys/unix"

// termiosGetReq returns the darwin termios get ioctl request (used by the TTY
// probe). darwin is a developer-host build target only (Amux is Linux-first);
// this keeps the interactive TUI compilable and testable on the author host.
func termiosGetReq() uint { return unix.TIOCGETA }
