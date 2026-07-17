//go:build darwin || linux

package observability

import (
	"fmt"
	"os"
	"syscall"
)

// validateOwnerOnlyDir enforces the private-runtime-dir posture on the pprof
// socket's parent: it must be a real directory (Lstat, so a symlinked parent
// is rejected), owned by the current effective user, with no group/other
// permission bits. This mirrors the LocalTransport rule in ADR-0006 —
// owner-only local surfaces validate their runtime path, they do not trust it.
func validateOwnerOnlyDir(dir string) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("observability: inspecting pprof socket dir %q: %w", dir, err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("observability: pprof socket dir %q is not a directory (mode %v)", dir, fi.Mode())
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("observability: pprof socket dir %q: cannot read ownership", dir)
	}
	if uid := uint32(os.Geteuid()); st.Uid != uid {
		return fmt.Errorf("observability: pprof socket dir %q is owned by uid %d, not the daemon owner uid %d", dir, st.Uid, uid)
	}
	if perm := fi.Mode().Perm(); perm&0o077 != 0 {
		return fmt.Errorf("observability: pprof socket dir %q mode %o grants group/other access; an owner-only (0700) directory is required", dir, perm)
	}
	return nil
}
