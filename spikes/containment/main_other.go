//go:build !linux

// This stub keeps the module buildable on the macOS author host. The
// containment spike is Linux-only (cgroup v2); see main_linux.go for the harness
// and the deferred runtime-evidence instructions.
package main

import "fmt"

func main() {
	fmt.Println("containment-spike: Linux-only (cgroup v2). Run on an Arch/Ubuntu host; see main_linux.go header.")
}
