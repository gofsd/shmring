//go:build mage

package main

import (
	"github.com/magefile/mage/mg"
	"github.com/magefile/mage/sh"
)

// Rust namespace targets the crate under rust/ (see rust/README.md).
type Rust mg.Namespace

const rustManifest = "rust/Cargo.toml"

// Build compiles the rust crate.
func (Rust) Build() error {
	return sh.RunV("cargo", "build", "--manifest-path", rustManifest)
}

// Test runs the rust crate's test suite: unit tests, rust/tests/*
// (including the real POSIX shared-memory round trip), and doctests.
func (Rust) Test() error {
	return sh.RunV("cargo", "test", "--manifest-path", rustManifest)
}

// Lint runs clippy (denying warnings) and checks rustfmt formatting.
func (Rust) Lint() error {
	if err := sh.RunV("cargo", "clippy", "--manifest-path", rustManifest, "--all-targets", "--", "-D", "warnings"); err != nil {
		return err
	}
	return sh.RunV("cargo", "fmt", "--manifest-path", rustManifest, "--check")
}

// Clean removes the crate's build artifacts (target/).
func (Rust) Clean() error {
	return sh.RunV("cargo", "clean", "--manifest-path", rustManifest)
}
