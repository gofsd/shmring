package shmring_test

import (
	"context"
	"io"
	"math/rand"
	"testing"
	"time"

	"github.com/gofsd/shmring"
	"github.com/gofsd/shmring/backend"
)

// newPair builds a Writer/Reader pair sharing one in-process MemStorage, so
// tests don't depend on OS shared memory being available in CI.
func newPair(t *testing.T, capacity int64) (*shmring.Writer, *shmring.Reader) {
	t.Helper()
	// NewWriter/NewReader only require Storage.Size() >= headerSize+capacity;
	// oversizing here avoids coupling the test to the unexported header size.
	st := backend.NewMemStorage(capacity + 4096)
	w, err := shmring.NewWriter(st, capacity)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	r, err := shmring.NewReader(st, capacity)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	return w, r
}

func TestTryWriteTryReadRoundTrip(t *testing.T) {
	w, r := newPair(t, 16)

	n, err := w.TryWrite([]byte("hello"))
	if err != nil || n != 5 {
		t.Fatalf("TryWrite = %d, %v, want 5, nil", n, err)
	}

	buf := make([]byte, 5)
	n, err = r.TryRead(buf)
	if err != nil || n != 5 || string(buf) != "hello" {
		t.Fatalf("TryRead = %d, %q, %v, want 5, hello, nil", n, buf[:n], err)
	}
}

func TestTryReadEmptyReturnsZeroNil(t *testing.T) {
	_, r := newPair(t, 16)
	buf := make([]byte, 4)
	n, err := r.TryRead(buf)
	if n != 0 || err != nil {
		t.Fatalf("TryRead on empty = %d, %v, want 0, nil", n, err)
	}
}

func TestTryWriteFullReturnsZeroNil(t *testing.T) {
	w, _ := newPair(t, 8)
	if _, err := w.TryWrite([]byte("12345678")); err != nil {
		t.Fatalf("fill TryWrite: %v", err)
	}
	n, err := w.TryWrite([]byte("x"))
	if n != 0 || err != nil {
		t.Fatalf("TryWrite on full = %d, %v, want 0, nil", n, err)
	}
}

func TestWraparound(t *testing.T) {
	w, r := newPair(t, 8)

	// Prime the buffer so head/tail sit in the middle of the ring, then
	// write a payload that straddles the wraparound point.
	if _, err := w.TryWrite([]byte("1234")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.TryRead(make([]byte, 4)); err != nil {
		t.Fatal(err)
	}

	payload := []byte("abcdefgh")
	n, err := w.TryWrite(payload)
	if err != nil || n != 8 {
		t.Fatalf("TryWrite wraparound = %d, %v, want 8, nil", n, err)
	}

	got := make([]byte, 8)
	n, err = r.TryRead(got)
	if err != nil || n != 8 || string(got) != string(payload) {
		t.Fatalf("TryRead wraparound = %d, %q, %v, want 8, %q, nil", n, got[:n], err, payload)
	}
}

func TestBlockingWriteReadAcrossGoroutines(t *testing.T) {
	w, r := newPair(t, 4)

	const total = 10_000
	data := make([]byte, total)
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}

	errCh := make(chan error, 2)
	go func() {
		_, err := w.Write(data)
		errCh <- err
	}()

	got := make([]byte, total)
	go func() {
		_, err := io.ReadFull(r, got)
		errCh <- err
	}()

	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil {
			t.Fatalf("goroutine error: %v", err)
		}
	}

	for i := range data {
		if got[i] != data[i] {
			t.Fatalf("byte %d mismatch: got %x want %x", i, got[i], data[i])
		}
	}
}

func TestCloseDrainsThenEOF(t *testing.T) {
	w, r := newPair(t, 16)

	if _, err := w.TryWrite([]byte("bye")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	buf := make([]byte, 3)
	n, err := r.TryRead(buf)
	if err != nil || n != 3 {
		t.Fatalf("TryRead after close (draining) = %d, %v, want 3, nil", n, err)
	}

	n, err = r.TryRead(buf)
	if n != 0 || err != io.EOF {
		t.Fatalf("TryRead after drained+closed = %d, %v, want 0, io.EOF", n, err)
	}
}

func TestWriteAfterCloseReturnsErrClosed(t *testing.T) {
	w, _ := newPair(t, 16)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := w.TryWrite([]byte("x"))
	if err != shmring.ErrClosed {
		t.Fatalf("TryWrite after close = %v, want ErrClosed", err)
	}
}

func TestBlockingReadReturnsEOFAfterClose(t *testing.T) {
	w, r := newPair(t, 16)

	done := make(chan error, 1)
	go func() {
		_, err := r.Read(make([]byte, 1))
		done <- err
	}()

	time.Sleep(10 * time.Millisecond) // let the reader start blocking
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-done:
		if err != io.EOF {
			t.Fatalf("blocking Read after close = %v, want io.EOF", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Read did not return after Writer closed")
	}
}

func TestWriteContextCancellation(t *testing.T) {
	w, _ := newPair(t, 4)
	if _, err := w.TryWrite([]byte("1234")); err != nil {
		t.Fatal(err) // fill the buffer so the next write must block
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := w.WriteContext(ctx, []byte("more"))
	if err != context.DeadlineExceeded {
		t.Fatalf("WriteContext = %v, want context.DeadlineExceeded", err)
	}
}

func TestNewWriterRejectsNonPowerOfTwoCapacity(t *testing.T) {
	st := backend.NewMemStorage(128)
	if _, err := shmring.NewWriter(st, 10); err != shmring.ErrInvalidCapacity {
		t.Fatalf("NewWriter with capacity=10 = %v, want ErrInvalidCapacity", err)
	}
}

func TestNewReaderRejectsCapacityMismatch(t *testing.T) {
	st := backend.NewMemStorage(64 + 16)
	if _, err := shmring.NewWriter(st, 16); err != nil {
		t.Fatal(err)
	}
	if _, err := shmring.NewReader(st, 8); err != shmring.ErrHeaderMismatch {
		t.Fatalf("NewReader with mismatched capacity = %v, want ErrHeaderMismatch", err)
	}
}

func TestNewWriterRejectsUndersizedStorage(t *testing.T) {
	st := backend.NewMemStorage(16)
	if _, err := shmring.NewWriter(st, 16); err != shmring.ErrStorageTooSmall {
		t.Fatalf("NewWriter with undersized storage = %v, want ErrStorageTooSmall", err)
	}
}
