//go:build android

package shmring

import "github.com/gofsd/shmring/backend"

// CreateAndroidSharedMemory creates a new ASharedMemory-backed ring buffer
// and returns the Writer plus the underlying file descriptor. Unlike
// CreateShm, there is no name-based rendezvous on Android -- ASharedMemory
// doesn't have one (see backend.AndroidSharedMemoryStorage). Hand the fd
// to whoever should be the Reader: pass it directly if that's another
// goroutine in the same process, or send it to another process (typically
// via your Java/Kotlin layer, as a ParcelFileDescriptor over Binder) and
// wrap it there with OpenAndroidSharedMemory.
func CreateAndroidSharedMemory(debugName string, capacity int64, opts ...Option) (*Writer, int, error) {
	if err := validateCapacity(capacity); err != nil {
		return nil, -1, err
	}
	st, err := backend.CreateAndroidSharedMemory(debugName, headerSize+capacity)
	if err != nil {
		return nil, -1, err
	}
	w, err := NewWriter(st, capacity, opts...)
	if err != nil {
		st.Close()
		return nil, -1, err
	}
	return w, st.Fd(), nil
}

// OpenAndroidSharedMemory wraps an ASharedMemory file descriptor produced
// by CreateAndroidSharedMemory and returns the Reader for it. capacity
// must match the value CreateAndroidSharedMemory was called with.
func OpenAndroidSharedMemory(fd int, capacity int64, opts ...Option) (*Reader, error) {
	if err := validateCapacity(capacity); err != nil {
		return nil, err
	}
	st, err := backend.OpenAndroidSharedMemory(fd, headerSize+capacity)
	if err != nil {
		return nil, err
	}
	return NewReader(st, capacity, opts...)
}
