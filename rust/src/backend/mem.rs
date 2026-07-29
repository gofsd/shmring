use std::sync::{Arc, Condvar, Mutex};
use std::time::Duration;

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
///
/// It also implements [`Storage::wait_u32_at`] via a [`Condvar`] paired
/// with that same mutex, so a blocking `Write`/`Read` over `MemStorage`
/// parks instead of sleep-polling -- the in-process equivalent of
/// `ShmStorage`'s Linux futex or the web build's `Atomics.wait`.
#[derive(Clone)]
pub struct MemStorage(Arc<Inner>);

struct Inner {
    data: Mutex<Vec<u8>>,
    changed: Condvar,
}

impl MemStorage {
    /// Allocates a `MemStorage` of the given size.
    ///
    /// # Panics
    ///
    /// Panics if `size` is zero.
    pub fn new(size: u64) -> Self {
        assert!(size > 0, "backend: MemStorage size must be positive");
        MemStorage(Arc::new(Inner {
            data: Mutex::new(vec![0u8; size as usize]),
            changed: Condvar::new(),
        }))
    }
}

impl Storage for MemStorage {
    fn read_at(&self, buf: &mut [u8], offset: u64) -> Result<()> {
        let data = self.0.data.lock().unwrap();
        let offset = offset as usize;
        let end = offset
            .checked_add(buf.len())
            .filter(|&end| end <= data.len())
            .ok_or_else(|| Error::Io(std::io::Error::from(std::io::ErrorKind::UnexpectedEof)))?;
        buf.copy_from_slice(&data[offset..end]);
        Ok(())
    }

    fn write_at(&self, buf: &[u8], offset: u64) -> Result<()> {
        let mut data = self.0.data.lock().unwrap();
        let offset = offset as usize;
        let end = offset
            .checked_add(buf.len())
            .filter(|&end| end <= data.len())
            .ok_or_else(|| Error::Io(std::io::Error::from(std::io::ErrorKind::WriteZero)))?;
        data[offset..end].copy_from_slice(buf);
        Ok(())
    }

    fn size(&self) -> u64 {
        self.0.data.lock().unwrap().len() as u64
    }

    fn close(self) -> Result<()> {
        Ok(())
    }

    /// Stores the word, then wakes every thread parked in
    /// [`wait_u32_at`](Storage::wait_u32_at) -- not just ones waiting on
    /// this particular offset, since `Condvar` has no concept of which
    /// value changed; a woken waiter just re-checks its own offset and,
    /// if that one hasn't changed, goes back to waiting.
    fn store_u32_at(&self, offset: u64, value: u32) -> Result<()> {
        {
            let mut data = self.0.data.lock().unwrap();
            let offset = offset as usize;
            let end = offset
                .checked_add(4)
                .filter(|&end| end <= data.len())
                .ok_or_else(|| Error::Io(std::io::Error::from(std::io::ErrorKind::WriteZero)))?;
            data[offset..end].copy_from_slice(&value.to_le_bytes());
        }
        self.0.changed.notify_all();
        Ok(())
    }

    fn supports_wait(&self) -> bool {
        true
    }

    /// Blocks for at most one `Condvar` wakeup (real or spurious) or the
    /// timeout, then returns -- deliberately not a loop that re-checks
    /// the word until it actually differs from `old`. That distinction
    /// matters: `Writer::close`/`Reader`'s wake-on-close path (see
    /// writer.rs) re-stores a header word's *unchanged* value purely to
    /// trigger a wakeup, exactly the way a real futex FUTEX_WAKE doesn't
    /// care whether the underlying value actually moved either. Looping
    /// here until the value changed would silently swallow that wakeup
    /// and block forever. The caller (Writer/Reader) already loops on
    /// its own real condition (try_write/try_read) after every wakeup,
    /// so a spurious return here just costs one cheap extra check, not
    /// correctness.
    fn wait_u32_at(&self, offset: u64, old: u32, timeout: Option<Duration>) {
        let offset = offset as usize;
        let Ok(data) = self.0.data.lock() else {
            return;
        };
        // Matches a real futex's compare-on-entry: if the word has
        // already changed since the caller observed `old`, return
        // immediately without waiting at all.
        let already_changed = match data.get(offset..offset + 4) {
            Some(b) => u32::from_le_bytes(b.try_into().unwrap()) != old,
            None => true,
        };
        if already_changed {
            return;
        }
        match timeout {
            None => drop(self.0.changed.wait(data)),
            Some(d) => drop(self.0.changed.wait_timeout(data, d)),
        }
    }
}
