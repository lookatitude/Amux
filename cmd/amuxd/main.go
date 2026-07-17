// Command amuxd is the Amux daemon: the single durable authority (ADR-0001)
// serving the local control protocol on an owner-only XDG runtime socket. All
// runtime assembly lives in internal/daemon.Run so tests exercise the exact
// production wiring in-process; this wrapper owns only flags and the process
// exit code.
//
// Logging discipline (PRD F10): the daemon writes diagnostics only to stderr
// via slog, never to stdout, so it can never corrupt a machine protocol
// stream or a TUI sharing the terminal.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/amux-run/amux/internal/daemon"
	"github.com/amux-run/amux/internal/version"
)

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Fprintln(os.Stdout, version.String())
		return
	}

	if err := daemon.Run(context.Background(), daemon.RunOptions{}); err != nil {
		fmt.Fprintln(os.Stderr, "amuxd:", err)
		os.Exit(1)
	}
}
