//go:build linux

package platform

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// linuxPeerCredentials implements mandatory SO_PEERCRED validation for the
// owner-only control socket (PRD F4; ADR-0006). Every accepted connection must
// pass this check; a UID mismatch with the daemon owner rejects the peer before
// any command is read.
//
// This compiles under GOOS=linux; end-to-end validation (a second-UID socket
// attempt is rejected) is a T2/T6 security fixture on a Linux host.
type linuxPeerCredentials struct{}

// NewLinuxPeerCredentials returns the SO_PEERCRED-backed validator.
func NewLinuxPeerCredentials() PeerCredentials { return linuxPeerCredentials{} }

func (linuxPeerCredentials) PeerUID(rawConnFD uintptr) (uint32, error) {
	ucred, err := unix.GetsockoptUcred(int(rawConnFD), unix.SOL_SOCKET, unix.SO_PEERCRED)
	if err != nil {
		return 0, fmt.Errorf("getsockopt SO_PEERCRED: %w", err)
	}
	return ucred.Uid, nil
}
