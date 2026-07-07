//go:build mage

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/magefile/mage/mg"
	"github.com/magefile/mage/sh"
)

// hostTag returns the Android NDK's directory name for this host's
// prebuilt clang toolchain (e.g. "linux-x86_64").
func hostTag() string {
	arch := "x86_64"
	switch runtime.GOOS {
	case "darwin":
		return "darwin-" + arch
	case "windows":
		return "windows-" + arch
	default:
		return "linux-" + arch
	}
}

// Android namespace builds the mobile/ package (see its doc comment for
// why that facade exists) into an AAR via `gomobile bind`.
//
// Build/Test confirm this compiles and links against a real NDK; they
// cannot confirm ASharedMemory behaves correctly at runtime, since that
// needs a device or working emulator -- see mobile/mobile.go's "Verification
// status" section and the README's Android section for what has and hasn't
// been checked.
type Android mg.Namespace

const androidOut = "bin/android/shmring.aar"

// checkToolchain verifies gomobile and the Android NDK are available,
// returning a specific, actionable error naming whichever is missing
// instead of letting the eventual gomobile failure speak for itself.
func (Android) checkToolchain() error {
	if !lookPath("gomobile") {
		return fmt.Errorf(
			"gomobile not found on PATH; install it with:\n" +
				"  go install golang.org/x/mobile/cmd/gomobile@latest\n" +
				"  gomobile init")
	}
	if ndk := findAndroidNDK(); ndk == "" {
		sdkRoot := os.Getenv("ANDROID_SDK_ROOT")
		if sdkRoot == "" {
			sdkRoot = os.Getenv("ANDROID_HOME")
		}
		return fmt.Errorf(
			"Android NDK not found (checked ANDROID_NDK_HOME and %s/ndk/*); install it with:\n"+
				"  sdkmanager --install \"ndk;28.2.13676358\"\n"+
				"and then set ANDROID_NDK_HOME to the installed version's directory", sdkRoot)
	}
	return nil
}

// findAndroidNDK looks for an installed NDK via ANDROID_NDK_HOME, or the
// newest version under $ANDROID_SDK_ROOT/ndk (the layout sdkmanager uses).
func findAndroidNDK() string {
	if ndk := os.Getenv("ANDROID_NDK_HOME"); ndk != "" {
		if _, err := os.Stat(ndk); err == nil {
			return ndk
		}
	}
	sdkRoot := os.Getenv("ANDROID_SDK_ROOT")
	if sdkRoot == "" {
		sdkRoot = os.Getenv("ANDROID_HOME")
	}
	if sdkRoot == "" {
		return ""
	}
	matches, _ := filepath.Glob(filepath.Join(sdkRoot, "ndk", "*"))
	if len(matches) == 0 {
		return ""
	}
	return matches[len(matches)-1] // newest-looking version by lexical sort
}

// Build produces bin/android/shmring.aar via `gomobile bind`.
func (a Android) Build() error {
	if err := a.checkToolchain(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(androidOut), 0o755); err != nil {
		return err
	}
	return runEnv(map[string]string{"ANDROID_NDK_HOME": findAndroidNDK()},
		// -androidapi 26 matches ASharedMemory_create's minimum (see
		// backend/android.go); gomobile's own default target is lower and
		// would otherwise silently produce an AAR that can't use it.
		"gomobile", "bind", "-target=android", "-androidapi", "26", "-o", androidOut, "./mobile")
}

// Test type-checks the mobile facade for android/arm64. It cannot execute
// anything (there's no device/emulator wired into this repo's tooling);
// running it there is a manual step -- see the README.
func (a Android) Test() error {
	if !lookPath("gomobile") {
		fmt.Println("note: gomobile not installed; falling back to a plain cross-compile type-check for android/arm64.")
	}
	ndk := findAndroidNDK()
	if ndk == "" {
		return fmt.Errorf("Android NDK not found; see android:build's error for install instructions")
	}
	// android26: ASharedMemory_create's minimum API level (see
	// backend/android.go) -- a lower target hides the declaration and
	// fails cgo's type-check rather than giving a clear availability error.
	cc := filepath.Join(ndk, "toolchains", "llvm", "prebuilt", hostTag(), "bin", "aarch64-linux-android26-clang")
	return runEnv(map[string]string{
		"GOOS": "android", "GOARCH": "arm64", "CGO_ENABLED": "1", "CC": cc,
	}, "go", "vet", "./mobile/...")
}

// Lint runs golangci-lint.
func (Android) Lint() error {
	return runEnv(nil, "golangci-lint", "run")
}

// Clean removes the built AAR.
func (Android) Clean() error {
	return sh.Rm(filepath.Dir(androidOut))
}
