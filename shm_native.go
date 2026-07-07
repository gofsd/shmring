//go:build (linux && !android) || darwin || windows

// CreateShm/OpenShm need GOOS=android excluded even though it inherits the
// "linux" build tag: they call into backend.CreateShm/OpenShm, which are
// built on hidez8891/shm's POSIX shm_open/shm_unlink -- and Android's
// bionic libc doesn't implement those (mmap/munmap only). See
// shm_android.go for Android's real backend (ASharedMemory).
package shmring

import "github.com/madi/shmring/backend"

// CreateShm creates a new OS shared-memory segment named name, sized for
// the given data capacity (a positive power of two), and returns the
// Writer for it. The segment is removed from the OS when the Writer is
// closed. The consumer opens the same segment with OpenShm.
func CreateShm(name string, capacity int64, opts ...Option) (*Writer, error) {
	if err := validateCapacity(capacity); err != nil {
		return nil, err
	}
	st, err := backend.CreateShm(name, headerSize+capacity)
	if err != nil {
		return nil, err
	}
	w, err := NewWriter(st, capacity, opts...)
	if err != nil {
		st.Close()
		return nil, err
	}
	return w, nil
}

// OpenShm opens a shared-memory segment created by CreateShm with the same
// name and capacity, and returns the Reader for it.
func OpenShm(name string, capacity int64, opts ...Option) (*Reader, error) {
	if err := validateCapacity(capacity); err != nil {
		return nil, err
	}
	st, err := backend.OpenShm(name, headerSize+capacity)
	if err != nil {
		return nil, err
	}
	r, err := NewReader(st, capacity, opts...)
	if err != nil {
		st.Close()
		return nil, err
	}
	return r, nil
}
