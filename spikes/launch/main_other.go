//go:build !linux

// This stub keeps the module buildable on the macOS author host. The launch
// spike is Linux-only (openat2 + /proc/self/fd exec); see main_linux.go for the
// harness and the deferred runtime-evidence instructions.
package main

import "fmt"

func main() {
	fmt.Println("launch-spike: Linux-only (openat2 + /proc/self/fd exec). Run on an Arch/Ubuntu host; see main_linux.go header.")
}
