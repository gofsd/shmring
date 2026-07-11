//go:build mage

package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/magefile/mage/mg"
	"github.com/magefile/mage/sh"
)

// Web namespace targets the wasm32 build used in a browser (see web/),
// compiled from the same rust/ crate as the native/POSIX backend --
// wasm-bindgen exports (rust/src/wasm_api.rs) are cfg-gated to
// wasm32-unknown-unknown and don't affect the native crate at all.
type Web mg.Namespace

const (
	wasmPkgDir = "rust/pkg"
	wasmOutDir = "web"
	wasmOut    = "web/example/shmring.wasm" // filename web/example's HTML/JS hardcode
)

// Build compiles rust/ to wasm via wasm-pack, then copies the generated
// glue (shmring_wasm.js/shmring_wasm_bg.wasm) into web/ alongside the
// hand-written shmring.js wrapper, and a further copy of the .wasm to
// web/example/shmring.wasm (the literal filename index.html/worker.js
// fetch, independent of wherever shmring.js itself lives).
func (Web) Build() error {
	if err := sh.RunV("wasm-pack", "build", "rust", "--target", "web", "--out-name", "shmring_wasm"); err != nil {
		return err
	}
	for _, name := range []string{"shmring_wasm.js", "shmring_wasm_bg.wasm"} {
		if err := sh.Copy(filepath.Join(wasmOutDir, name), filepath.Join(wasmPkgDir, name)); err != nil {
			return err
		}
	}
	return sh.Copy(wasmOut, filepath.Join(wasmPkgDir, "shmring_wasm_bg.wasm"))
}

// Test runs the native (non-wasm) package tests, then a headless-browser
// end-to-end check (web/e2e) that a real Writer and Reader actually
// exchange data across a main-thread/Worker SharedArrayBuffer via the
// compiled wasm module. Requires Node.js and Chrome/Chromium (set
// CHROME_PATH if it's not in one of the usual places); run `npm install`
// in web/e2e once first.
func (w Web) Test() error {
	mg.Deps(w.Build)
	if err := sh.RunV("go", "test", "-race", "-count=1", "./..."); err != nil {
		return err
	}

	// Build the devserver to a real binary and exec it directly rather than
	// via `go run`: go run's process is a wrapper around a grandchild
	// process, so killing it doesn't reliably kill the actual server and
	// leaks a listener behind.
	tmpBin := filepath.Join(os.TempDir(), "shmring-devserver")
	if err := sh.RunV("go", "build", "-o", tmpBin, "./web/devserver"); err != nil {
		return err
	}
	defer os.Remove(tmpBin)

	addr := "localhost:8099"
	srv := exec.Command(tmpBin, "-addr", addr)
	if err := srv.Start(); err != nil {
		return fmt.Errorf("starting devserver: %w", err)
	}
	defer srv.Process.Kill()

	if err := waitForServer("http://"+addr+"/example/", 10*time.Second); err != nil {
		return err
	}

	return sh.RunV("node", "web/e2e/test.mjs", "http://"+addr)
}

// Lint runs golangci-lint against the whole Go module (the wasm-facing
// code now lives in rust/, not Go, so there's no js/wasm build-tagged Go
// left to separately vet here -- see rust:lint for that side), plus
// clippy against the wasm32 target, which nothing else in rust:lint
// exercises since it only ever builds the default/native target.
func (Web) Lint() error {
	if err := sh.RunV("cargo", "clippy", "--manifest-path", rustManifest, "--target", "wasm32-unknown-unknown", "--lib", "--", "-D", "warnings"); err != nil {
		return err
	}
	return runEnv(nil, "golangci-lint", "run")
}

// Serve starts the local dev server (with the COOP/COEP headers
// SharedArrayBuffer requires) at http://localhost:8080/example/. Run
// `mage web:build` first.
func (Web) Serve() error {
	return sh.RunV("go", "run", "./web/devserver")
}

// Clean removes wasm-pack's output directory and the copies of it under
// web/ and web/example/.
func (Web) Clean() error {
	if err := sh.Rm(wasmPkgDir); err != nil {
		return err
	}
	if err := sh.Rm(wasmOut); err != nil {
		return err
	}
	for _, name := range []string{"shmring_wasm.js", "shmring_wasm_bg.wasm"} {
		if err := sh.Rm(filepath.Join(wasmOutDir, name)); err != nil {
			return err
		}
	}
	return nil
}

// Npm namespace assembles the publishable npm package under npm/.
type Npm mg.Namespace

// Build compiles the wasm module and copies shmring_wasm.js/
// shmring_wasm_bg.wasm plus shmring.js (the hand-written wrapper,
// importing the generated shmring_wasm.js) into npm/, alongside the
// package.json/README.md checked into the repo. Run this before `npm
// publish` (from within npm/); the copied files are gitignored so the
// checked-in web/ sources stay the single copy in git. Note this does
// *not* copy web/example/shmring.wasm -- that filename only matters to
// the local web/example/ demo page's hardcoded fetch, not to npm
// consumers, who load wasmURL as exported by shmring.js.
func (n Npm) Build() error {
	mg.Deps(Web.Build)
	if err := sh.Copy(filepath.Join("npm", "shmring_wasm.js"), filepath.Join("web", "shmring_wasm.js")); err != nil {
		return err
	}
	if err := sh.Copy(filepath.Join("npm", "shmring_wasm_bg.wasm"), filepath.Join("web", "shmring_wasm_bg.wasm")); err != nil {
		return err
	}
	return sh.Copy(filepath.Join("npm", "shmring.js"), filepath.Join("web", "shmring.js"))
}

// Clean removes npm/'s generated (gitignored) files, leaving package.json
// and README.md in place.
func (Npm) Clean() error {
	files := []string{"shmring_wasm.js", "shmring_wasm_bg.wasm", "shmring.js"}
	for _, f := range files {
		if err := sh.Rm(filepath.Join("npm", f)); err != nil {
			return err
		}
	}
	return nil
}

func waitForServer(url string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	for {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			resp.Body.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("devserver did not become ready at %s: %w", url, ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}
