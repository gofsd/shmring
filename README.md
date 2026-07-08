# shmring

`shmring` is a fixed-capacity, single-producer/single-consumer byte ring
buffer for Go, built on top of [`github.com/hidez8891/shm`](https://github.com/hidez8891/shm)
for cross-process shared memory.

One process creates the ring buffer and gets a `Writer`; another process
opens the same named segment and gets a `Reader`. Bytes written on one side
become readable on the other, in order, with no sockets, pipes, or copies
through the kernel beyond the initial `mmap`.

## Platform support

| Platform | Transport | Status |
| --- | --- | --- |
| Linux, macOS, Windows | OS shared memory (`hidez8891/shm`, cgo) | native, `CreateShm`/`OpenShm`, CI-tested |
| Web (browser) | `SharedArrayBuffer` + `Atomics` (Go compiled to `js/wasm`) | native, `CreateSharedArrayBuffer`/`OpenSharedArrayBuffer`, browser-tested — see [Web](#web) |
| Android | `ASharedMemory` (cgo), fd-based, via `gomobile bind` | compiles against the real NDK and produces a real AAR; **runtime behavior unverified on-device** — see [Android](#android) |

Same underlying Go source and ring buffer algorithm everywhere; only the
storage backend and the surface exposed to the host language differ (Go on
desktop, JavaScript on web via generated bindings, Kotlin/Java on Android
via `gomobile`).

## Install

```sh
go get github.com/gofsd/shmring
```

## Quick start

```go
// process A (producer)
w, err := shmring.CreateShm("my-channel", 4096) // capacity must be a power of two
if err != nil {
    log.Fatal(err)
}
defer w.CloseStorage() // removes the OS shared-memory segment

w.Write([]byte("hello\n"))
w.Close() // signal EOF to the reader once done

// process B (consumer)
r, err := shmring.OpenShm("my-channel", 4096)
if err != nil {
    log.Fatal(err)
}
defer r.Close()

io.Copy(os.Stdout, r) // reads until the writer closes and the buffer drains
```

See [`examples/producer`](examples/producer) and
[`examples/consumer`](examples/consumer) for a runnable two-process demo:

```sh
go run ./examples/producer &
go run ./examples/consumer
```

## Web

`web/wasm` compiles the same `shmring.Writer`/`Reader` code to WebAssembly
for use in a browser. Go's `js/wasm` target is single-threaded, so two
separate WASM instances (e.g. one on the main thread, one in a Web Worker)
can't literally share Go's linear memory the way two native processes share
an `mmap`'d segment — instead, the ring buffer's storage lives in a
JavaScript `SharedArrayBuffer`, and head/tail coordination goes through
`Atomics.load`/`Atomics.store` (`backend.SharedArrayBufferStorage`, used via
`shmring.CreateSharedArrayBuffer`/`OpenSharedArrayBuffer`). That's the web
platform's actual cross-thread visibility guarantee — stronger than the
"aligned access is coherent" argument the native OS-shared-memory backend
relies on, not weaker.

`web/shmring.js` is a thin ES module wrapper (`loadShmring`, `Writer`,
`Reader`, `createWriter`, `openReader`) mirroring the Go API as closely as
JS idiom allows. See [`web/example`](web/example) for a working
main-thread-Writer / Worker-Reader page.

```sh
mage web:build            # -> web/example/shmring.wasm
mage web:serve             # http://localhost:8080/example/
```

**Requires cross-origin isolation.** Browsers only expose
`SharedArrayBuffer` on pages served with
`Cross-Origin-Opener-Policy: same-origin` and
`Cross-Origin-Embedder-Policy: require-corp` (`web/devserver` sets both for
local testing; your production server needs to as well).

```js
// main thread
import { loadShmring, createWriter } from "./shmring.js";
const raw = await loadShmring("shmring.wasm");
const { writer, sab } = createWriter(raw, 4096);
worker.postMessage({ sab, capacity: 4096 });
await writer.write(new TextEncoder().encode("hello\n"));
writer.close();

// worker.js
import { loadShmring, openReader } from "./shmring.js";
const raw = await loadShmring("shmring.wasm");
self.onmessage = async ({ data: { sab, capacity } }) => {
  const reader = openReader(raw, sab, capacity);
  const buf = new Uint8Array(64);
  const { n, eof } = await reader.read(buf);
  // ...
};
```

`mage web:test` builds the wasm module, runs the native test suite, then
drives a real headless Chrome (`web/e2e`, via `puppeteer-core`) through the
example page end to end — confirming actual data crosses the
main-thread/Worker boundary, not just that things compile. Run
`npm install` in `web/e2e` once first.

## Android

`GOOS=android` picks up Go's `linux` build tag too (a long-standing special
case in the toolchain's build-constraint matching), so the first version of
this support just reused `hidez8891/shm`'s Linux backend as-is. That does
not work: bionic libc's own headers say so directly —
`sys/posix_limits.h` defines `_POSIX_SHARED_MEMORY_OBJECTS` as
`__BIONIC_POSIX_FEATURE_MISSING`, with the comment *"mmap/munmap are
implemented, but shm_open/shm_unlink are not."* Confirmed by cross-compiling
against a real NDK: it fails at the `import "C"` step, not at link time.
[`backend/shm.go`](backend/shm.go) now explicitly excludes Android from the
Linux/macOS/Windows build tag.

**The real backend** ([`backend/android.go`](backend/android.go)) uses
Android's actual shared-memory API, `<android/sharedmem.h>`
(`ASharedMemory_create` + `mmap`, both available since API 26). The
important shape difference from `CreateShm`/`OpenShm`: `ASharedMemory` has
no name-based rendezvous — `ASharedMemory_create`'s name argument is a
debug label only, visible in `/proc/<pid>/maps`, not something a second
call can open by name. Sharing a region means handing over its file
descriptor directly: trivial within a process, and across processes
normally means your Java/Kotlin layer sending it over Binder as a
`ParcelFileDescriptor` (that plumbing is app-specific and outside this
library's scope). Hence `shm_android.go`'s constructors return/accept an
fd rather than a name:

```go
w, fd, err := shmring.CreateAndroidSharedMemory("my-buffer", 4096)
// hand fd to whoever should be the Reader
r, err := shmring.OpenAndroidSharedMemory(fd, 4096)
```

[`mobile/mobile.go`](mobile/mobile.go) wraps that for `gomobile bind`
(gobind, gomobile's binding generator, doesn't support Go's multi-value
returns beyond `(value, error)`, so `CreateSharedMemory` returns a
`CreateResult{Writer, Fd}` struct instead of a 3-tuple). The generated Java
API:

```java
Mobile.CreateResult result = Mobile.createSharedMemory("my-buffer", 4096);
Writer writer = result.getWriter();
long fd = result.getFd();
// ... send fd to another process via ParcelFileDescriptor ...
Reader reader = Mobile.openSharedMemory(fd, 4096);
```

```sh
go install golang.org/x/mobile/cmd/gomobile@latest
go get -tool golang.org/x/mobile/cmd/gobind   # records a tool dependency in go.mod
gomobile init

sdkmanager --install "ndk;28.2.13676358"
export ANDROID_NDK_HOME=$ANDROID_SDK_ROOT/ndk/28.2.13676358

mage android:build   # -> bin/android/shmring.aar
```

### Verification status

**Confirmed:** `backend/android.go` and `mobile/mobile.go` cross-compile
cleanly against a real NDK (28.2.13676358, targeting API 26, the
`ASharedMemory_create` minimum — an API-24 target hides the declaration
entirely and fails cgo's type-checking rather than giving a clear
availability error) and link against the real bionic sysroot.
`gomobile bind` produces a complete, real `.aar`: native `libgojni.so` for
all four Android ABIs (armeabi-v7a, arm64-v8a, x86, x86_64) plus the
generated Java bindings shown above.

**Not confirmed:** that `ASharedMemory_create`/`mmap` actually behave
correctly at runtime on Android. Verifying that needs a device or emulator,
and two attempts on an AVD (`Pixel_9`, both with and without KVM
acceleration) ended in the *emulator itself* segfaulting during boot
(`SIGSEGV`, exit 139) — a crash in QEMU/the emulator, unrelated to this
library — with no physical device available as a fallback. Until someone
runs it on a real device or a working emulator, treat Android's runtime
behavior as unverified. [`examples/android-smoketest`](examples/android-smoketest)
is a ready-made single-process round-trip check for whoever does that
next:

```sh
CC=$ANDROID_NDK_HOME/toolchains/llvm/prebuilt/linux-x86_64/bin/aarch64-linux-android26-clang \
  GOOS=android GOARCH=arm64 CGO_ENABLED=1 \
  go build -o android-smoketest ./examples/android-smoketest
adb push android-smoketest /data/local/tmp/
adb shell /data/local/tmp/android-smoketest
```

## API

- `CreateShm(name string, capacity int64, opts ...Option) (*Writer, error)`
  creates a new shared-memory ring buffer.
- `OpenShm(name string, capacity int64, opts ...Option) (*Reader, error)`
  opens one created by `CreateShm`.
- `*Writer` implements `io.Writer` (`Write`), plus non-blocking `TryWrite`
  and a cancellable `WriteContext`.
- `*Reader` implements `io.Reader` (`Read`), plus non-blocking `TryRead`
  and a cancellable `ReadContext`. `Read` returns `io.EOF` once the writer
  has closed and all buffered data has been drained.
- `Writer.Close` marks the ring buffer closed (readable data already
  written is still drained normally); `Writer.CloseStorage` additionally
  releases the OS shared-memory segment and should be called once, by
  whichever side created it, after the other side is done.
- `CreateSharedArrayBuffer(capacity int64, opts ...Option) (*Writer, js.Value, error)`
  and `OpenSharedArrayBuffer(sab js.Value, capacity int64, opts ...Option) (*Reader, error)`
  are the `js/wasm`-only equivalents of `CreateShm`/`OpenShm` (see
  [Web](#web)); `*Writer`/`*Reader` are otherwise identical.

## Design

**Pluggable storage.** The ring buffer algorithm never talks to OS shared
memory directly — it depends only on the small `backend.Storage` interface
(`ReadAt`/`WriteAt`/`Size`/`Close`). `CreateShm`/`OpenShm` use
`backend.ShmStorage`, backed by `hidez8891/shm`; `CreateSharedArrayBuffer`/
`OpenSharedArrayBuffer` use `backend.SharedArrayBufferStorage`.
`NewWriter`/`NewReader` accept any `backend.Storage`, including
`backend.MemStorage`, an in-process byte-slice backend used by this
package's own tests. This is the extension point for the future: a new
platform or transport means adding a new `backend.Storage` implementation,
not touching the ring buffer logic — which is exactly how the web backend
was added.

**Platform support** is summarized in the table at the top; CI
(`.github/workflows/ci.yml`) runs the native test suite on Linux, macOS,
and Windows with `CGO_ENABLED=1` (the underlying `hidez8891/shm` library
uses cgo).

**Concurrency model.** A ring buffer has exactly one `Writer` and one
`Reader`, each used from a single goroutine (or, in the browser, thread) at
a time — this is a single-producer/single-consumer (SPSC) structure, not a
general-purpose concurrent queue. Head/tail/closed are 32-bit counters
(`backend.AtomicStorage`, an optional capability) rather than 64-bit, so
they fit a JavaScript `Int32Array`; correctness only depends on
`tail-head`, which never approaches 2^31 as long as capacity does (enforced
at construction). Coordination goes through plain, 4-byte aligned loads and
stores on `ShmStorage`/`MemStorage`, because the underlying shm library
only exposes copy-based `ReadAt`/`WriteAt`, not a raw pointer into the
mapping. This mirrors how classic SPSC ring buffers over shared memory
(e.g. Linux `kfifo`) work, and holds on every architecture Go currently
targets, but it is a weaker guarantee than `sync/atomic` gives within a
single process — which is exactly why `SharedArrayBufferStorage` does *not*
take this shortcut, and uses real JavaScript `Atomics` instead (see [Web](#web)).
The
in-process `backend.MemStorage` backend compensates for that gap with an
internal mutex, since two goroutines in one process *do* need a Go
memory-model-legal happens-before edge, unlike two OS processes sharing
real mapped memory.

**Blocking calls poll.** There's no cross-process wakeup primitive
available through shared memory alone, so `Write`/`Read` (and their
`Context` variants) block by polling the shared counters with an
exponential backoff (tunable via `WithPollInterval`). Use `TryWrite`/
`TryRead` if busy-polling isn't acceptable for your use case.

## Development

This repo uses [Mage](https://magefile.org) instead of Make. The magefile
lives in `magefiles/` as its own Go module, so the `mage` dependency never
leaks into `shmring`'s own `go.mod`.

```sh
go install github.com/magefile/mage@latest

mage -l          # list targets
mage build
mage test
mage testRace
mage vet
mage lint        # requires golangci-lint
mage examples    # builds bin/producer and bin/consumer
```

The top-level targets above build/test using whatever platform you're
already on. Alongside them, each supported platform has its own namespace
(`mage -l` lists all of them):

```sh
mage linux:build    mage linux:test     mage linux:lint     mage linux:clean
mage darwin:build   mage darwin:test    mage darwin:lint    mage darwin:clean
mage windows:build  mage windows:test   mage windows:lint   mage windows:clean
mage android:build  mage android:test   mage android:lint   mage android:clean
mage web:build      mage web:test       mage web:lint       mage web:clean   mage web:serve
```

What actually runs depends on what's installed where you invoke it:

- **linux**: fully native on a Linux host.
- **windows**: cross-compiles from any host with `mingw-w64`
  (`x86_64-w64-mingw32-gcc`); `test` additionally *runs* the suite if
  `wine`/`wine64` is on PATH, otherwise it falls back to `go vet` (compiles
  and type-checks, including test files, without executing anything).
- **darwin**: needs a real Apple toolchain for cgo (no practical open
  cross-compiler exists), so `build`/`test` are only expected to work when
  run on macOS itself — CI covers this on a `macos-latest` runner instead.
- **android**: needs `gomobile` and an installed NDK (`ANDROID_NDK_HOME`);
  `build`/`test` fail with a specific, actionable error naming whichever is
  missing rather than a raw toolchain error. With both present, `build`
  genuinely produces `bin/android/shmring.aar` — see [Android](#android)
  for what that does and doesn't confirm.
- **web**: pure Go, no cgo, cross-compiles from anywhere; `test` additionally
  needs Node.js and a Chrome/Chromium binary (`CHROME_PATH` env var if it's
  not in a standard location) to run the real-browser check in `web/e2e`.

## License

MIT, see [LICENSE](LICENSE).
