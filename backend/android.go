//go:build android

package backend

/*
#cgo LDFLAGS: -landroid
#include <android/sharedmem.h>
#include <stdlib.h>
*/
import "C"

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/unix"
)

// AndroidSharedMemoryStorage is a Storage backed by Android's
// ASharedMemory API (<android/sharedmem.h>), which is what Android
// actually provides for shared memory -- bionic libc doesn't implement
// POSIX shm_open/shm_unlink at all (confirmed by bionic's own headers:
// "mmap/munmap are implemented, but shm_open/shm_unlink are not"), so
// ShmStorage (used on Linux/macOS/Windows) cannot be used here.
//
// The important shape difference from ShmStorage: ASharedMemory has no
// named rendezvous. shm_open(name) lets two unrelated calls find the same
// segment by name; ASharedMemory_create's name argument is purely a debug
// label (visible in /proc/<pid>/maps) and does not let a second call open
// the same region. The only way to share an ASharedMemory region is to
// hand over its file descriptor directly -- within the same process
// that's trivial (pass the int), across processes it means sending it
// over a Unix domain socket (SCM_RIGHTS) or, in a typical Android app, via
// Binder as a ParcelFileDescriptor from the Java/Kotlin layer. This
// package only creates/wraps the fd; cross-process fd transport is the
// caller's responsibility (see shm_android.go and mobile/mobile.go).
//
// ASharedMemory_create requires API level 26+ (NDK headers guard it with
// __INTRODUCED_IN(26); compiling against an older target, e.g. via a
// "…-android24-clang" toolchain binary, hides the declaration entirely and
// fails with "could not determine what C.ASharedMemory_create refers to"
// rather than a clearer availability error).
type AndroidSharedMemoryStorage struct {
	fd   int
	mem  []byte
	size int64
}

// CreateAndroidSharedMemory creates a new ASharedMemory region of the
// given size. debugName is a label for debugging only, not a lookup key.
func CreateAndroidSharedMemory(debugName string, size int64) (*AndroidSharedMemoryStorage, error) {
	if size <= 0 {
		return nil, fmt.Errorf("backend: size must be positive, got %d", size)
	}
	cname := C.CString(debugName)
	defer C.free(unsafe.Pointer(cname))

	fd := C.ASharedMemory_create(cname, C.size_t(size))
	if fd < 0 {
		return nil, fmt.Errorf("backend: ASharedMemory_create failed")
	}
	return newAndroidSharedMemoryStorage(int(fd), size)
}

// OpenAndroidSharedMemory wraps an existing ASharedMemory file descriptor,
// for example one received from another goroutine directly, or from
// Java/Kotlin (via gomobile) after arriving over Binder as a
// ParcelFileDescriptor from another process.
func OpenAndroidSharedMemory(fd int, size int64) (*AndroidSharedMemoryStorage, error) {
	if size <= 0 {
		return nil, fmt.Errorf("backend: size must be positive, got %d", size)
	}
	return newAndroidSharedMemoryStorage(fd, size)
}

func newAndroidSharedMemoryStorage(fd int, size int64) (*AndroidSharedMemoryStorage, error) {
	mem, err := unix.Mmap(fd, 0, int(size), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		return nil, fmt.Errorf("backend: mmap fd %d: %w", fd, err)
	}
	return &AndroidSharedMemoryStorage{fd: fd, mem: mem, size: size}, nil
}

// Fd returns the underlying file descriptor, to hand to another goroutine
// or (via JNI/gomobile) to Java/Kotlin for cross-process transport.
func (s *AndroidSharedMemoryStorage) Fd() int { return s.fd }

// ReadAt implements Storage.
func (s *AndroidSharedMemoryStorage) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 || off+int64(len(p)) > s.size {
		return 0, fmt.Errorf("backend: ReadAt out of range: off=%d len=%d size=%d", off, len(p), s.size)
	}
	return copy(p, s.mem[off:off+int64(len(p))]), nil
}

// WriteAt implements Storage.
func (s *AndroidSharedMemoryStorage) WriteAt(p []byte, off int64) (int, error) {
	if off < 0 || off+int64(len(p)) > s.size {
		return 0, fmt.Errorf("backend: WriteAt out of range: off=%d len=%d size=%d", off, len(p), s.size)
	}
	return copy(s.mem[off:off+int64(len(p))], p), nil
}

// Size implements Storage.
func (s *AndroidSharedMemoryStorage) Size() int64 { return s.size }

// Close unmaps the region and closes the file descriptor.
func (s *AndroidSharedMemoryStorage) Close() error {
	if err := unix.Munmap(s.mem); err != nil {
		return fmt.Errorf("backend: munmap: %w", err)
	}
	return unix.Close(s.fd)
}
