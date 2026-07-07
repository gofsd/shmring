// Command devserver serves the shmring web/ directory (wasm_exec.js,
// shmring.js, shmring.wasm, and the example/ page) with the
// Cross-Origin-Opener-Policy and Cross-Origin-Embedder-Policy response
// headers that SharedArrayBuffer requires. Browsers won't expose
// SharedArrayBuffer on a page that isn't cross-origin isolated, so a plain
// static file server isn't enough to try the example locally.
package main

import (
	"flag"
	"log"
	"net/http"
	"path/filepath"
	"runtime"
)

func main() {
	addr := flag.String("addr", "localhost:8080", "listen address")
	dir := flag.String("dir", defaultWebDir(), "directory to serve (the shmring web/ directory)")
	flag.Parse()

	fs := http.FileServer(http.Dir(*dir))
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		w.Header().Set("Cross-Origin-Embedder-Policy", "require-corp")
		fs.ServeHTTP(w, r)
	}

	log.Printf("serving %s", *dir)
	log.Printf("open http://%s/example/ (build shmring.wasm into web/example/ first, e.g. `mage web:build`)", *addr)
	log.Fatal(http.ListenAndServe(*addr, http.HandlerFunc(handler)))
}

// defaultWebDir anchors on this source file's location so the server finds
// web/ correctly regardless of the caller's working directory.
func defaultWebDir() string {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "."
	}
	return filepath.Dir(filepath.Dir(thisFile)) // .../web/devserver -> .../web
}
