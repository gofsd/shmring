package backend

import (
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// This file backs backend.WaiterStorage for shm_linux.go's ShmStorage and
// android.go's AndroidSharedMemoryStorage, both of which hold a real
// mmap'd []byte and so have a stable address to futex(2) on.
//
// "_linux.go" (like an explicit "//go:build linux") also applies to
// GOOS=android: the toolchain matches android build tags/files as for
// linux, plus android-specific ones (see the README's Android section).
// The futex syscall itself is a plain Linux kernel syscall available on
// Android too -- it's what bionic's own pthread primitives are built on --
// even though bionic is missing the *named* IPC calls (shm_open,
// sem_open) this package works around elsewhere.
//
// FUTEX_WAIT/FUTEX_WAKE are used without FUTEX_PRIVATE_FLAG deliberately:
// the private variants assume both the waiter and the waker are the same
// process (they skip a VM lookup by hashing on the process's mm plus the
// address), which does not hold here -- these words are shared across
// independent processes. Using the private flag for a cross-process futex
// is a real, documented bug class (missed wakeups), not just a missed
// optimization.
const (
	futexWait = 0
	futexWake = 1
)

// wordAt returns a pointer to the uint32 at byte offset off within mem,
// for use as a futex address. Callers must ensure off is 4-byte aligned
// and in range -- true for every offset the ring buffer header defines
// (offHead/offTail/offClosed are all word-aligned by construction).
func wordAt(mem []byte, off int64) *uint32 {
	return (*uint32)(unsafe.Pointer(&mem[off]))
}

// futexWaitWord blocks the calling goroutine while *word == old, waking
// when another thread/process calls futexWakeWord on the same address, or
// when timeout elapses (<= 0 means wait indefinitely). Like the raw
// futex(2) FUTEX_WAIT operation it wraps, it may also return for a
// spurious reason (e.g. EINTR) -- callers must re-check *word themselves,
// exactly as backend.WaiterStorage.Wait documents.
func futexWaitWord(word *uint32, old uint32, timeout time.Duration) {
	var ts *unix.Timespec
	if timeout > 0 {
		t := unix.NsecToTimespec(timeout.Nanoseconds())
		ts = &t
	}
	_, _, _ = unix.Syscall6(unix.SYS_FUTEX,
		uintptr(unsafe.Pointer(word)),
		uintptr(futexWait),
		uintptr(old),
		uintptr(unsafe.Pointer(ts)),
		0, 0)
}

// futexWakeWord wakes every goroutine/thread/process currently parked in
// futexWaitWord on word. Waking "all" (INT_MAX) rather than one is
// correct and cheap here: a ring buffer has exactly one reader and one
// writer, so there is at most one real waiter on any given word; this
// just avoids having to reason about which single waiter futex's internal
// wake ordering would pick.
func futexWakeWord(word *uint32) {
	const wakeAll = 1<<31 - 1
	_, _, _ = unix.Syscall6(unix.SYS_FUTEX,
		uintptr(unsafe.Pointer(word)),
		uintptr(futexWake),
		uintptr(wakeAll),
		0, 0, 0)
}
