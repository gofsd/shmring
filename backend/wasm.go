//go:build js && wasm

package backend

import (
	"fmt"
	"syscall/js"
)

var (
	uint8ArrayClass        = js.Global().Get("Uint8Array")
	int32ArrayClass        = js.Global().Get("Int32Array")
	atomicsObject          = js.Global().Get("Atomics")
	sharedArrayBufferClass = js.Global().Get("SharedArrayBuffer")
)

// SharedArrayBufferStorage is a Storage backed by a JavaScript
// SharedArrayBuffer. It's what lets a ring buffer span two different
// threads in a browser -- typically the main thread and a Web Worker --
// when this package is compiled with GOOS=js GOARCH=wasm.
//
// A SharedArrayBuffer is created on one side (NewSharedArrayBuffer) and
// transferred to the other (e.g. via postMessage to a Worker, which shares
// rather than copies a SharedArrayBuffer); each side then wraps its own
// copy of the same underlying buffer with NewSharedArrayBufferStorage and
// builds a Writer/Reader on top of it as usual. Both sides run their own,
// independent instance of this compiled wasm module -- Go's js/wasm target
// doesn't support true multithreading within a single instance, so the two
// sides are not sharing a Go runtime, only the bytes in the
// SharedArrayBuffer.
//
// Requires the page to be cross-origin isolated (the
// Cross-Origin-Opener-Policy and Cross-Origin-Embedder-Policy response
// headers), which is what makes SharedArrayBuffer available in the first
// place; see the web/ directory for a working example.
type SharedArrayBufferStorage struct {
	buf   js.Value // the SharedArrayBuffer
	bytes js.Value // Uint8Array view over buf, for bulk ReadAt/WriteAt
	words js.Value // Int32Array view over buf, for Atomics
	size  int64
}

// NewSharedArrayBuffer allocates a fresh SharedArrayBuffer of the given
// size and wraps it. Call Buffer to obtain the js.Value to transfer to a
// Worker.
func NewSharedArrayBuffer(size int64) (*SharedArrayBufferStorage, error) {
	if size <= 0 {
		return nil, fmt.Errorf("backend: size must be positive, got %d", size)
	}
	if size%4 != 0 {
		return nil, fmt.Errorf("backend: size must be a multiple of 4, got %d", size)
	}
	return NewSharedArrayBufferStorage(sharedArrayBufferClass.New(size))
}

// NewSharedArrayBufferStorage wraps an existing JavaScript SharedArrayBuffer,
// for example one received from another thread via postMessage.
func NewSharedArrayBufferStorage(sab js.Value) (*SharedArrayBufferStorage, error) {
	if sab.Type() != js.TypeObject || !sab.InstanceOf(sharedArrayBufferClass) {
		return nil, fmt.Errorf("backend: value is not a SharedArrayBuffer")
	}
	size := int64(sab.Get("byteLength").Int())
	if size%4 != 0 {
		return nil, fmt.Errorf("backend: SharedArrayBuffer size must be a multiple of 4, got %d", size)
	}
	return &SharedArrayBufferStorage{
		buf:   sab,
		bytes: uint8ArrayClass.New(sab),
		words: int32ArrayClass.New(sab),
		size:  size,
	}, nil
}

// Buffer returns the underlying JavaScript SharedArrayBuffer, to transfer
// to a Worker (e.g. worker.postMessage({sab: storage.Buffer()})) or hand to
// hand-written JS/TS code sharing the same ring buffer format.
func (s *SharedArrayBufferStorage) Buffer() js.Value { return s.buf }

// ReadAt implements Storage using a bulk, non-atomic copy out of the
// SharedArrayBuffer. Safe for the ring buffer's payload bytes because
// access to them is already gated by the atomic head/tail handshake (see
// LoadUint32/StoreUint32).
func (s *SharedArrayBufferStorage) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 || off+int64(len(p)) > s.size {
		return 0, fmt.Errorf("backend: ReadAt out of range: off=%d len=%d size=%d", off, len(p), s.size)
	}
	if len(p) == 0 {
		return 0, nil
	}
	view := uint8ArrayClass.New(s.buf, off, len(p))
	return js.CopyBytesToGo(p, view), nil
}

// WriteAt implements Storage using a bulk, non-atomic copy into the
// SharedArrayBuffer. See ReadAt for why that's safe here.
func (s *SharedArrayBufferStorage) WriteAt(p []byte, off int64) (int, error) {
	if off < 0 || off+int64(len(p)) > s.size {
		return 0, fmt.Errorf("backend: WriteAt out of range: off=%d len=%d size=%d", off, len(p), s.size)
	}
	if len(p) == 0 {
		return 0, nil
	}
	view := uint8ArrayClass.New(s.buf, off, len(p))
	return js.CopyBytesToJS(view, p), nil
}

// Size implements Storage.
func (s *SharedArrayBufferStorage) Size() int64 { return s.size }

// Close implements Storage. It is a no-op; JavaScript's garbage collector
// owns the SharedArrayBuffer.
func (s *SharedArrayBufferStorage) Close() error { return nil }

// LoadUint32 implements backend.AtomicStorage using Atomics.load.
func (s *SharedArrayBufferStorage) LoadUint32(off int64) (uint32, error) {
	idx, err := s.wordIndex(off)
	if err != nil {
		return 0, err
	}
	v := atomicsObject.Call("load", s.words, idx)
	return uint32(int32(v.Int())), nil
}

// StoreUint32 implements backend.AtomicStorage using Atomics.store.
func (s *SharedArrayBufferStorage) StoreUint32(off int64, v uint32) error {
	idx, err := s.wordIndex(off)
	if err != nil {
		return err
	}
	atomicsObject.Call("store", s.words, idx, v)
	return nil
}

func (s *SharedArrayBufferStorage) wordIndex(off int64) (int, error) {
	if off < 0 || off%4 != 0 || off+4 > s.size {
		return 0, fmt.Errorf("backend: misaligned or out-of-range atomic offset %d", off)
	}
	return int(off / 4), nil
}
