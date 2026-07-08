//go:build js && wasm

package shmring

import (
	"syscall/js"

	"github.com/gofsd/shmring/backend"
)

// CreateSharedArrayBuffer allocates a new JavaScript SharedArrayBuffer
// sized for the given data capacity (a positive power of two), builds a
// Writer over it, and returns both. Transfer the returned js.Value to
// another thread (for example: worker.Call("postMessage", sab), or include
// it in a struct passed to postMessage) and wrap it with
// OpenSharedArrayBuffer there to get the Reader.
//
// This is the js/wasm equivalent of CreateShm: same ring buffer format,
// same Writer API, different transport (a SharedArrayBuffer shared between
// browser threads instead of an OS shared-memory segment shared between
// processes).
func CreateSharedArrayBuffer(capacity int64, opts ...Option) (*Writer, js.Value, error) {
	if err := validateCapacity(capacity); err != nil {
		return nil, js.Value{}, err
	}
	st, err := backend.NewSharedArrayBuffer(headerSize + capacity)
	if err != nil {
		return nil, js.Value{}, err
	}
	w, err := NewWriter(st, capacity, opts...)
	if err != nil {
		return nil, js.Value{}, err
	}
	return w, st.Buffer(), nil
}

// OpenSharedArrayBuffer wraps a JavaScript SharedArrayBuffer received from
// another thread (typically via a Worker's onmessage handler) and returns
// the Reader for it. capacity must match the value CreateSharedArrayBuffer
// was called with.
func OpenSharedArrayBuffer(sab js.Value, capacity int64, opts ...Option) (*Reader, error) {
	if err := validateCapacity(capacity); err != nil {
		return nil, err
	}
	st, err := backend.NewSharedArrayBufferStorage(sab)
	if err != nil {
		return nil, err
	}
	return NewReader(st, capacity, opts...)
}
