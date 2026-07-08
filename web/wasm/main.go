//go:build js && wasm

// Command wasm compiles to shmring.wasm: a WebAssembly build of the
// shmring library exposed to JavaScript on globalThis.shmring, for use in
// a browser via web/shmring.js.
//
// This is the same Go shmring.Writer/Reader code that runs natively on
// Linux/macOS/Windows -- only the storage backend differs (a JavaScript
// SharedArrayBuffer instead of an OS shared-memory segment). Each browser
// thread (main thread, or a Web Worker) that wants to be one side of a
// ring buffer loads its own copy of this wasm module; see
// web/example for a working main-thread-writer / worker-reader pair.
package main

import (
	"fmt"
	"io"
	"sync"
	"syscall/js"

	"github.com/gofsd/shmring"
)

func main() {
	js.Global().Set("shmring", js.ValueOf(map[string]any{
		"createWriter":   js.FuncOf(createWriter),
		"openReader":     js.FuncOf(openReader),
		"writerTryWrite": js.FuncOf(writerTryWrite),
		"writerWrite":    js.FuncOf(writerWrite),
		"writerClose":    js.FuncOf(writerClose),
		"readerTryRead":  js.FuncOf(readerTryRead),
		"readerRead":     js.FuncOf(readerRead),
		"readerClose":    js.FuncOf(readerClose),
	}))

	select {} // keep the wasm instance alive to service callbacks
}

var (
	handleMu sync.Mutex
	nextID   int
	writers  = map[int]*shmring.Writer{}
	readers  = map[int]*shmring.Reader{}
)

func newHandle() int {
	handleMu.Lock()
	defer handleMu.Unlock()
	nextID++
	return nextID
}

// result builds the {value, err} shaped object every exported call returns
// on the synchronous path, so JS-side error checking is uniform.
func result(value any, err error) map[string]any {
	if err != nil {
		return map[string]any{"value": nil, "err": err.Error()}
	}
	return map[string]any{"value": value, "err": nil}
}

// createWriter(capacity) -> {value: {writerId, sab}, err}
func createWriter(this js.Value, args []js.Value) any {
	if len(args) != 1 {
		return js.ValueOf(result(nil, fmt.Errorf("createWriter(capacity): expected 1 argument, got %d", len(args))))
	}
	capacity := int64(args[0].Int())

	w, sab, err := shmring.CreateSharedArrayBuffer(capacity)
	if err != nil {
		return js.ValueOf(result(nil, err))
	}

	id := newHandle()
	handleMu.Lock()
	writers[id] = w
	handleMu.Unlock()

	return js.ValueOf(result(map[string]any{"writerId": id, "sab": sab}, nil))
}

// openReader(sab, capacity) -> {value: {readerId}, err}
func openReader(this js.Value, args []js.Value) any {
	if len(args) != 2 {
		return js.ValueOf(result(nil, fmt.Errorf("openReader(sab, capacity): expected 2 arguments, got %d", len(args))))
	}
	sab := args[0]
	capacity := int64(args[1].Int())

	r, err := shmring.OpenSharedArrayBuffer(sab, capacity)
	if err != nil {
		return js.ValueOf(result(nil, err))
	}

	id := newHandle()
	handleMu.Lock()
	readers[id] = r
	handleMu.Unlock()

	return js.ValueOf(result(map[string]any{"readerId": id}, nil))
}

func getWriter(id int) (*shmring.Writer, error) {
	handleMu.Lock()
	defer handleMu.Unlock()
	w, ok := writers[id]
	if !ok {
		return nil, fmt.Errorf("shmring: unknown writerId %d", id)
	}
	return w, nil
}

func getReader(id int) (*shmring.Reader, error) {
	handleMu.Lock()
	defer handleMu.Unlock()
	r, ok := readers[id]
	if !ok {
		return nil, fmt.Errorf("shmring: unknown readerId %d", id)
	}
	return r, nil
}

func bytesFromJS(v js.Value) []byte {
	p := make([]byte, v.Get("length").Int())
	js.CopyBytesToGo(p, v)
	return p
}

// writerTryWrite(writerId, uint8array) -> {value: {n}, err} (synchronous)
func writerTryWrite(this js.Value, args []js.Value) any {
	w, err := getWriter(args[0].Int())
	if err != nil {
		return js.ValueOf(result(nil, err))
	}
	n, err := w.TryWrite(bytesFromJS(args[1]))
	if err != nil {
		return js.ValueOf(result(nil, err))
	}
	return js.ValueOf(result(map[string]any{"n": n}, nil))
}

// writerWrite(writerId, uint8array) -> Promise<{value: {n}, err}>
// Blocks (via the poll-backoff Write implements) until all bytes are
// written, without blocking the JS event loop -- see jsPromise.
func writerWrite(this js.Value, args []js.Value) any {
	writerID := args[0].Int()
	p := bytesFromJS(args[1])
	return jsPromise(func() any {
		w, err := getWriter(writerID)
		if err != nil {
			return js.ValueOf(result(nil, err))
		}
		n, err := w.Write(p)
		if err != nil {
			return js.ValueOf(result(nil, err))
		}
		return js.ValueOf(result(map[string]any{"n": n}, nil))
	})
}

// writerClose(writerId) -> {value: null, err}
func writerClose(this js.Value, args []js.Value) any {
	w, err := getWriter(args[0].Int())
	if err != nil {
		return js.ValueOf(result(nil, err))
	}
	return js.ValueOf(result(nil, w.Close()))
}

// readerTryRead(readerId, uint8array) -> {value: {n, eof}, err} (synchronous)
func readerTryRead(this js.Value, args []js.Value) any {
	r, err := getReader(args[0].Int())
	if err != nil {
		return js.ValueOf(result(nil, err))
	}
	buf := make([]byte, args[1].Get("length").Int())
	n, err := r.TryRead(buf)
	eof := err == io.EOF
	if err != nil && !eof {
		return js.ValueOf(result(nil, err))
	}
	js.CopyBytesToJS(args[1], buf[:n])
	return js.ValueOf(result(map[string]any{"n": n, "eof": eof}, nil))
}

// readerRead(readerId, uint8array) -> Promise<{value: {n, eof}, err}>
func readerRead(this js.Value, args []js.Value) any {
	readerID := args[0].Int()
	length := args[1].Get("length").Int()
	target := args[1]
	return jsPromise(func() any {
		r, err := getReader(readerID)
		if err != nil {
			return js.ValueOf(result(nil, err))
		}
		buf := make([]byte, length)
		n, err := r.Read(buf)
		eof := err == io.EOF
		if err != nil && !eof {
			return js.ValueOf(result(nil, err))
		}
		js.CopyBytesToJS(target, buf[:n])
		return js.ValueOf(result(map[string]any{"n": n, "eof": eof}, nil))
	})
}

// readerClose(readerId) -> {value: null, err}
func readerClose(this js.Value, args []js.Value) any {
	r, err := getReader(args[0].Int())
	if err != nil {
		return js.ValueOf(result(nil, err))
	}
	return js.ValueOf(result(nil, r.Close()))
}
