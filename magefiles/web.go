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

// Web namespace targets the js/wasm build used in a browser (see web/).
type Web mg.Namespace

const wasmOut = "web/example/shmring.wasm"

// Build compiles web/wasm to web/example/shmring.wasm.
func (Web) Build() error {
	return runEnv(map[string]string{"GOOS": "js", "GOARCH": "wasm"}, "go", "build", "-o", wasmOut, "./web/wasm")
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

// Lint runs golangci-lint against the whole module, including the
// js/wasm-only files (which `go vet`/tests alone won't type-check on a
// non-wasm host).
func (Web) Lint() error {
	if err := runEnv(map[string]string{"GOOS": "js", "GOARCH": "wasm"}, "go", "vet", "./.", "./backend/...", "./web/wasm/..."); err != nil {
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

// Clean removes the compiled wasm artifact.
func (Web) Clean() error {
	return sh.Rm(wasmOut)
}

// Npm namespace assembles the publishable npm package under npm/.
type Npm mg.Namespace

// Build compiles shmring.wasm and copies it plus shmring.js/wasm_exec.js
// into npm/, alongside the package.json/README.md checked into the repo.
// Run this before `npm publish` (from within npm/); the copied files are
// gitignored so the checked-in web/ sources stay the single copy in git.
func (n Npm) Build() error {
	mg.Deps(Web.Build)
	if err := sh.Copy(filepath.Join("npm", "shmring.wasm"), wasmOut); err != nil {
		return err
	}
	if err := sh.Copy(filepath.Join("npm", "shmring.js"), filepath.Join("web", "shmring.js")); err != nil {
		return err
	}
	return sh.Copy(filepath.Join("npm", "wasm_exec.js"), filepath.Join("web", "wasm_exec.js"))
}

// Clean removes npm/'s generated (gitignored) files, leaving package.json
// and README.md in place.
func (Npm) Clean() error {
	for _, f := range []string{"shmring.wasm", "shmring.js", "wasm_exec.js"} {
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
