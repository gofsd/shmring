use std::sync::{Arc, Mutex};

use crate::backend::Storage;
use crate::error::{Error, Result};

/// A [`Storage`] backed by a plain in-process byte buffer. It never leaves
/// the process, so it's useful for unit tests, benchmarks, and for
/// platforms where an OS shared-memory backend isn't available: the same
/// ring buffer code path can run against it, with a producer and consumer
/// thread sharing one `MemStorage` (via [`Clone`]) instead of two processes
/// sharing shared memory.
///
/// Real OS shared memory (see [`ShmStorage`](crate::backend::ShmStorage)) is
/// coherent across processes at the hardware level, which is what lets the
/// ring buffer's SPSC algorithm use plain aligned loads/stores for its
/// head/tail counters instead of atomics. That guarantee doesn't hold for
/// two threads in the same process talking through an ordinary byte slice
/// without synchronization. `MemStorage` supplies that synchronization with
/// a mutex around every `read_at`/`write_at`, so it is safe to share
/// between threads even though it isn't lock-free.
#[derive(Clone)]
pub struct MemStorage(Arc<Mutex<Vec<u8>>>);

impl MemStorage {
    /// Allocates a `MemStorage` of the given size.
    ///
    /// # Panics
    ///
    /// Panics if `size` is zero.
    pub fn new(size: u64) -> Self {
        assert!(size > 0, "backend: MemStorage size must be positive");
        MemStorage(Arc::new(Mutex::new(vec![0u8; size as usize])))
    }
}

impl Storage for MemStorage {
    fn read_at(&self, buf: &mut [u8], offset: u64) -> Result<()> {
        let data = self.0.lock().unwrap();
        let offset = offset as usize;
        let end = offset
            .checked_add(buf.len())
            .filter(|&end| end <= data.len())
            .ok_or_else(|| Error::Io(std::io::Error::from(std::io::ErrorKind::UnexpectedEof)))?;
        buf.copy_from_slice(&data[offset..end]);
        Ok(())
    }

    fn write_at(&self, buf: &[u8], offset: u64) -> Result<()> {
        let mut data = self.0.lock().unwrap();
        let offset = offset as usize;
        let end = offset
            .checked_add(buf.len())
            .filter(|&end| end <= data.len())
            .ok_or_else(|| Error::Io(std::io::Error::from(std::io::ErrorKind::WriteZero)))?;
        data[offset..end].copy_from_slice(buf);
        Ok(())
    }

    fn size(&self) -> u64 {
        self.0.lock().unwrap().len() as u64
    }

    fn close(self) -> Result<()> {
        Ok(())
    }
}
