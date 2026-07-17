//go:build !linux && !darwin

package main

// Amux is Linux-first (darwin is a developer-host target). On any other
// platform the interactive Bubble Tea program is reported as unavailable via
// stdoutIsTTY=false, so `amux tui` prints the non-interactive guidance; `amux
// tui --preview` and every `amux` subcommand remain available.

func terminalSize() (int, int) { return 80, 24 }

func stdoutIsTTY() bool { return false }
