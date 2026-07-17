//go:build unix

package local

import (
	"fmt"
	"os"
	"syscall"
)

// fileOwner extracts the owning UID from an os.Lstat result on Unix platforms
// (Linux is the supported target; Darwin works for the authoring-host tests).
// The path argument exists for the test seam signature and diagnostics only —
// the production extraction reads the already-fetched stat, so no extra
// filesystem access (and no TOCTOU window) is introduced here.
func fileOwner(path string, fi os.FileInfo) (uint32, error) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("amux transport: no unix stat for %s", path)
	}
	return st.Uid, nil
}
