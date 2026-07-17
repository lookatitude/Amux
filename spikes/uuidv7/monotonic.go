package uuidv7spike

import (
	"encoding/hex"
	"sync"
)

// MonotonicV7 is a self-contained UUIDv7-layout generator with an Amux-owned
// monotonic floor. It exists to DOCUMENT the exact clamp design Amux relies on
// so the "opaque but strictly sortable by creation" invariant (ADR-0002)
// survives a swap away from google/uuid. Production uses google/uuid, which
// already provides this guarantee (proven empirically in the test); this type is
// the fallback blueprint, not production code.
//
// Layout (RFC 9562 UUIDv7): 48-bit big-endian Unix-millisecond timestamp,
// version nibble (7), 12-bit sub-millisecond counter, variant bits (10), and
// 62 bits of entropy. The clamp guarantees that the (timestamp, counter) high
// bits never decrease between successive emissions, so the canonical string form
// is strictly non-decreasing even if the injected wall clock jumps backward.
type MonotonicV7 struct {
	mu         sync.Mutex
	now        func() int64  // injectable Unix-millisecond clock
	entropy    func() uint64 // injectable 64-bit entropy source
	lastMillis int64
	lastCount  uint16 // 12-bit counter
	started    bool
}

// NewMonotonicV7 builds a generator with an injectable clock and entropy source.
func NewMonotonicV7(now func() int64, entropy func() uint64) *MonotonicV7 {
	return &MonotonicV7{now: now, entropy: entropy}
}

// Next returns the canonical string form of the next monotonic UUIDv7.
func (g *MonotonicV7) Next() string {
	g.mu.Lock()
	defer g.mu.Unlock()

	ms := g.now()
	switch {
	case !g.started:
		g.started = true
		g.lastMillis = ms
		g.lastCount = 0
	case ms > g.lastMillis:
		g.lastMillis = ms
		g.lastCount = 0
	default:
		// ms <= lastMillis (same millisecond OR a backward clock jump): hold the
		// floor and advance the counter. This is the clamp that makes a regressing
		// clock safe.
		g.lastCount++
		if g.lastCount > 0x0FFF {
			// Counter overflow within a clamped millisecond: step the floor
			// forward by one ms so ordering is still strictly preserved.
			g.lastMillis++
			g.lastCount = 0
		}
	}

	var b [16]byte
	t := uint64(g.lastMillis)
	b[0] = byte(t >> 40)
	b[1] = byte(t >> 32)
	b[2] = byte(t >> 24)
	b[3] = byte(t >> 16)
	b[4] = byte(t >> 8)
	b[5] = byte(t)
	// version 7 in the high nibble of byte 6, counter high nibble in the low.
	c := g.lastCount & 0x0FFF
	b[6] = 0x70 | byte(c>>8)
	b[7] = byte(c)
	rnd := g.entropy()
	// variant (10) in the two high bits of byte 8, then 62 bits of entropy.
	b[8] = 0x80 | byte(rnd>>58)&0x3F
	b[9] = byte(rnd >> 50)
	b[10] = byte(rnd >> 42)
	b[11] = byte(rnd >> 34)
	b[12] = byte(rnd >> 26)
	b[13] = byte(rnd >> 18)
	b[14] = byte(rnd >> 10)
	b[15] = byte(rnd >> 2)
	return format(b)
}

// format renders 16 bytes as canonical 8-4-4-4-12 hex.
func format(b [16]byte) string {
	var buf [36]byte
	hex.Encode(buf[0:8], b[0:4])
	buf[8] = '-'
	hex.Encode(buf[9:13], b[4:6])
	buf[13] = '-'
	hex.Encode(buf[14:18], b[6:8])
	buf[18] = '-'
	hex.Encode(buf[19:23], b[8:10])
	buf[23] = '-'
	hex.Encode(buf[24:36], b[10:16])
	return string(buf[:])
}
