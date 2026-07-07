package shmring

import (
	"encoding/binary"

	"github.com/madi/shmring/backend"
)

// Storage layout (all fields little-endian):
//
//	[ 0: 4]  magic     uint32  format identifier
//	[ 4: 8]  version   uint32  format version
//	[ 8:16]  capacity  uint64  size of the data region in bytes (write-once)
//	[16:20]  head      uint32  monotonic read counter, owned by the Reader (atomic)
//	[20:24]  reserved
//	[24:28]  tail      uint32  monotonic write counter, owned by the Writer (atomic)
//	[28:32]  reserved
//	[32:36]  closed    uint32  0 = open, 1 = writer closed (atomic)
//	[36:64]  reserved  28 bytes reserved for future header fields
//	[64: )            data region, `capacity` bytes
//
// head/tail/closed are 32-bit rather than 64-bit so they fit JavaScript's
// Int32Array, the only typed array width Atomics.load/store on the
// js/wasm SharedArrayBuffer backend can use without a BigInt round-trip.
// A 32-bit monotonic counter is safe here because ring buffer correctness
// only depends on tail-head, which never exceeds capacity -- see
// maxCapacity.
const (
	magic         uint32 = 0x53484d52 // "SHMR"
	formatVersion uint32 = 1

	headerSize = 64

	offMagic    = 0
	offVersion  = 4
	offCapacity = 8
	offHead     = 16
	offTail     = 24
	offClosed   = 32

	// maxCapacity bounds ring buffer capacity so that the true distance
	// between the head and tail counters never approaches the point where
	// uint32 wraparound arithmetic (tail-head) could become ambiguous.
	maxCapacity = 1 << 31
)

var byteOrder = binary.LittleEndian

// validateCapacity reports whether capacity is usable as a ring buffer
// size: a positive power of two, no larger than maxCapacity.
func validateCapacity(capacity int64) error {
	if capacity <= 0 || capacity > maxCapacity || capacity&(capacity-1) != 0 {
		return ErrInvalidCapacity
	}
	return nil
}

// initHeader writes a fresh header into st, describing a ring buffer with
// the given data capacity. It is called once, by the side that creates the
// ring buffer.
func initHeader(st backend.Storage, capacity int64) error {
	var buf [headerSize]byte
	byteOrder.PutUint32(buf[offMagic:], magic)
	byteOrder.PutUint32(buf[offVersion:], formatVersion)
	byteOrder.PutUint64(buf[offCapacity:], uint64(capacity))
	byteOrder.PutUint32(buf[offHead:], 0)
	byteOrder.PutUint32(buf[offTail:], 0)
	byteOrder.PutUint32(buf[offClosed:], 0)

	_, err := st.WriteAt(buf[:], 0)
	return err
}

// verifyHeader reads the header from st and checks that it describes a ring
// buffer compatible with the given expected capacity.
func verifyHeader(st backend.Storage, capacity int64) error {
	var buf [headerSize]byte
	if _, err := st.ReadAt(buf[:], 0); err != nil {
		return err
	}
	if byteOrder.Uint32(buf[offMagic:]) != magic {
		return ErrHeaderMismatch
	}
	if byteOrder.Uint32(buf[offVersion:]) != formatVersion {
		return ErrHeaderMismatch
	}
	if byteOrder.Uint64(buf[offCapacity:]) != uint64(capacity) {
		return ErrHeaderMismatch
	}
	return nil
}

// readUint32At loads a header word. If st implements backend.AtomicStorage,
// the load goes through it (required for correctness on backends, like the
// js/wasm SharedArrayBuffer one, where cross-thread visibility isn't
// guaranteed by plain memory access); otherwise it falls back to a plain
// ReadAt, which is sufficient for backends over hardware-coherent memory
// (see ShmStorage and MemStorage's docs).
func readUint32At(st backend.Storage, off int64) (uint32, error) {
	if as, ok := st.(backend.AtomicStorage); ok {
		return as.LoadUint32(off)
	}
	var buf [4]byte
	if _, err := st.ReadAt(buf[:], off); err != nil {
		return 0, err
	}
	return byteOrder.Uint32(buf[:]), nil
}

func writeUint32At(st backend.Storage, off int64, v uint32) error {
	if as, ok := st.(backend.AtomicStorage); ok {
		return as.StoreUint32(off, v)
	}
	var buf [4]byte
	byteOrder.PutUint32(buf[:], v)
	_, err := st.WriteAt(buf[:], off)
	return err
}
