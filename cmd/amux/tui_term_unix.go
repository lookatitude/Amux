//go:build linux || darwin

package main

import (
	"os"

	"golang.org/x/sys/unix"
)

// terminalSize returns the current stdout terminal size, defaulting to 80×24
// when it cannot be determined (never returns zero, which would corrupt the
// initial layout before Bubble Tea delivers the first WindowSizeMsg). Bubble
// Tea owns raw mode, input decoding, and SIGWINCH resize once the program runs.
func terminalSize() (cols, rows int) {
	ws, err := unix.IoctlGetWinsize(int(os.Stdout.Fd()), unix.TIOCGWINSZ)
	if err != nil || ws.Col == 0 || ws.Row == 0 {
		return 80, 24
	}
	return int(ws.Col), int(ws.Row)
}

// stdoutIsTTY reports whether stdout is an interactive terminal.
func stdoutIsTTY() bool {
	_, err := unix.IoctlGetTermios(int(os.Stdout.Fd()), termiosGetReq())
	return err == nil
}
