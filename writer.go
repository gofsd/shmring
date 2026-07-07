package shmring

import (
	"context"
	"time"

	"github.com/madi/shmring/backend"
)

// Writer is the producer side of a ring buffer. A Writer must only be used
// from a single goroutine at a time.
type Writer struct {
	st       backend.Storage
	capacity int64
	mask     int64
	opt      options

	tail       uint32 // local authoritative copy; persisted to storage on every write
	cachedHead uint32 // last head value observed from storage
	closed     bool
}

// NewWriter initializes a fresh ring buffer header on st and returns the
// producer handle for it. capacity must be a positive power of two, and st
// must be at least headerSize+capacity bytes.
//
// NewWriter is the low-level entry point used by CreateShm; use it directly
// when running the ring buffer over a custom backend.Storage (for example
// backend.MemStorage in tests).
func NewWriter(st backend.Storage, capacity int64, opts ...Option) (*Writer, error) {
	if err := validateCapacity(capacity); err != nil {
		return nil, err
	}
	if st.Size() < headerSize+capacity {
		return nil, ErrStorageTooSmall
	}
	if err := initHeader(st, capacity); err != nil {
		return nil, err
	}
	o := defaultOptions()
	for _, opt := range opts {
		opt(&o)
	}
	return &Writer{
		st:       st,
		capacity: capacity,
		mask:     capacity - 1,
		opt:      o,
	}, nil
}

// TryWrite writes as much of p as currently fits in the ring buffer without
// blocking, returning the number of bytes written. It returns (0, nil) if
// the buffer is full, and (0, ErrClosed) if the Writer has been closed.
func (w *Writer) TryWrite(p []byte) (int, error) {
	if w.closed {
		return 0, ErrClosed
	}
	if len(p) == 0 {
		return 0, nil
	}

	free := w.capacity - int64(w.tail-w.cachedHead)
	if free < int64(len(p)) {
		// The cached head may be stale (the reader has consumed more than
		// we've observed); refresh it before concluding there's no room.
		head, err := readUint32At(w.st, offHead)
		if err != nil {
			return 0, err
		}
		w.cachedHead = head
		free = w.capacity - int64(w.tail-w.cachedHead)
	}
	if free <= 0 {
		return 0, nil
	}

	n := int64(len(p))
	if n > free {
		n = free
	}

	start := int64(w.tail) & w.mask
	if start+n <= w.capacity {
		if _, err := w.st.WriteAt(p[:n], headerSize+start); err != nil {
			return 0, err
		}
	} else {
		first := w.capacity - start
		if _, err := w.st.WriteAt(p[:first], headerSize+start); err != nil {
			return 0, err
		}
		if _, err := w.st.WriteAt(p[first:n], headerSize); err != nil {
			return 0, err
		}
	}

	w.tail += uint32(n)
	if err := writeUint32At(w.st, offTail, w.tail); err != nil {
		return 0, err
	}
	return int(n), nil
}

// Write writes all of p to the ring buffer, blocking until space is
// available. It implements io.Writer. Write only returns a short count
// alongside a non-nil error; on success n == len(p).
func (w *Writer) Write(p []byte) (int, error) {
	return w.WriteContext(context.Background(), p)
}

// WriteContext is like Write but returns ctx.Err() if ctx is done before
// all of p has been written.
func (w *Writer) WriteContext(ctx context.Context, p []byte) (int, error) {
	written := 0
	wait := w.opt.minPoll
	for written < len(p) {
		n, err := w.TryWrite(p[written:])
		written += n
		if err != nil {
			return written, err
		}
		if n > 0 {
			wait = w.opt.minPoll
			continue
		}
		if err := ctx.Err(); err != nil {
			return written, err
		}
		select {
		case <-ctx.Done():
			return written, ctx.Err()
		case <-time.After(wait):
		}
		if wait *= 2; wait > w.opt.maxPoll {
			wait = w.opt.maxPoll
		}
	}
	return written, nil
}

// Close marks the ring buffer as closed. Any data already written remains
// available for the Reader to drain; once drained, the Reader's Read calls
// return ErrClosed. Close does not release the underlying storage — call
// Storage.Close (or keep using the Writer's storage via Reader) separately
// once both sides are done, since the Reader may still be reading.
func (w *Writer) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	return writeUint32At(w.st, offClosed, 1)
}

// CloseStorage closes the underlying storage in addition to marking the
// ring buffer closed. For OS shared memory this unmaps the segment and, on
// the creating side, removes it. Call this once no other process still
// needs the storage.
func (w *Writer) CloseStorage() error {
	if err := w.Close(); err != nil {
		return err
	}
	return w.st.Close()
}
