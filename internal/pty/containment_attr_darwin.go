//go:build darwin

package pty

import (
	"syscall"

	"github.com/amux-run/amux/internal/platform"
)

func applyContainmentAttrs(*syscall.SysProcAttr, platform.PTYSpec) {}
