//go:build android

// Package mobile is a gomobile-friendly facade over shmring's Android
// backend, for building an Android AAR with `gomobile bind`. It exists for
// two reasons: gobind (gomobile's binding generator) can't export
// variadic parameters, so CreateShm-style ...Option signatures aren't
// usable directly; and Android's real shared-memory API (ASharedMemory,
// see backend/android.go and shm_android.go) is fd-based rather than the
// name-based rendezvous CreateShm/OpenShm use on desktop, which needs its
// own Writer/Reader constructors regardless of gomobile.
//
// Passing the fd to another *process* -- the actual point of a shared
// memory ring buffer -- is not something this package or gomobile does for
// you: ASharedMemory has no named lookup, so the fd has to be transported
// explicitly, which on Android normally means the Java/Kotlin layer
// sending it over Binder as a ParcelFileDescriptor. That plumbing is
// app-specific and isn't included here.
//
// # Verification status
//
// Confirmed: this package and backend/android.go cross-compile cleanly
// against a real Android NDK (28.2.13676358, API 26 clang) and link
// against the actual bionic sysroot headers -- that's what caught
// hidez8891/shm's incompatibility with Android in the first place (see
// backend/shm.go's doc comment) and confirms this replacement targets the
// right API. `gomobile bind` from this package also produces a complete,
// real AAR (native .so for armeabi-v7a/arm64-v8a/x86/x86_64 plus generated
// Java bindings).
//
// Not confirmed: that ASharedMemory_create/mmap actually behave correctly
// at runtime on Android. Two attempts to verify this on an emulator (AVD
// "Pixel_9", both with and without KVM acceleration) ended in the
// emulator process itself segfaulting during boot, unrelated to this
// code -- and no physical device was available as a fallback. Treat the
// runtime behavior as unverified until someone runs it on a real device
// or a working emulator; see the Android section of the README for how.
package mobile

import "github.com/gofsd/shmring"

// Writer is the producer side of a ring buffer.
type Writer struct {
	w *shmring.Writer
}

// Reader is the consumer side of a ring buffer.
type Reader struct {
	r *shmring.Reader
}

// CreateResult bundles CreateSharedMemory's outputs: gobind (gomobile's
// binding generator) only allows a function to return one value plus an
// optional error, not a (Writer, fd, error) triple.
type CreateResult struct {
	Writer *Writer
	Fd     int
}

// CreateSharedMemory creates a new ASharedMemory-backed ring buffer.
// capacity must be a positive power of two, in bytes. debugName is a
// label for debugging only (visible in /proc/<pid>/maps), not a lookup
// key. The result's Fd is the region's file descriptor -- hand it to
// whoever should be the Reader (see the package doc).
func CreateSharedMemory(debugName string, capacity int64) (*CreateResult, error) {
	w, fd, err := shmring.CreateAndroidSharedMemory(debugName, capacity)
	if err != nil {
		return nil, err
	}
	return &CreateResult{Writer: &Writer{w: w}, Fd: fd}, nil
}

// OpenSharedMemory wraps an ASharedMemory file descriptor produced by
// CreateSharedMemory (received directly, or via Binder from another
// process) and returns the Reader for it. capacity must match.
func OpenSharedMemory(fd int, capacity int64) (*Reader, error) {
	r, err := shmring.OpenAndroidSharedMemory(fd, capacity)
	if err != nil {
		return nil, err
	}
	return &Reader{r: r}, nil
}

// TryWrite writes as much of p as currently fits without blocking.
func (w *Writer) TryWrite(p []byte) (int, error) { return w.w.TryWrite(p) }

// Write blocks until all of p is written.
func (w *Writer) Write(p []byte) (int, error) { return w.w.Write(p) }

// Close marks the ring buffer closed; buffered data can still be drained.
func (w *Writer) Close() error { return w.w.Close() }

// CloseStorage additionally unmaps and closes the underlying fd.
func (w *Writer) CloseStorage() error { return w.w.CloseStorage() }

// TryRead reads as many bytes as are currently available without blocking.
func (r *Reader) TryRead(p []byte) (int, error) { return r.r.TryRead(p) }

// Read blocks until at least one byte is available.
func (r *Reader) Read(p []byte) (int, error) { return r.r.Read(p) }

// Close releases this side's handle on the shared memory.
func (r *Reader) Close() error { return r.r.Close() }
