//go:build (linux && !android) || darwin || windows

// ShmStorage is only built for the platforms github.com/hidez8891/shm
// itself supports; other GOOS/GOARCH targets get their own Storage
// implementation instead (js/wasm: wasm.go; android: android.go -- Android
// is explicitly excluded here even though GOOS=android inherits the
// "linux" build tag, because bionic libc doesn't implement the
// shm_open/shm_unlink calls hidez8891/shm's Linux backend depends on).
package backend

import (
	"fmt"
	"math"

	"github.com/hidez8891/shm"
)

// ShmStorage is a Storage backed by an OS shared-memory segment, provided
// by github.com/hidez8891/shm. It is what makes a ring buffer usable for
// cross-process communication: one process calls CreateShm, another opens
// the same named segment with OpenShm.
//
// hidez8891/shm currently supports Linux, macOS and Windows via
// build-tagged implementations; ShmStorage inherits that support
// transparently. Platforms it doesn't cover can still use the ring buffer
// through MemStorage, or through a new Storage implementation added to
// this package.
type ShmStorage struct {
	mem  *shm.Memory
	size int64
}

// CreateShm creates a new named shared-memory segment of the given size and
// returns a Storage backed by it. The segment is removed from the OS when
// the returned Storage is closed.
//
// size must fit in an int32, which is the limit imposed by the underlying
// shm library.
func CreateShm(name string, size int64) (*ShmStorage, error) {
	if err := validateShmSize(size); err != nil {
		return nil, err
	}
	m, err := shm.Create(name, int32(size))
	if err != nil {
		return nil, fmt.Errorf("backend: create shared memory %q: %w", name, err)
	}
	return &ShmStorage{mem: m, size: size}, nil
}

// OpenShm opens a shared-memory segment previously created with CreateShm.
// size must match the size the segment was created with.
func OpenShm(name string, size int64) (*ShmStorage, error) {
	if err := validateShmSize(size); err != nil {
		return nil, err
	}
	m, err := shm.Open(name, int32(size))
	if err != nil {
		return nil, fmt.Errorf("backend: open shared memory %q: %w", name, err)
	}
	return &ShmStorage{mem: m, size: size}, nil
}

func validateShmSize(size int64) error {
	if size <= 0 {
		return fmt.Errorf("backend: size must be positive, got %d", size)
	}
	if size > math.MaxInt32 {
		return fmt.Errorf("backend: size %d exceeds shared memory limit of %d", size, math.MaxInt32)
	}
	return nil
}

// ReadAt implements Storage.
func (s *ShmStorage) ReadAt(p []byte, off int64) (int, error) {
	return s.mem.ReadAt(p, off)
}

// WriteAt implements Storage.
func (s *ShmStorage) WriteAt(p []byte, off int64) (int, error) {
	return s.mem.WriteAt(p, off)
}

// Size implements Storage.
func (s *ShmStorage) Size() int64 {
	return s.size
}

// Close implements Storage.
func (s *ShmStorage) Close() error {
	return s.mem.Close()
}
