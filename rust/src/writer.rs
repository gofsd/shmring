use std::io;
use std::time::{Duration, Instant};

use crate::backend::Storage;
use crate::error::{Error, Result};
use crate::header::{
    init_header, read_u32_at, validate_capacity, wait_slice, write_u32_at, HEADER_SIZE, OFF_CLOSED,
    OFF_HEAD, OFF_TAIL,
};
use crate::options::Options;

/// The producer side of a ring buffer. A `Writer` must only be used from a
/// single thread at a time.
pub struct Writer<S: Storage> {
    storage: S,
    capacity: u64,
    mask: u64,
    options: Options,

    tail: u32,        // local authoritative copy; persisted to storage on every write
    cached_head: u32, // last head value observed from storage
    closed: bool,
}

impl<S: Storage> Writer<S> {
    /// Initializes a fresh ring buffer header on `storage` and returns the
    /// producer handle for it. `capacity` must be a positive power of two,
    /// and `storage` must be at least `header size + capacity` bytes.
    ///
    /// This is the low-level entry point used by [`create_shm`](crate::create_shm);
    /// use it directly to run the ring buffer over a custom [`Storage`]
    /// (for example [`MemStorage`](crate::backend::MemStorage) in tests).
    pub fn new(storage: S, capacity: u64, options: Options) -> Result<Self> {
        validate_capacity(capacity)?;
        if storage.size() < HEADER_SIZE + capacity {
            return Err(Error::StorageTooSmall);
        }
        init_header(&storage, capacity)?;
        Ok(Writer {
            storage,
            capacity,
            mask: capacity - 1,
            options,
            tail: 0,
            cached_head: 0,
            closed: false,
        })
    }

    /// Writes as much of `buf` as currently fits in the ring buffer without
    /// blocking, returning the number of bytes written. Returns `Ok(0)` if
    /// the buffer is full, and `Err(Error::Closed)` if the writer has been
    /// closed.
    pub fn try_write(&mut self, buf: &[u8]) -> Result<usize> {
        if self.closed {
            return Err(Error::Closed);
        }
        if buf.is_empty() {
            return Ok(0);
        }

        let mut free = self.capacity as i64 - self.tail.wrapping_sub(self.cached_head) as i64;
        if free < buf.len() as i64 {
            // The cached head may be stale (the reader has consumed more
            // than we've observed); refresh it before concluding there's
            // no room.
            self.cached_head = read_u32_at(&self.storage, OFF_HEAD)?;
            free = self.capacity as i64 - self.tail.wrapping_sub(self.cached_head) as i64;
        }
        if free <= 0 {
            return Ok(0);
        }

        let mut n = buf.len() as i64;
        if n > free {
            n = free;
        }
        let n = n as u64;

        let start = self.tail as u64 & self.mask;
        if start + n <= self.capacity {
            self.storage
                .write_at(&buf[..n as usize], HEADER_SIZE + start)?;
        } else {
            let first = self.capacity - start;
            self.storage
                .write_at(&buf[..first as usize], HEADER_SIZE + start)?;
            self.storage
                .write_at(&buf[first as usize..n as usize], HEADER_SIZE)?;
        }

        self.tail = self.tail.wrapping_add(n as u32);
        write_u32_at(&self.storage, OFF_TAIL, self.tail)?;
        Ok(n as usize)
    }

    /// Writes all of `buf`, blocking until space is available or `timeout`
    /// elapses since the call started. On success, the full buffer was
    /// written. On `Err(Error::Timeout)`, a prefix of `buf` may already
    /// have been written (and is already visible to the reader) before the
    /// deadline elapsed; the ring buffer has no way to report exactly how
    /// much, since that data cannot be un-written. Mirrors Go's
    /// `WriteContext`.
    pub fn write_timeout(&mut self, buf: &[u8], timeout: Duration) -> Result<usize> {
        self.write_until(buf, Some(Instant::now() + timeout))
    }

