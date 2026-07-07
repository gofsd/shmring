//go:build mage

package main

import (
	"fmt"
	"runtime"

	"github.com/magefile/mage/mg"
	"github.com/magefile/mage/sh"
)

// Darwin namespace targets build/test/lint macOS (arm64). Building it uses
// cgo (hidez8891/shm's Darwin backend), which needs a real Apple SDK/
// toolchain -- there is no widely-usable open cross-compiler for it, so
// unlike Windows this genuinely needs to run on a Mac (this repo's CI runs
// it on a macos-latest GitHub Actions runner for that reason).
type Darwin mg.Namespace

func (Darwin) env() map[string]string {
	return map[string]string{"GOOS": "darwin", "GOARCH": "arm64", "CGO_ENABLED": "1"}
}

// Build compiles the module for darwin/arm64. Must run on macOS.
func (d Darwin) Build() error {
	if runtime.GOOS != "darwin" {
		fmt.Println("note: cross-compiling cgo for darwin from a non-Darwin host requires an Apple SDK/toolchain (e.g. osxcross), which isn't set up here; this will likely fail unless run on macOS.")
	}
	return runEnv(d.env(), "go", "build", "./...")
}

// Test runs the race-enabled test suite for darwin/arm64. Must run on macOS.
func (d Darwin) Test() error {
	return runEnv(d.env(), "go", "test", "-race", "-count=1", "./...")
}

// Lint runs golangci-lint.
func (Darwin) Lint() error {
	return runEnv(nil, "golangci-lint", "run")
}

// Clean removes darwin build artifacts.
func (Darwin) Clean() error {
	return sh.Rm("bin/darwin-arm64")
}
