//! Storage abstraction the ring buffer is built on top of, plus the
//! implementations shmring ships with.
//!
//! The ring buffer logic in the parent module never talks to OS shared
//! memory directly: it only depends on the [`Storage`] trait. This keeps
//! the platform-specific and IPC-specific concerns isolated to this module,
//! so support for additional platforms or transports can be added as new
//! `Storage` implementations without touching the ring buffer algorithm.

use crate::error::Result;

mod mem;
pub use mem::MemStorage;

#[cfg(unix)]
mod shm;
#[cfg(unix)]
pub use shm::ShmStorage;

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
}
