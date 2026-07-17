//go:build linux

package pty

import (
	"syscall"

	"github.com/amux-run/amux/internal/platform"
)

func applyContainmentAttrs(attr *syscall.SysProcAttr, spec platform.PTYSpec) {
	attr.Pdeathsig = syscall.SIGKILL
	if spec.UseCgroupFD {
		attr.UseCgroupFD = true
		attr.CgroupFD = spec.CgroupFD
	}
}
