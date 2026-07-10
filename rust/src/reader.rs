use std::io;
use std::time::{Duration, Instant};

use crate::backend::Storage;
use crate::error::{Error, Result};
use crate::header::{
    read_u32_at, validate_capacity, verify_header, write_u32_at, HEADER_SIZE, OFF_CLOSED, OFF_HEAD,
    OFF_TAIL,
};
use crate::options::Options;

/// The consumer side of a ring buffer. A `Reader` must only be used from a
/// single thread at a time.
pub struct Reader<S: Storage> {
    storage: S,
    capacity: u64,
    mask: u64,
    options: Options,

    head: u32,        // local authoritative copy; persisted to storage on every read
    cached_tail: u32, // last tail value observed from storage
}

impl<S: Storage> Reader<S> {
    /// Attaches to a ring buffer previously initialized by
    /// [`Writer::new`](crate::Writer::new) on the same storage. `capacity`
    /// must match the value the writer used.
    ///
    /// This is the low-level entry point used by [`open_shm`](crate::open_shm);
    /// use it directly to run the ring buffer over a custom [`Storage`].
    pub fn new(storage: S, capacity: u64, options: Options) -> Result<Self> {
        validate_capacity(capacity)?;
        if storage.size() < HEADER_SIZE + capacity {
            return Err(Error::StorageTooSmall);
        }
        verify_header(&storage, capacity)?;
        Ok(Reader {
            storage,
            capacity,
            mask: capacity - 1,
            options,
            head: 0,
            cached_tail: 0,
        })
    }

    /// Reads as many bytes as are currently available into `buf` without
    /// blocking, returning the number of bytes read. Returns `Ok(0)` if the
    /// buffer is empty and still open. Once the buffer is empty and the
    /// writer has been closed, returns `Err(Error::Eof)`.
    pub fn try_read(&mut self, buf: &mut [u8]) -> Result<usize> {
        if buf.is_empty() {
            return Ok(0);
        }

        let mut avail = self.cached_tail.wrapping_sub(self.head) as i64;
        if avail < buf.len() as i64 {
            // The cached tail may be stale (the writer has produced more
            // than we've observed); refresh it before concluding there's
            // no data.
            self.cached_tail = read_u32_at(&self.storage, OFF_TAIL)?;
            avail = self.cached_tail.wrapping_sub(self.head) as i64;
        }
        if avail <= 0 {
            let closed = read_u32_at(&self.storage, OFF_CLOSED)?;
            if closed != 0 {
                return Err(Error::Eof);
            }
            return Ok(0);
        }

        let mut n = buf.len() as i64;
        if n > avail {
            n = avail;
        }
        let n = n as u64;

        let start = self.head as u64 & self.mask;
        if start + n <= self.capacity {
            self.storage
                .read_at(&mut buf[..n as usize], HEADER_SIZE + start)?;
        } else {
            let first = self.capacity - start;
            self.storage
                .read_at(&mut buf[..first as usize], HEADER_SIZE + start)?;
            self.storage
                .read_at(&mut buf[first as usize..n as usize], HEADER_SIZE)?;
        }

        self.head = self.head.wrapping_add(n as u32);
        write_u32_at(&self.storage, OFF_HEAD, self.head)?;
        Ok(n as usize)
    }

    /// Blocks until at least one byte is available and reads into `buf`, or
    /// returns `Err(Error::Timeout)` if `timeout` elapses first. Returns
    /// `Err(Error::Eof)` once the writer has closed and all buffered data
    /// has been drained. Mirrors Go's `ReadContext`.
    pub fn read_timeout(&mut self, buf: &mut [u8], timeout: Duration) -> Result<usize> {
        self.read_until(buf, Some(Instant::now() + timeout))
    }

    fn read_until(&mut self, buf: &mut [u8], deadline: Option<Instant>) -> Result<usize> {
        let mut wait = self.options.min_poll;
        loop {
            let n = self.try_read(buf)?;
            if n > 0 {
                return Ok(n);
            }
            if let Some(deadline) = deadline {
                if Instant::now() >= deadline {
                    return Err(Error::Timeout);
                }
            }
            std::thread::sleep(wait);
            wait = (wait * 2).min(self.options.max_poll);
        }
    }

    /// Releases the reader's handle on the underlying storage. For OS
    /// shared memory this unmaps the segment (it does not remove it; only
    /// the creating writer's `close_storage` does that).
    pub fn close(self) -> Result<()> {
        self.storage.close()
    }
}

impl<S: Storage> std::fmt::Debug for Reader<S> {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("Reader")
            .field("capacity", &self.capacity)
            .finish()
    }
}

impl<S: Storage> io::Read for Reader<S> {
    /// Blocks until at least one byte is available and reads into `buf`,
    /// implementing [`io::Read`]. Returns `Ok(0)` once the writer has
    /// closed and all buffered data has been drained (this trait's
    /// end-of-stream convention).
    fn read(&mut self, buf: &mut [u8]) -> io::Result<usize> {
        if buf.is_empty() {
            return Ok(0);
        }
        let mut wait = self.options.min_poll;
        loop {
            match self.try_read(buf) {
                Ok(0) => {
                    std::thread::sleep(wait);
                    wait = (wait * 2).min(self.options.max_poll);
                }
                Ok(n) => return Ok(n),
                Err(Error::Eof) => return Ok(0),
                Err(Error::Io(e)) => return Err(e),
                Err(other) => return Err(io::Error::other(other)),
            }
        }
    }
}
