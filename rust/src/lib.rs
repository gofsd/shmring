//! `shmring` implements a fixed-capacity, single-producer/single-consumer
//! byte ring buffer over shared memory, using the same wire format as the
//! Go and JavaScript implementations at <https://github.com/gofsd/shmring>
//! -- a Rust process using this crate can exchange bytes with a Go process
//! using `shmring.CreateShm`/`OpenShm` over the same named segment.
//!
//! A ring buffer is created by one side (the producer) with [`create_shm`]
//! and opened by the other side (the consumer) with [`open_shm`], naming
//! the same OS shared-memory segment. Bytes written by the [`Writer`]
//! become visible to the [`Reader`] in FIFO order, wrapping around the
//! underlying storage as needed.
//!
//! The storage the ring buffer runs on is pluggable (see the [`backend`]
//! module): `create_shm`/`open_shm` use OS shared memory for cross-process
//! use, while [`Writer::new`]/[`Reader::new`] accept any
//! [`backend::Storage`], which is what makes it possible to run the exact
//! same algorithm over a plain in-process byte buffer
//! ([`backend::MemStorage`], handy for tests) or over a future backend for
//! a platform or transport this crate doesn't cover yet.
//!
//! # Platform support
//!
//! OS shared memory ([`create_shm`]/[`open_shm`]) is implemented for Unix
//! (Linux, macOS) via POSIX `shm_open`. Windows isn't implemented yet --
//! [`Writer::new`]/[`Reader::new`] over a custom [`backend::Storage`] work
//! on every platform regardless; only the OS-shared-memory convenience
//! functions are Unix-only for now.
//!
//! # Concurrency model
//!
//! A ring buffer has exactly one [`Writer`] and one [`Reader`]. Each must
//! only be used from a single thread at a time (the writer's thread may
//! differ from the reader's thread, and in the cross-process case they're
//! typically different processes entirely). Calling a write method
//! concurrently from two threads, or a read method concurrently from two
//! threads, is not supported.
//!
//! The head/tail coordination between the writer and the reader relies on
//! plain, naturally aligned 32-bit loads and stores to the shared region
//! by default (see [`backend::Storage::load_u32_at`]/`store_u32_at`), which
//! is the same assumption classic SPSC ring buffers over shared memory
//! (e.g. Linux `kfifo`) make, and holds for OS shared memory
//! (hardware-coherent across processes) and for [`backend::MemStorage`]
//! (which serializes access with a mutex instead). Backends without either
//! guarantee override those two methods with a real atomic load/store --
//! see [`backend::SharedArrayBufferStorage`] (compiled for
//! `wasm32-unknown-unknown` only), used from a browser where two
//! independent wasm module instances -- typically the main thread and a
//! Web Worker -- coordinate through a JavaScript `SharedArrayBuffer` and
//! its `Atomics`. Do not repurpose the writer/reader split for anything
//! other than the SPSC pattern it was designed for.

mod error;
mod header;
mod options;
mod reader;
mod writer;

pub mod backend;

pub use error::{Error, Result};
pub use options::Options;
pub use reader::Reader;
pub use writer::Writer;

#[cfg(unix)]
mod shm;
#[cfg(unix)]
pub use shm::{create_shm, open_shm};

#[cfg(all(target_arch = "wasm32", target_os = "unknown"))]
mod wasm_api;
