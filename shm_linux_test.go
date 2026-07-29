//go:build linux && !android

package shmring_test

import (
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/gofsd/shmring"
)

// TestShmRoundTripBlocking exercises the real CreateShm/OpenShm backend
// (backend.ShmStorage on Linux, backed by an actual /dev/shm mapping and
// futex-based WaiterStorage) end to end, including a blocking Read that
// must be woken by a Write in another goroutine rather than by polling.
// ringbuffer_test.go's tests all run over backend.MemStorage, which never
// exercised this backend or its futex Wait/Wake path at all.
//
// A Writer/Reader may only be used from one goroutine at a time (see
// their docs), so this test is careful to fully hand off each of w and r
// to a single goroutine for its remaining lifetime, rather than touching
// either from two goroutines without a happens-before edge between them
// -- the latter would be a real data race on the Writer/Reader struct
// fields regardless of the futex wakeup underneath being correct.
func TestShmRoundTripBlocking(t *testing.T) {
	name := fmt.Sprintf("shmring-go-test-%d", time.Now().UnixNano())

	w, err := shmring.CreateShm(name, 4096)
	if err != nil {
		t.Fatalf("CreateShm: %v", err)
	}
	r, err := shmring.OpenShm(name, 4096)
	if err != nil {
		w.CloseStorage()
		t.Fatalf("OpenShm: %v", err)
	}

	const msg = "hello from a real futex wakeup\n"

	// The reader blocks first, waiting on a futex tied to offTail; the
	// writer only writes (and futex-wakes) after a delay comfortably
	// longer than a single old-style poll tick. If Wait/Wake are wired up
	// correctly, Read returns promptly after the write; if the wiring is
	// broken (e.g. Wait never returns, or Wake targets the wrong word),
	// this either hangs -- caught by the test's own timeout below -- or
	// silently falls back to polling, caught by the latency assertion.
	//
	// w is now solely owned by this goroutine for the rest of the test.
	writeStarted := make(chan struct{})
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		defer w.CloseStorage()
		<-writeStarted
		time.Sleep(20 * time.Millisecond)
		if _, err := w.Write([]byte(msg)); err != nil {
			t.Errorf("Write: %v", err)
			return
		}
		if err := w.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	// r is now solely owned by this goroutine for the rest of the test.
	buf := make([]byte, len(msg))
	readDone := make(chan struct{})
	var n int
	var readErr error
	readerDone := make(chan struct{})
	before := time.Now()
	close(writeStarted)
	go func() {
		defer close(readerDone)
		n, readErr = io.ReadFull(r, buf)
		close(readDone)

		// Drain to EOF once the writer's Close() has had a chance to run
		// (also futex-guarded: this Read blocks on the same tail futex
		// until Writer.Close's wake-on-close fires).
		if readErr == nil {
			if _, err := r.Read(buf); err != io.EOF {
				readErr = fmt.Errorf("final Read = %v, want io.EOF", err)
			}
		}
		r.Close()
	}()

	select {
	case <-readDone:
	case <-time.After(2 * time.Second):
		t.Fatal("blocking Read never returned; WaiterStorage.Wait/Wake is likely deadlocked")
	}
	elapsed := time.Since(before)

	if string(buf[:n]) != msg {
		t.Fatalf("read %q, want %q", buf[:n], msg)
	}
	// The write happens ~20ms after the read starts blocking. A real
	// futex wakeup should return within microseconds of that write; 15ms
	// of slack is generous headroom for scheduler noise while still
	// failing if this silently regressed to a multi-tick poll wait.
	if elapsed > 35*time.Millisecond {
		t.Errorf("Read took %v after the write; want well under the 20ms write delay + a few ms -- looks like it fell back to polling instead of a real futex wakeup", elapsed)
	}

	select {
	case <-readerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("final blocking Read never returned io.EOF; Writer.Close's wake-on-close is likely broken")
	}
	if readErr != nil {
		t.Fatalf("reader goroutine: %v", readErr)
	}

	select {
	case <-writerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("writer goroutine never finished")
	}
}
