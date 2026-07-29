//! Storage abstraction the ring buffer is built on top of, plus the
//! implementations shmring ships with.
//!
//! The ring buffer logic in the parent module never talks to OS shared
//! memory directly: it only depends on the [`Storage`] trait. This keeps
//! the platform-specific and IPC-specific concerns isolated to this module,
//! so support for additional platforms or transports can be added as new
//! `Storage` implementations without touching the ring buffer algorithm.

use std::time::Duration;

use crate::error::Result;

mod mem;
pub use mem::MemStorage;

#[cfg(unix)]
mod shm;
#[cfg(unix)]
pub use shm::ShmStorage;

#[cfg(all(target_arch = "wasm32", target_os = "unknown"))]
mod wasm;
#[cfg(all(target_arch = "wasm32", target_os = "unknown"))]
pub use wasm::SharedArrayBufferStorage;

/// A fixed-size, randomly addressable region of bytes shared between a
/// producer and a consumer. It is the minimal capability the ring buffer
/// needs from its underlying memory.
///
/// Implementations must be safe for concurrent use by one reader thread and
/// one writer thread at the same time (but not by multiple readers or
/// multiple writers), matching the single-producer/single-consumer
/// contract of the ring buffer itself.
pub trait Storage {
    /// Reads `buf.len()` bytes starting at `offset` into `buf`.
    fn read_at(&self, buf: &mut [u8], offset: u64) -> Result<()>;

    /// Writes all of `buf` starting at `offset`.
    fn write_at(&self, buf: &[u8], offset: u64) -> Result<()>;

    /// Returns the total size in bytes of the storage region. It is
    /// constant for the lifetime of the storage.
    fn size(&self) -> u64;

    /// Releases resources associated with the storage. For process-local
    /// backends this is typically a no-op; for shared-memory backends it
    /// unmaps the segment and, for the creating side, removes the
    /// underlying OS object.
    fn close(self) -> Result<()>;

    /// Loads a little-endian `u32` at `offset` (which must be 4-byte
    /// aligned). Used for the ring buffer's head/tail/closed counters.
    ///
    /// The default implementation is a plain [`read_at`](Storage::read_at),
    /// which is sufficient for backends over hardware-coherent memory (OS
    /// shared memory) or that already serialize access themselves (a
    /// mutex-backed in-process buffer). Backends without either guarantee
    /// -- like a JavaScript `SharedArrayBuffer` shared between browser
    /// threads, where plain reads/writes are a data race under the
    /// JavaScript memory model -- should override this with a real atomic
    /// load.
    fn load_u32_at(&self, offset: u64) -> Result<u32> {
        let mut buf = [0u8; 4];
        self.read_at(&mut buf, offset)?;
        Ok(u32::from_le_bytes(buf))
    }

    /// Stores a little-endian `u32` at `offset`. See
    /// [`load_u32_at`](Storage::load_u32_at) for when to override this.
    ///
    /// Implementations that also override [`wait_u32_at`](Storage::wait_u32_at)
    /// (i.e. that set [`supports_wait`](Storage::supports_wait) to `true`)
    /// must wake any thread parked in `wait_u32_at` on `offset` after
    /// storing, the same way a real futex's "wake" side must run after
    /// its "store" side for the wakeup to actually happen. See
    /// [`ShmStorage`](crate::backend::ShmStorage)'s Linux implementation
    /// (a real futex wake) and
    /// [`SharedArrayBufferStorage`](crate::backend::SharedArrayBufferStorage)'s
    /// (`Atomics.notify`) for how the two ends pair up.
    fn store_u32_at(&self, offset: u64, value: u32) -> Result<()> {
        self.write_at(&value.to_le_bytes(), offset)
    }

    /// Reports whether [`wait_u32_at`](Storage::wait_u32_at) provides a
    /// real blocking wait (`true`) or is a no-op stub that returns
    /// immediately (`false`, the default). [`Writer`](crate::Writer) and
    /// [`Reader`](crate::Reader) check this once per blocking call to
    /// decide whether to wait on a real wakeup primitive instead of their
    /// own sleep-based backoff.
    fn supports_wait(&self) -> bool {
        false
    }

    /// Blocks the calling thread until the `u32` at `offset` no longer
    /// equals `old`, or `timeout` elapses (`None` means wait
    /// indefinitely). Always returns eventually for some reason (a real
    /// change, a spurious wakeup, or a timeout, exactly like a raw
    /// futex `FUTEX_WAIT`) -- the caller re-reads the word and decides
    /// what to do next.
    ///
    /// Only meaningful when [`supports_wait`](Storage::supports_wait)
    /// returns `true`; the default implementation here is a no-op that
    /// returns immediately, which would busy-loop if a caller relied on
    /// it without checking `supports_wait` first.
    fn wait_u32_at(&self, _offset: u64, _old: u32, _timeout: Option<Duration>) {}
}
