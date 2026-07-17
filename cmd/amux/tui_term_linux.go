//go:build linux

package main

import "golang.org/x/sys/unix"

// termiosGetReq returns the Linux termios get ioctl request (used by the TTY
// probe). Bubble Tea owns raw-mode setup for the interactive program itself.
func termiosGetReq() uint { return unix.TCGETS }
