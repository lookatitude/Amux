package platform

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
)

// ProjectKey is the durable, opaque project-identity key: the hex SHA-256 digest
// of (canonical realpath || dev || ino). It is the sole trust anchor for hooks
// (spec "Project identity and trust boundary"): moving, replacing, or remounting
// a root changes realpath or dev/ino and therefore the key, invalidating trust.
type ProjectKey string

// ComputeProjectKey resolves root's canonical identity via fsid and returns its
// ProjectKey plus the resolved realpath. The digest domain-separates the three
// inputs with length prefixes so no two different tuples can collide by
// concatenation ambiguity.
func ComputeProjectKey(fsid FilesystemIdentity, root string) (ProjectKey, string, error) {
	realpath, id, err := fsid.Identify(root)
	if err != nil {
		return "", "", err
	}
	h := sha256.New()
	writeField(h, []byte("amux-project-v1"))
	writeField(h, []byte(realpath))
	var dev, ino [8]byte
	binary.BigEndian.PutUint64(dev[:], id.Dev)
	binary.BigEndian.PutUint64(ino[:], id.Ino)
	writeField(h, dev[:])
	writeField(h, ino[:])
	return ProjectKey(hex.EncodeToString(h.Sum(nil))), realpath, nil
}

// writeField writes a length-prefixed field to the digest so concatenation is
// unambiguous.
func writeField(h interface{ Write([]byte) (int, error) }, b []byte) {
	var l [4]byte
	binary.BigEndian.PutUint32(l[:], uint32(len(b)))
	_, _ = h.Write(l[:])
	_, _ = h.Write(b)
}
