package shmring

import "errors"

var (
	// ErrClosed is returned by Write/Read operations performed after the
	// Writer has been closed and, for reads, once all buffered data has
	// been drained.
	ErrClosed = errors.New("shmring: ring buffer closed")

	// ErrInvalidCapacity is returned when a requested capacity is not a
	// power of two, or is not positive.
	ErrInvalidCapacity = errors.New("shmring: capacity must be a positive power of two")

	// ErrHeaderMismatch is returned by NewReader/OpenShm when the header
	// found in storage doesn't match what this version of shmring expects
	// (wrong magic, incompatible format version, or a capacity different
	// from the one requested).
	ErrHeaderMismatch = errors.New("shmring: storage header does not match expected ring buffer format")

	// ErrStorageTooSmall is returned when the provided storage is smaller
	// than headerSize+capacity.
	ErrStorageTooSmall = errors.New("shmring: storage is too small for the requested capacity")
)
