//go:build js && wasm

package main

import "syscall/js"

// jsPromise runs fn on its own goroutine and returns a JavaScript Promise
// that resolves with fn's result. It exists because shmring's blocking
// Write/Read poll with time.Sleep, which under GOOS=js relies on the JS
// event loop's timers to resume the goroutine -- so they must not be
// called synchronously from a JS-invoked function (that would starve the
// event loop and the timers backing the very sleep it's waiting on).
// Running fn on a goroutine and reporting back through a Promise lets the
// event loop keep pumping while the wait happens.
func jsPromise(fn func() any) js.Value {
	executor := js.FuncOf(func(this js.Value, args []js.Value) any {
		resolve := args[0]
		go func() {
			resolve.Invoke(fn())
		}()
		return nil
	})
	promise := js.Global().Get("Promise").New(executor)
	executor.Release()
	return promise
}
