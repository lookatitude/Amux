//go:build !linux

package pty

import "github.com/amux-run/amux/internal/platform"

// PlatformContainment returns nil off Linux: the containment seam is absent,
// so the Supervisor's portable behavior is process-group signaling only. That
// is a TEST baseline for the darwin author host (which runs tests only), NOT
// a platform-support claim — Linux is the sole product platform (ADR-0006),
// and the real containment mechanism plus its deferred runtime evidence live
// behind the linux build tag (see containment_linux.go for the exact
// spikes/containment command).
func PlatformContainment(string) platform.Containment { return nil }
