//go:build mage

package main

import (
	"fmt"

	"github.com/magefile/mage/mg"
	"github.com/magefile/mage/sh"
)

// Windows namespace targets build/test/lint windows/amd64. Unlike Darwin,
// mingw-w64 makes cgo cross-compilation from Linux genuinely practical.
type Windows mg.Namespace

func (Windows) env() map[string]string {
	env := map[string]string{"GOOS": "windows", "GOARCH": "amd64", "CGO_ENABLED": "1"}
	if lookPath("x86_64-w64-mingw32-gcc") {
		env["CC"] = "x86_64-w64-mingw32-gcc"
	}
	return env
}

// Build cross-compiles the module for windows/amd64. Requires
// x86_64-w64-mingw32-gcc (mingw-w64) for cgo.
func (w Windows) Build() error {
	if !lookPath("x86_64-w64-mingw32-gcc") {
		return fmt.Errorf("windows:build needs mingw-w64 (x86_64-w64-mingw32-gcc not found on PATH); install it (e.g. `dnf install mingw64-gcc` / `apt install gcc-mingw-w64`)")
	}
	return runEnv(w.env(), "go", "build", "./...")
}

// Test cross-compiles and, if wine/wine64 is available, actually runs the
// suite under it via `go test -exec`. Otherwise it falls back to a
// compile-only check (`go test -c`) and says so, since a windows/amd64
// test binary can't run directly on a non-Windows host.
func (w Windows) Test() error {
	env := w.env()
	if !lookPath("x86_64-w64-mingw32-gcc") {
		return fmt.Errorf("windows:test needs mingw-w64 (x86_64-w64-mingw32-gcc not found on PATH)")
	}
	switch {
	case lookPath("wine64"):
		return runEnv(env, "go", "test", "-race", "-count=1", "-exec=wine64", "./...")
	case lookPath("wine"):
		return runEnv(env, "go", "test", "-race", "-count=1", "-exec=wine", "./...")
	default:
		fmt.Println("note: wine not found; type-checking tests with `go vet` instead of running them. Install wine to actually execute the suite, or rely on CI's windows-latest runner.")
		return runEnv(env, "go", "vet", "./...")
	}
}

// Lint runs golangci-lint.
func (Windows) Lint() error {
	return runEnv(nil, "golangci-lint", "run")
}

// Clean removes windows build artifacts.
func (Windows) Clean() error {
	return sh.Rm("bin/windows-amd64")
}