    fn write_until(&mut self, buf: &[u8], deadline: Option<Instant>) -> Result<usize> {
        let can_wait = self.storage.supports_wait();
        let mut written = 0usize;
        let mut wait = self.options.min_poll;
        while written < buf.len() {
            let n = self.try_write(&buf[written..])?;
            written += n;
            if n > 0 {
                wait = self.options.min_poll;
                continue;
            }
            if let Some(deadline) = deadline {
                if Instant::now() >= deadline {
                    return Err(Error::Timeout);
                }
            }
            if can_wait {
                // Block on a real wakeup tied to OFF_HEAD instead of
                // sleeping; see wait_slice for why the wait is bounded
                // when there's a deadline. A real wakeup interrupts this
                // immediately regardless of that bound.
                self.storage
                    .wait_u32_at(OFF_HEAD, self.cached_head, wait_slice(deadline));
                continue;
            }
            std::thread::sleep(wait);
            wait = (wait * 2).min(self.options.max_poll);
        }
        Ok(written)
    }

    /// Marks the ring buffer as closed. Any data already written remains
    /// available for the reader to drain; once drained, the reader's reads
    /// return end-of-stream. `close` does not release the underlying
    /// storage -- call [`close_storage`](Writer::close_storage) instead
    /// once no other process still needs the storage.
    pub fn close(&mut self) -> Result<()> {
        if self.closed {
            return Ok(());
        }
        self.closed = true;
        write_u32_at(&self.storage, OFF_CLOSED, 1)?;
        // A reader blocked in Storage::wait_u32_at watches OFF_TAIL, not
        // OFF_CLOSED (there's no data to wait for otherwise), so closing
        // without a final write needs an explicit nudge here. Re-storing
        // tail's current, unchanged value is a deliberate no-op purely
        // for store_u32_at's wake-on-store side effect.
        write_u32_at(&self.storage, OFF_TAIL, self.tail)
    }

    /// The last head value this `Writer` has observed from `storage`
    /// (refreshed by `try_write` whenever the cached one looked
    /// insufficient). crate-internal: used by
    /// [`wasm_api`](crate::wasm_api) to drive `Atomics.waitAsync` on
    /// `OFF_HEAD` without duplicating the ring buffer's own bookkeeping.
    #[cfg_attr(
        not(all(target_arch = "wasm32", target_os = "unknown")),
        allow(dead_code)
    )]
    pub(crate) fn cached_head(&self) -> u32 {
        self.cached_head
    }

    /// crate-internal accessor to the underlying storage, for the same
    /// reason as [`cached_head`](Writer::cached_head).
    #[cfg_attr(
        not(all(target_arch = "wasm32", target_os = "unknown")),
        allow(dead_code)
    )]
    pub(crate) fn storage(&self) -> &S {
        &self.storage
    }

    /// Marks the ring buffer closed (see [`close`](Writer::close)) and
    /// additionally closes the underlying storage: for OS shared memory
    /// this unmaps the segment and, on the creating side, removes it. Call
    /// this once no other process still needs the storage.
    pub fn close_storage(mut self) -> Result<()> {
        self.close()?;
        self.storage.close()
    }
}

impl<S: Storage> std::fmt::Debug for Writer<S> {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("Writer")
            .field("capacity", &self.capacity)
            .field("closed", &self.closed)
            .finish()
    }
}

impl<S: Storage> io::Write for Writer<S> {
    /// Blocks until at least one byte can be written, then writes as much
    /// of `buf` as currently fits. Combine with the [`Write`](io::Write)
    /// trait's default `write_all` to block until the whole buffer is
    /// written, unbounded -- the equivalent of Go's blocking `Write`.
    fn write(&mut self, buf: &[u8]) -> io::Result<usize> {
        if buf.is_empty() {
            return Ok(0);
        }
        let can_wait = self.storage.supports_wait();
        let mut wait = self.options.min_poll;
        loop {
            match self.try_write(buf) {
                Ok(0) => {
                    if can_wait {
                        self.storage.wait_u32_at(OFF_HEAD, self.cached_head, None);
                    } else {
                        std::thread::sleep(wait);
                        wait = (wait * 2).min(self.options.max_poll);
                    }
                }
                Ok(n) => return Ok(n),
                Err(Error::Closed) => {
                    return Err(io::Error::new(io::ErrorKind::BrokenPipe, Error::Closed))
                }
                Err(Error::Io(e)) => return Err(e),
                Err(other) => return Err(io::Error::other(other)),
            }
        }
    }

    fn flush(&mut self) -> io::Result<()> {
        Ok(())
    }
}
