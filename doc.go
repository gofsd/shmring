// Package shmring implements a fixed-capacity, single-producer/
// single-consumer byte ring buffer on top of github.com/hidez8891/shm.
//
// A ring buffer is created by one side (the producer) with CreateShm and
// opened by the other side (the consumer) with OpenShm, naming the same OS
// shared-memory segment. Bytes written by the Writer become visible to the
// Reader in FIFO order, wrapping around the underlying storage as needed.
//
// The storage the ring buffer runs on is pluggable (see the backend
// package): CreateShm/OpenShm use OS shared memory for cross-process use,
// while NewWriter/NewReader accept any backend.Storage, which is what makes
// it possible to run the exact same algorithm over a plain in-process byte
// slice (backend.MemStorage, handy for tests) or over a future backend for
// a platform or transport hidez8891/shm doesn't cover yet.
//
// # Concurrency model
//
// A ring buffer has exactly one Writer and one Reader. Each must only be
// used from a single goroutine at a time (the Writer's goroutine may differ
// from the Reader's goroutine, and in the cross-process case they're
// typically different processes entirely). Calling Write concurrently from
// two goroutines, or Read concurrently from two goroutines, is not
// supported.
//
// The head/tail coordination between the Writer and the Reader relies on
// plain, naturally aligned 64-bit loads and stores to the shared region
// rather than compiler/hardware-level atomics (the underlying shm library
// only exposes ReadAt/WriteAt, not a raw pointer into the mapping). This is
// the same assumption classic SPSC ring buffers over shared memory (e.g.
// Linux kfifo) make, and holds on every architecture Go currently targets,
// but it is weaker than the guarantees sync/atomic gives you within a
// single process. Do not repurpose the Writer/Reader split for anything
// other than the SPSC pattern it was designed for.
package shmring
