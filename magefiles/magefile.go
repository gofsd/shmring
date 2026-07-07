//go:build mage

package main

import (
	"github.com/magefile/mage/sh"
)

// Build compiles all packages.
func Build() error {
	return sh.RunV("go", "build", "./...")
}

// Test runs the test suite.
func Test() error {
	return sh.RunV("go", "test", "./...")
}

// TestRace runs the test suite with the race detector enabled.
func TestRace() error {
	return sh.RunV("go", "test", "-race", "-count=1", "./...")
}

// Vet runs go vet.
func Vet() error {
	return sh.RunV("go", "vet", "./...")
}

// Lint runs golangci-lint.
func Lint() error {
	return sh.RunV("golangci-lint", "run")
}

// Tidy runs go mod tidy.
func Tidy() error {
	return sh.RunV("go", "mod", "tidy")
}

// Examples builds the producer and consumer example binaries into bin/.
func Examples() error {
	if err := sh.RunV("go", "build", "-o", "bin/producer", "./examples/producer"); err != nil {
		return err
	}
	return sh.RunV("go", "build", "-o", "bin/consumer", "./examples/consumer")
}

// Clean removes build artifacts.
func Clean() error {
	return sh.Rm("bin")
}
