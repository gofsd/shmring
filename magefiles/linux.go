//go:build mage

package main

import (
	"github.com/magefile/mage/mg"
	"github.com/magefile/mage/sh"
)

// Linux namespace targets build/test/lint amd64 Linux, the platform the
// hidez8891/shm cgo backend was originally written for. Test genuinely
// executes the suite when run on a Linux host; on another host it still
// verifies the code cross-compiles.
type Linux mg.Namespace

func (Linux) env() map[string]string {
	return map[string]string{"GOOS": "linux", "GOARCH": "amd64", "CGO_ENABLED": "1"}
}

// Build cross-compiles the module for linux/amd64.
func (l Linux) Build() error {
	return runEnv(l.env(), "go", "build", "./...")
}

// Test runs the race-enabled test suite for linux/amd64. Only actually
// executes the tests when the host is linux/amd64; otherwise go test will
// fail to run the resulting binaries locally.
func (l Linux) Test() error {
	return runEnv(l.env(), "go", "test", "-race", "-count=1", "./...")
}

// Lint runs golangci-lint (a host tool, not itself cross-compiled).
func (Linux) Lint() error {
	return runEnv(nil, "golangci-lint", "run")
}

// Clean removes linux build artifacts.
func (Linux) Clean() error {
	return sh.Rm("bin/linux-amd64")
}
