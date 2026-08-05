//go:build linux && !android

package shmring_test

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/gofsd/shmring"
)

// partialOpenEnv marks the re-executed child process that performs the
// actual OpenShm call. It has to run in its own process because the
// failure this test pins is a SIGBUS, which Go cannot recover from: a
// regression kills the whole test binary rather than failing one test.
const (
	partialOpenEnv  = "SHMRING_PARTIAL_OPEN_CHILD"
	partialNameEnv  = "SHMRING_PARTIAL_OPEN_NAME"
	partialCapacity = 4096
)

// TestOpenShmRefusesASegmentShorterThanTheMapping pins that OpenShm never
// maps more of a /dev/shm file than the file actually contains.
//
// mmap happily creates a mapping that extends past end-of-file; the fault
// only arrives when something touches a page beyond the file's last one,
// and on Linux that fault is SIGBUS (BUS_ADRERR), not a page-in. Go turns
// that into "fatal error: fault" and the process dies -- no error return,
// no recover, no stack unwinding for anything else running in it.
//
// CreateShm opens the file with O_CREATE and only then Truncates it to
// size, so between those two syscalls the segment exists at length 0.
// OpenShm used to map its full requested size regardless, which made any
// consumer that races the creator -- exactly what a retry-until-it-exists
// open loop does, since the file becoming visible is the thing it is
// waiting for -- crash inside verifyHeader's very first ReadAt:
//
//	unexpected fault address 0x7f87641de000
//	fatal error: fault
//	[signal SIGBUS: bus error code=0x2]
//	runtime.memmove()
//	backend.(*ShmStorage).ReadAt      shm_linux.go:119
//	shmring.verifyHeader              header.go:78
//	shmring.OpenShm                   shm_native.go:44
//
// That is a real trace from github.com/gofsd/libp2p-kv-raft's IPC layer,
// whose openRespWithRetry polls OpenShm waiting for the daemon to create
// the response ring. Under v0.1.0 an early open failed with an error and
// the loop retried; the mapping is what changed underneath it.
//
// A short file is the same condition as a segment created with a smaller
// capacity than the one being opened, so the guard also turns that
// mismatch into an immediate error instead of a SIGBUS later on, once a
// read reaches past the real end.
func TestOpenShmRefusesASegmentShorterThanTheMapping(t *testing.T) {
	if os.Getenv(partialOpenEnv) == "1" {
		runPartialOpenChild(t)
		return
	}

	name := fmt.Sprintf("shmring-partial-open-%d", time.Now().UnixNano())
	path := "/dev/shm/" + name

	// The segment as CreateShm leaves it in the window before Truncate:
	// present, openable, and zero bytes long.
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatalf("create the zero-length segment: %v", err)
	}
	f.Close()
	defer os.Remove(path)

	cmd := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$", "-test.v")
	cmd.Env = append(os.Environ(), partialOpenEnv+"=1", partialNameEnv+"="+name)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return
	}
	if strings.Contains(string(out), "signal SIGBUS") || strings.Contains(err.Error(), "bus error") {
		t.Fatalf("OpenShm mapped past the end of a segment the creator has not sized yet, and faulted: %v\n"+
			"a consumer polling for a segment that is still being created cannot survive this -- see this test's doc comment\n%s", err, out)
	}
	t.Fatalf("child process failed: %v\n%s", err, out)
}

func runPartialOpenChild(t *testing.T) {
	name := os.Getenv(partialNameEnv)
	if name == "" {
		t.Fatalf("%s must be set in the child process", partialNameEnv)
	}

	r, err := shmring.OpenShm(name, partialCapacity)
	if err != nil {
		// The whole point: a plain error, which a retry loop can act on.
		return
	}
	r.Close()
	t.Fatalf("OpenShm succeeded on a zero-length segment while mapping a header plus %d bytes of capacity; every byte it hands out past the end of the file is a SIGBUS waiting to happen",
		partialCapacity)
}
