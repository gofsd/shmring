//go:build linux && !android

// ShmStorage here is Linux's own direct implementation: a POSIX
// shared-memory segment, mapped by hand via a file under /dev/shm rather
// than through github.com/hidez8891/shm (see shm.go, used on
// macOS/Windows). A /dev/shm-backed file is exactly what glibc's own
// shm_open is documented to do on Linux (a tmpfs-backed regular file
// named by the path), so this is equivalent cross-process shared memory,
// not an approximation of it -- and unlike hidez8891/shm's Memory type,
// it gives this package the raw mapped address a real OS wakeup primitive
// needs. See futex_linux.go: that's what lets ShmStorage implement
// WaiterStorage here, so blocking Write/Read on Linux block on a real
// futex instead of polling with a sleep-based backoff.
package backend

import (
	"fmt"
	"math"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// ShmStorage is a Storage backed by an OS shared-memory segment. It is
// what makes a ring buffer usable for cross-process communication: one
// process calls CreateShm, another opens the same named segment with
// OpenShm.
type ShmStorage struct {
	mem  []byte
	size int64
	owns bool
	path string
}

// CreateShm creates a new named shared-memory segment of the given size
// and returns a Storage backed by it. The segment is removed from the OS
// when the returned Storage is closed.
//
// size must fit in an int32, matching the limit the macOS/Windows build
// of ShmStorage imposes (see shm.go), so callers don't need platform-
// specific size logic.
func CreateShm(name string, size int64) (*ShmStorage, error) {
	if err := validateShmSize(size); err != nil {
		return nil, err
	}
	path, err := shmPath(name)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("backend: create shared memory %q: %w", name, err)
	}
	defer f.Close()
	if err := f.Truncate(size); err != nil {
		os.Remove(path)
		return nil, fmt.Errorf("backend: create shared memory %q: %w", name, err)
	}
	return mapShm(f, path, size, true)
}

// OpenShm opens a shared-memory segment previously created with
// CreateShm. size must match the size the segment was created with.
//
// A segment that exists but is not yet that long -- the state CreateShm
// leaves behind between creating the file and sizing it -- returns an
// error wrapping ErrIncompleteSegment rather than a Storage. A consumer
// racing a producer should treat that as "not ready yet" and retry; see
// mapShm for why it cannot be mapped and read anyway.
func OpenShm(name string, size int64) (*ShmStorage, error) {
	if err := validateShmSize(size); err != nil {
		return nil, err
	}
	path, err := shmPath(name)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("backend: open shared memory %q: %w", name, err)
	}
	defer f.Close()
	return mapShm(f, path, size, false)
}

func shmPath(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("backend: name must not be empty")
	}
	for _, c := range name {
		if c == '/' {
			return "", fmt.Errorf("backend: name must not contain '/', got %q", name)
		}
	}
	return "/dev/shm/" + name, nil
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

func mapShm(f *os.File, path string, size int64, owns bool) (*ShmStorage, error) {
	// mmap creates a mapping that runs past end-of-file without
	// complaining; the fault only arrives later, when something touches a
	// page beyond the file's last one, and on Linux that fault is SIGBUS
	// (BUS_ADRERR). Go reports it as "fatal error: fault" and the process
	// dies -- there is no error to return and nothing to recover from, so
	// a short file has to be caught here rather than at the first read.
	//
	// A file can be short two ways. CreateShm opens with O_CREATE and
	// only then Truncates to size, so a consumer opening in between sees
	// a zero-length segment -- which is precisely what a retry-until-it-
	// exists open loop does, since the file appearing is the event it
	// waits for. And a segment created with a smaller capacity than the
	// one being opened is short in the same way, just further in.
	info, err := f.Stat()
	if err != nil {
		if owns {
			os.Remove(path)
		}
		return nil, fmt.Errorf("backend: stat %q: %w", path, err)
	}
	if info.Size() < size {
		if owns {
			os.Remove(path)
		}
		return nil, fmt.Errorf("backend: %q holds %d bytes, need %d: %w", path, info.Size(), size, ErrIncompleteSegment)
	}

	mem, err := unix.Mmap(int(f.Fd()), 0, int(size), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		if owns {
			os.Remove(path)
		}
		return nil, fmt.Errorf("backend: mmap %q: %w", path, err)
	}
	return &ShmStorage{mem: mem, size: size, owns: owns, path: path}, nil
}

// ReadAt implements Storage.
func (s *ShmStorage) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 || off+int64(len(p)) > s.size {
		return 0, fmt.Errorf("backend: ReadAt out of range: off=%d len=%d size=%d", off, len(p), s.size)
	}
	return copy(p, s.mem[off:off+int64(len(p))]), nil
}

// WriteAt implements Storage.
func (s *ShmStorage) WriteAt(p []byte, off int64) (int, error) {
	if off < 0 || off+int64(len(p)) > s.size {
		return 0, fmt.Errorf("backend: WriteAt out of range: off=%d len=%d size=%d", off, len(p), s.size)
	}
	return copy(s.mem[off:off+int64(len(p))], p), nil
}

// Size implements Storage.
func (s *ShmStorage) Size() int64 { return s.size }

// Close implements Storage: unmaps the segment and, on the creating side,
// removes the underlying /dev/shm file.
func (s *ShmStorage) Close() error {
	if err := unix.Munmap(s.mem); err != nil {
		return fmt.Errorf("backend: munmap: %w", err)
	}
	if s.owns {
		if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("backend: remove %q: %w", s.path, err)
		}
	}
	return nil
}

// Wait implements WaiterStorage using a real futex(2) FUTEX_WAIT on the
// shared word, so a blocking Write/Read parked here costs no CPU and
// wakes as soon as the other side calls Wake.
func (s *ShmStorage) Wait(off int64, old uint32, timeout time.Duration) {
	futexWaitWord(wordAt(s.mem, off), old, timeout)
}

// Wake implements WaiterStorage via futex(2) FUTEX_WAKE.
func (s *ShmStorage) Wake(off int64) {
	futexWakeWord(wordAt(s.mem, off))
}
