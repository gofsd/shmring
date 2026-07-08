package backend_test

import (
	"io"
	"testing"

	"github.com/gofsd/shmring/backend"
)

func TestMemStorageReadWriteRoundTrip(t *testing.T) {
	var st backend.Storage = backend.NewMemStorage(16)

	if _, err := st.WriteAt([]byte("hello"), 4); err != nil {
		t.Fatalf("WriteAt: %v", err)
	}
	got := make([]byte, 5)
	if _, err := st.ReadAt(got, 4); err != nil {
		t.Fatalf("ReadAt: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("ReadAt = %q, want %q", got, "hello")
	}
	if st.Size() != 16 {
		t.Fatalf("Size = %d, want 16", st.Size())
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestMemStorageWriteAtOutOfBounds(t *testing.T) {
	st := backend.NewMemStorage(8)
	if _, err := st.WriteAt([]byte("toolong!!"), 0); err != io.ErrShortWrite {
		t.Fatalf("WriteAt out of bounds = %v, want io.ErrShortWrite", err)
	}
}

func TestMemStorageReadAtOutOfBounds(t *testing.T) {
	st := backend.NewMemStorage(8)
	buf := make([]byte, 4)
	if _, err := st.ReadAt(buf, 8); err != io.EOF {
		t.Fatalf("ReadAt at end = %v, want io.EOF", err)
	}
}
