//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestAliveTreatsZombieAsDead(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Wait() })

	deadline := time.Now().Add(2 * time.Second)
	for testProcessState(cmd.Process.Pid) != "Z" {
		if time.Now().After(deadline) {
			t.Fatalf("pid %d never entered zombie state", cmd.Process.Pid)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if alive(cmd.Process.Pid) {
		t.Fatalf("alive(%d) = true for zombie; want false", cmd.Process.Pid)
	}
}

func testProcessState(pid int) string {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return ""
	}
	end := strings.LastIndexByte(string(data), ')')
	if end < 0 {
		return ""
	}
	fields := strings.Fields(string(data[end+1:]))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}
