package shmring

import (
	"context"
	"io"
	"time"

	"github.com/gofsd/shmring/backend"
)

// Reader is the consumer side of a ring buffer. A Reader must only be used
// from a single goroutine at a time.
type Reader struct {
	st       backend.Storage
	capacity int64
	mask     int64
	opt      options

	head       uint32 // local authoritative copy; persisted to storage on every read
	cachedTail uint32 // last tail value observed from storage
}

// NewReader attaches to a ring buffer previously initialized by NewWriter
// on the same storage. capacity must match the value the writer used.
//
// NewReader is the low-level entry point used by OpenShm; use it directly
// when running the ring buffer over a custom backend.Storage.
func NewReader(st backend.Storage, capacity int64, opts ...Option) (*Reader, error) {
	if err := validateCapacity(capacity); err != nil {
		return nil, err
	}
	if st.Size() < headerSize+capacity {
		return nil, ErrStorageTooSmall
	}
	if err := verifyHeader(st, capacity); err != nil {
		return nil, err
	}
	o := defaultOptions()
	for _, opt := range opts {
		opt(&o)
	}
	return &Reader{
		st:       st,
		capacity: capacity,
		mask:     capacity - 1,
		opt:      o,
	}, nil
}

// TryRead reads as many bytes as are currently available into p without
// blocking, returning the number of bytes read. It returns (0, nil) if the
// buffer is empty and still open. Once the buffer is empty and the Writer
// has been closed, it returns (0, io.EOF).
func (r *Reader) TryRead(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	avail := int64(r.cachedTail - r.head)
	if avail < int64(len(p)) {
		// The cached tail may be stale (the writer has produced more than
		// we've observed); refresh it before concluding there's no data.
		tail, err := readUint32At(r.st, offTail)
		if err != nil {
			return 0, err
		}
		r.cachedTail = tail
		avail = int64(r.cachedTail - r.head)
	}
	if avail <= 0 {
		closed, err := readUint32At(r.st, offClosed)
		if err != nil {
			return 0, err
		}
		if closed != 0 {
			return 0, io.EOF
		}
		return 0, nil
	}

	n := int64(len(p))
	if n > avail {
		n = avail
	}

	start := int64(r.head) & r.mask
	if start+n <= r.capacity {
		if _, err := r.st.ReadAt(p[:n], headerSize+start); err != nil {
			return 0, err
		}
	} else {
		first := r.capacity - start
		if _, err := r.st.ReadAt(p[:first], headerSize+start); err != nil {
			return 0, err
		}
		if _, err := r.st.ReadAt(p[first:n], headerSize); err != nil {
			return 0, err
		}
	}

	r.head += uint32(n)
	if err := writeUint32At(r.st, offHead, r.head); err != nil {
		return 0, err
	}
	return int(n), nil
}

// Read blocks until at least one byte is available and reads into p. It
// implements io.Reader, including returning io.EOF once the Writer has
// closed and all buffered data has been drained.
func (r *Reader) Read(p []byte) (int, error) {
	return r.ReadContext(context.Background(), p)
}

// ReadContext is like Read but returns ctx.Err() if ctx is done before any
// data becomes available.
func (r *Reader) ReadContext(ctx context.Context, p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	wait := r.opt.minPoll
	for {
		n, err := r.TryRead(p)
		if n > 0 || err != nil {
			return n, err
		}
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(wait):
		}
		if wait *= 2; wait > r.opt.maxPoll {
			wait = r.opt.maxPoll
		}
	}
}

// Close releases the Reader's handle on the underlying storage. For OS
// shared memory this unmaps the segment (it does not remove it; only the
// creating Writer's CloseStorage does that).
func (r *Reader) Close() error {
	return r.st.Close()
}
