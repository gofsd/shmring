package backend

import (
	"io"
	"sync"
)

// MemStorage is a Storage backed by a plain Go byte slice. It never leaves
// the process, so it's useful for unit tests, benchmarks, and for platforms
// where an OS shared-memory backend isn't available: the same ring buffer
// code path can run against it, with a producer and consumer goroutine
// sharing one MemStorage instead of two processes sharing shared memory.
//
// Real OS shared memory (see ShmStorage) is coherent across processes at
// the hardware level, which is what lets the ring buffer's SPSC algorithm
// use plain aligned loads/stores for its head/tail counters instead of
// sync/atomic. That guarantee doesn't hold for two goroutines in the same
// process talking through an ordinary []byte: the Go memory model requires
// an explicit happens-before edge, or the compiler is free to reorder or
// cache accesses. MemStorage supplies that edge with a mutex around every
// ReadAt/WriteAt, so it is safe to share between goroutines even though it
// isn't lock-free.
type MemStorage struct {
	mu  sync.Mutex
	buf []byte
}

// NewMemStorage allocates a MemStorage of the given size.
func NewMemStorage(size int64) *MemStorage {
	if size <= 0 {
		panic("backend: MemStorage size must be positive")
	}
	return &MemStorage{buf: make([]byte, size)}
}

// ReadAt implements Storage.
func (s *MemStorage) ReadAt(p []byte, off int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if off < 0 || off >= int64(len(s.buf)) {
		return 0, io.EOF
	}
	n := copy(p, s.buf[off:])
	if n < len(p) {
		return n, io.ErrUnexpectedEOF
	}
	return n, nil
}

// WriteAt implements Storage.
func (s *MemStorage) WriteAt(p []byte, off int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if off < 0 || off+int64(len(p)) > int64(len(s.buf)) {
		return 0, io.ErrShortWrite
	}
	return copy(s.buf[off:], p), nil
}

// Size implements Storage.
func (s *MemStorage) Size() int64 {
	return int64(len(s.buf))
}

// Close implements Storage. It is a no-op.
func (s *MemStorage) Close() error {
	return nil
}
