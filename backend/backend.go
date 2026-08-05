// Package backend defines the storage abstraction that the ring buffer is
// built on top of, plus the implementations shmring ships with.
//
// The ring buffer logic in the parent package never talks to OS shared
// memory directly: it only depends on the Storage interface. This keeps
// the platform-specific and IPC-specific concerns isolated to this package,
// so support for additional platforms or transports (POSIX shm, Windows
// file mappings, /dev/shm, RDMA, ...) can be added as new Storage
// implementations without touching the ring buffer algorithm.
package backend

import (
	"errors"
	"io"
	"time"
)

// ErrIncompleteSegment is returned, wrapped, when a named shared-memory
// segment exists but is smaller than the mapping being asked for -- most
// often because its creator has not finished sizing it yet. It is a
// transient condition for a consumer that opens a segment concurrently
// with its producer, and the only way such a consumer can tell "come back
// in a moment" apart from a segment that will never be right.
//
// Only the Linux backend reports it: it maps /dev/shm files itself and so
// can see their true length. The macOS/Windows backend goes through
// github.com/hidez8891/shm, which does not expose one.
var ErrIncompleteSegment = errors.New("backend: shared memory segment is smaller than the requested mapping")

// Storage is a fixed-size, randomly addressable region of bytes shared
// between a producer and a consumer. It is the minimal capability the ring
// buffer needs from its underlying memory.
//
// Implementations must be safe for concurrent use by one reader goroutine
// and one writer goroutine at the same time (but not by multiple readers or
// multiple writers), matching the single-producer/single-consumer contract
// of the ring buffer itself.
type Storage interface {
	io.ReaderAt
	io.WriterAt

	// Size returns the total size in bytes of the storage region. It is
	// constant for the lifetime of the Storage.
	Size() int64

	// Close releases resources associated with the storage. For
	// process-local backends this is typically a no-op; for shared-memory
	// backends it unmaps the segment and, for the creating side, removes
	// the underlying OS object.
	Close() error
}

// AtomicStorage is an optional capability a Storage may implement to
// provide real atomic 32-bit loads/stores for the ring buffer's
// head/tail/closed counters, at the offsets the ring buffer header defines
// for them (all 4-byte aligned).
//
// ShmStorage and MemStorage don't implement it: OS shared memory is
// coherent across processes at the hardware level, so a plain aligned
// load/store is enough (see their docs). The js/wasm SharedArrayBuffer
// backend does implement it, using JavaScript's Atomics, because that's
// the web platform's actual cross-thread visibility guarantee -- an
// ordinary read/write to a SharedArrayBuffer from two different threads
// (e.g. a browser main thread and a Web Worker) is a data race under the
// JavaScript memory model, the same way it would be between two Go
// goroutines without synchronization.
type AtomicStorage interface {
	Storage
	LoadUint32(off int64) (uint32, error)
	StoreUint32(off int64, v uint32) error
}

// WaiterStorage is an optional capability a Storage may implement to let a
// blocking Write/Read sleep on a real OS wakeup primitive tied to one of
// the ring buffer's header words (head/tail/closed), instead of polling
// those words with a sleep-based backoff.
//
// This is the abstraction real cross-process wakeup primitives (a Linux
// futex on the shared word, for example) sit behind: Wait blocks the
// calling goroutine until the word at off no longer holds old, or until
// timeout elapses (timeout <= 0 means wait indefinitely); Wake wakes any
// goroutine -- in this process or another -- currently parked in Wait on
// off. Wait always returns eventually for some reason (real change,
// spurious wakeup, or timeout); it's the caller's job to re-read the word
// and decide what to do next, exactly as with a futex.
//
// Implementing this is optional: a Storage that doesn't implement it gets
// the existing sleep-and-poll behavior automatically (see Writer/Reader),
// which remains correct, just busier while waiting.
type WaiterStorage interface {
	Storage
	Wait(off int64, old uint32, timeout time.Duration)
	Wake(off int64)
}
