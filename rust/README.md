# shmring (Rust)

Rust implementation of [shmring](https://github.com/gofsd/shmring): a
fixed-capacity, single-producer/single-consumer byte ring buffer, using the
same wire format as the Go implementation. A Rust process can exchange
bytes with a Go process over the same named shared-memory segment -- no
sockets, pipes, or copies through the kernel beyond the initial `mmap`.
This crate also compiles to `wasm32-unknown-unknown` for the browser (via
`wasm-bindgen`), published as [`@gofsd/shmring`](https://www.npmjs.com/package/@gofsd/shmring)
on npm -- see [Web](#web) below.

## Platform support

OS shared memory (`create_shm`/`open_shm`) is implemented for Unix (Linux,
macOS) via POSIX `shm_open`. Windows isn't implemented yet.
`Writer::new`/`Reader::new` over a custom [`backend::Storage`] work on every
platform regardless -- only the OS-shared-memory convenience functions are
Unix-only for now. On `wasm32-unknown-unknown`, `backend::SharedArrayBufferStorage`
provides a browser-`SharedArrayBuffer`-backed `Storage` instead (see
[Web](#web)).

## Install

```sh
cargo add shmring
```

## Quick start

```rust
use std::io::{Read, Write};
use shmring::{create_shm, open_shm, Options};

// process A (producer)
let mut w = create_shm("my-channel", 4096, Options::default())?; // capacity must be a power of two
w.write_all(b"hello\n")?;
w.close()?; // signal EOF to the reader once done

// process B (consumer)
let mut r = open_shm("my-channel", 4096, Options::default())?;
let mut out = Vec::new();
r.read_to_end(&mut out)?; // reads until the writer closes and the buffer drains

// once both sides are done, the creating side releases the OS segment:
w.close_storage()?;
```

See [`examples/producer.rs`](examples/producer.rs) and
[`examples/consumer.rs`](examples/consumer.rs) for a runnable two-process
demo:

```sh
cargo run --example producer &
cargo run --example consumer
```

## Web

`src/backend/wasm.rs` implements `Storage` over a JavaScript
`SharedArrayBuffer` (`backend::SharedArrayBufferStorage`), and
`src/wasm_api.rs` exposes it to JavaScript via `wasm-bindgen`
(`WasmWriter`/`WasmReader`, `createWriter`/`openReader`), both compiled
only for `wasm32-unknown-unknown` -- neither affects the native crate or
its crates.io publish at all. Each browser thread (main thread, or a Web
Worker) that wants to be one side of a ring buffer loads its own
independent wasm module instance; coordination between them goes through
real `Atomics.load`/`Atomics.store` on the shared `SharedArrayBuffer`, not
through any Rust-level threading -- this is why plain stable
`wasm32-unknown-unknown` is enough here, no nightly or `-Z build-std`
needed.

You don't have to go through the JS bindings, either: a Rust web app
(compiled to wasm itself, e.g. via Leptos or Yew) can use
`backend::SharedArrayBufferStorage` with `Writer::new`/`Reader::new`
directly, exactly like the native backend.

Build and test the JS-facing package with:

```sh
rustup target add wasm32-unknown-unknown   # once
cargo install wasm-pack                     # once
wasm-pack build rust --target web --out-name shmring_wasm
```

Or via the repo's `mage web:build`/`mage web:test` targets, which drive
this plus [`../web/`](../web)'s example page and a real headless-Chrome
end-to-end check -- see the main [README](../README.md#web).

## API

- `create_shm(name: &str, capacity: u64, options: Options) -> Result<Writer<ShmStorage>>`
  creates a new shared-memory ring buffer.
- `open_shm(name: &str, capacity: u64, options: Options) -> Result<Reader<ShmStorage>>`
  opens one created by `create_shm`.
- `Writer<S>` implements [`std::io::Write`] (blocking, combine with the
  trait's default `write_all` for the equivalent of Go's blocking `Write`),
  plus non-blocking `try_write` and a deadline-bound `write_timeout`.
- `Reader<S>` implements [`std::io::Read`] (blocking; returns `Ok(0)` at
  end-of-stream, once the writer has closed and all buffered data has been
  drained), plus non-blocking `try_read` (returns `Err(Error::Eof)` at the
  same point) and a deadline-bound `read_timeout`.
- `Writer::close` marks the ring buffer closed (readable data already
  written is still drained normally); `Writer::close_storage` additionally
  releases the OS shared-memory segment and should be called once, by
  whichever side created it, after the other side is done.
- `backend::Storage` is the pluggable storage trait; `backend::MemStorage`
  (an in-process, `Clone`-able byte buffer) is what this crate's own tests
  run against, and is a useful `Writer::new`/`Reader::new` backend anywhere
  OS shared memory isn't available or applicable.

## Design

**Pluggable storage.** The ring buffer algorithm never talks to OS shared
memory directly -- it depends only on the `backend::Storage` trait
(`read_at`/`write_at`/`size`/`close`, plus `load_u32_at`/`store_u32_at` for
the head/tail/closed counters, defaulted to a plain `read_at`/`write_at`
pair so most backends need not override them). `create_shm`/`open_shm` use
`backend::ShmStorage`, backed directly by POSIX `shm_open`/`mmap`; the web
build's `createWriter`/`openReader` use `backend::SharedArrayBufferStorage`
(see [Web](#web)), which overrides `load_u32_at`/`store_u32_at` with real
`Atomics.load`/`store` -- the compile-time equivalent of Go's runtime
`backend.AtomicStorage` capability check. `Writer::new`/`Reader::new`
accept any `backend::Storage`, including `backend::MemStorage`. This is
the extension point for a future Windows backend, or any other transport:
add a new `backend::Storage` impl, not touch the ring buffer logic --
exactly how the web backend was added.

**Resource cleanup is RAII, not manual.** Go's `backend.ShmStorage`
requires callers to remember to call `Close`, and Go's own `CreateShm`/
`OpenShm` explicitly clean up a partially constructed storage on error path
by hand. Rust's ownership rules make that unnecessary: `ShmStorage` unmaps
(and, for the creating side, `shm_unlink`s) itself in `Drop`, so a `Writer`/
`Reader` that fails to construct -- or is simply dropped without an
explicit `close()` -- can't leak the mapping. `Storage::close`'s `Result`
return is kept for trait conformance with the Go/JS API shape, not because
failure here is actionable.

**Concurrency model.** A ring buffer has exactly one `Writer` and one
`Reader`, each used from a single thread at a time -- this is a
single-producer/single-consumer (SPSC) structure, not a general-purpose
concurrent queue. Head/tail/closed are 32-bit counters, matching the header
format shared with the Go implementation and, on the web, JavaScript's
`Int32Array`. Coordination goes through plain, 4-byte aligned loads and
stores by default, which is safe over real OS shared memory
(hardware-coherent across processes) and over `MemStorage` (which
serializes access with a mutex instead of relying on coherency, since two
threads in one process need an explicit happens-before edge that a plain
byte buffer alone doesn't give them) -- but not over a browser
`SharedArrayBuffer` shared between the main thread and a Worker, where a
plain access is a data race under the JavaScript memory model. That's
exactly why `SharedArrayBufferStorage` doesn't take this shortcut and uses
real `Atomics` instead (see [Web](#web)).

**Blocking calls poll.** There's no cross-process wakeup primitive
available through shared memory alone, so the blocking `Read`/`Write`
implementations (and `read_timeout`/`write_timeout`) block by polling the
shared counters with an exponential backoff (tunable via `Options`). Use
`try_write`/`try_read` if busy-polling isn't acceptable for your use case.

## Development

```sh
cargo build
cargo test
cargo clippy --all-targets -- -D warnings
cargo fmt
```

Or via the repo's [Mage](https://magefile.org) targets from the repo root:
`mage -l` lists them once added (see the main [README](../README.md)).

## License

MIT, see [LICENSE](../LICENSE).
