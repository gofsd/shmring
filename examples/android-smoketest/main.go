//go:build android

// Command android-smoketest is a direct, single-process check that
// shmring's Android backend (backend/android.go, ASharedMemory-based --
// see its doc comment for why hidez8891/shm's POSIX shm_open backend
// doesn't work on Android at all) actually works on-device: it creates a
// ring buffer, reuses the resulting fd to open a second handle onto the
// same region (standing in for handing that fd to another thread/process,
// since ASharedMemory has no name-based rendezvous to open by), writes
// through one, and reads back through the other.
//
// Meant to be cross-compiled for android/arm64 (API 26+, ASharedMemory's
// minimum) and run via `adb shell`, not run directly:
//
//	CC=$ANDROID_NDK_HOME/toolchains/llvm/prebuilt/linux-x86_64/bin/aarch64-linux-android26-clang \
//	  GOOS=android GOARCH=arm64 CGO_ENABLED=1 \
//	  go build -o android-smoketest ./examples/android-smoketest
//	adb push android-smoketest /data/local/tmp/
//	adb shell /data/local/tmp/android-smoketest
package main

import (
	"fmt"
	"io"
	"log"
	"os"

	"github.com/gofsd/shmring"
)

func main() {
	const capacity = 4096
	const message = "hello from ASharedMemory\n"

	w, fd, err := shmring.CreateAndroidSharedMemory("shmring-smoketest", capacity)
	if err != nil {
		log.Fatalf("CreateAndroidSharedMemory: %v", err)
	}
	defer w.CloseStorage()

	r, err := shmring.OpenAndroidSharedMemory(fd, capacity)
	if err != nil {
		log.Fatalf("OpenAndroidSharedMemory: %v", err)
	}
	defer r.Close()

	if _, err := w.Write([]byte(message)); err != nil {
		log.Fatalf("Write: %v", err)
	}
	w.Close()

	got, err := io.ReadAll(r)
	if err != nil {
		log.Fatalf("ReadAll: %v", err)
	}

	if string(got) != message {
		fmt.Printf("FAIL: wrote %q, read back %q\n", message, got)
		os.Exit(1)
	}
	fmt.Printf("PASS: wrote and read back %q through real ASharedMemory-backed shared memory\n", got)
}
