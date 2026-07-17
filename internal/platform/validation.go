package platform

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
)

// This file defines the replacement-validation discriminator for project
// roots (G-lane F2 remediation). It is ADDITIVE to — and deliberately outside
// of — the 13 frozen ADR-0006 seams (seam_test.go): the frozen
// FilesystemIdentity interface and the public durable-key definition
// (ComputeProjectKey = SHA-256 of the length-prefixed (realpath, dev, ino)
// tuple) are unchanged.
//
// Why it exists: the durable key alone cannot distinguish a replaced root on
// filesystems that deterministically reuse directory inodes. On overlayfs
// (the container deployment class), `rm -rf root && mkdir root` reuses the
// same (st_dev, st_ino), so the key — and therefore any trust bound to it —
// would survive object replacement, violating the spec's "replacing a root
// invalidates trust" guarantee. The discriminator is a SEPARATELY PERSISTED
// second factor derived from a kernel identity surface that changes exactly
// when the object at the path is recreated:
//
//   - Linux: statx(2) birth time (STATX_BTIME). A recreated directory is a
//     new inode with a new birth time even when the inode NUMBER is reused.
//     Mount identity (stx_mnt_id) was rejected as the primary surface because
//     it changes across reboots of an unchanged root, which would invalidate
//     trust without any replacement; inode generation (FS_IOC_GETVERSION)
//     was rejected because it is privileged/unsupported on common overlay
//     stacks. Filesystems that do not report STATX_BTIME (statx masks it
//     out) and pre-statx kernels (ENOSYS) FAIL CLOSED: no discriminator is
//     fabricated, and callers must deny trust reuse.
//   - Darwin (author/test host, not a supported runtime platform): the
//     native st_birthtimespec, which APFS/HFS+ maintain with the same
//     "changes exactly on recreation" property. A zero birth time fails
//     closed the same way.
//   - Every other platform: ErrValidationUnsupported (fail closed), matching
//     the ADR-0006 placeholder posture.
//
// Semantics contract for consumers (internal/control, internal/hooks):
//   - Ordinary child create/write/remove inside the root NEVER changes the
//     discriminator (directory birth time is immutable under content churn),
//     so trust is never invalidated by normal project work.
//   - A discriminator mismatch against the persisted value means the object
//     at the path is not the object trust was granted to: the consumer must
//     invalidate (revoke) that trust, never transfer it.
//   - An unavailable discriminator (ErrValidationUnsupported) is an
//     AMBIGUOUS identity: consumers must deny trust reuse with a typed,
//     audited failure rather than guessing.
//   - Known conservative edge (documented, fail-closed direction): on
//     overlayfs, the FIRST modification of a root that only exists in a
//     lower layer copies the directory up, giving the upper inode a fresh
//     birth time. Trust granted before that first copy-up is invalidated
//     once and must be re-approved. Invalidating valid trust is the safe
//     direction; the reverse (honoring trust across replacement) is not.

// ValidationID is the persisted replacement-validation discriminator of a
// project root. Scheme names the kernel identity surface that produced Value
// so a persisted discriminator is never compared against one derived from a
// different surface (a scheme mismatch is an identity mismatch).
type ValidationID struct {
	Scheme string
	Value  string
}

// IsZero reports an absent discriminator (e.g. a row persisted before this
// mechanism existed). Absent is ambiguous and must be treated as mismatch.
func (v ValidationID) IsZero() bool { return v.Scheme == "" && v.Value == "" }

// ErrValidationUnsupported is returned when no reliable replacement-validation
// discriminator exists for a path (kernel without statx, filesystem without
// birth time, unsupported platform). Callers fail closed: deny trust reuse.
var ErrValidationUnsupported = errors.New("amux: replacement-validation discriminator unavailable; project identity is ambiguous")

// ReplacementValidator resolves the replacement-validation discriminator of a
// canonical root path. Implementations must be pure reads (no mutation of the
// inspected tree) and must return ErrValidationUnsupported — never a guessed
// value — when the platform/filesystem cannot provide a reliable surface.
type ReplacementValidator interface {
	ValidationID(realpath string) (ValidationID, error)
}

// birthTimeDigest hashes birth-time fields with domain separation and length
// prefixes (same discipline as ComputeProjectKey) so the persisted value is
// opaque and unambiguous. Shared by the Linux (statx) and Darwin
// (st_birthtimespec) resolvers.
func birthTimeDigest(sec uint64, nsec uint32) string {
	h := sha256.New()
	writeField(h, []byte("amux-rootval-v1"))
	var s [8]byte
	binary.BigEndian.PutUint64(s[:], sec)
	writeField(h, s[:])
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], nsec)
	writeField(h, n[:])
	return hex.EncodeToString(h.Sum(nil))
}
