//go:build linux

package pty

import (
	"syscall"
	"testing"

	"github.com/amux-run/amux/internal/platform"
)

func TestApplyContainmentAttrsUsesAtomicCgroupPlacement(t *testing.T) {
	attr := &syscall.SysProcAttr{}
	applyContainmentAttrs(attr, platform.PTYSpec{UseCgroupFD: true, CgroupFD: 42})

	if attr.Pdeathsig != syscall.SIGKILL {
		t.Fatalf("Pdeathsig = %v; want SIGKILL", attr.Pdeathsig)
	}
	if !attr.UseCgroupFD || attr.CgroupFD != 42 {
		t.Fatalf("cgroup attrs = use:%v fd:%d; want use:true fd:42", attr.UseCgroupFD, attr.CgroupFD)
	}
}
