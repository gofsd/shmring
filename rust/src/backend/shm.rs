use std::ffi::CString;
use std::io;
use std::ptr;
use std::time::Duration;

use crate::backend::Storage;
use crate::error::{Error, Result};

/// A [`Storage`] backed by a named POSIX shared-memory object
/// (`shm_open`/`mmap`), for cross-process use on Unix (Linux, macOS).
///
/// Real OS shared memory is coherent across processes at the hardware
/// level, which is what lets the ring buffer's SPSC algorithm use plain
/// aligned loads/stores for its head/tail counters instead of atomics --
/// see the crate-level docs' Concurrency model section.
///
/// Unlike Go's `backend.ShmStorage`, `close` doesn't need to be called for
/// correctness: the mapping (and, for the creating side, the named OS
/// object) is released by `Drop` regardless, since Rust's ownership rules
/// already prevent any further `read_at`/`write_at` calls once a value is
/// consumed by [`Storage::close`]. `close`'s `Result` is kept for trait
/// conformance and mirrors the Go API's shape, not because failure is
/// actionable here.
pub struct ShmStorage {
    ptr: *mut u8,
    size: usize,
    owns: bool,
    name: CString,
}

// SAFETY: `ptr` addresses OS shared memory, valid to dereference from any
// thread as long as accesses stay within `size` -- exactly the same
// contract `Storage::read_at`/`write_at` already impose. Moving a
// `ShmStorage` to another thread (e.g. handing a `Writer<ShmStorage>` to
// its own worker thread) is the whole point of this backend.
unsafe impl Send for ShmStorage {}

impl ShmStorage {
    fn normalize_name(name: &str) -> Result<CString> {
        // POSIX shm_open names are conventionally a single leading slash
        // followed by no further slashes (glibc enforces this; other
        // platforms are more lenient but agree on the leading slash).
        // Callers pass a bare name (see the crate docs' example), so add
        // the slash here rather than pushing that POSIX detail onto them.
        let full = if let Some(stripped) = name.strip_prefix('/') {
            format!("/{stripped}")
        } else {
            format!("/{name}")
        };
        CString::new(full).map_err(|e| Error::Io(io::Error::new(io::ErrorKind::InvalidInput, e)))
    }

    fn map(fd: i32, size: usize) -> io::Result<*mut u8> {
        unsafe {
            let ptr = libc::mmap(
                ptr::null_mut(),
                size,
                libc::PROT_READ | libc::PROT_WRITE,
                libc::MAP_SHARED,
                fd,
                0,
            );
            if ptr == libc::MAP_FAILED {
                return Err(io::Error::last_os_error());
            }
            Ok(ptr as *mut u8)
        }
    }

    /// Creates a new named shared-memory segment of `size` bytes and maps
    /// it. Fails if a segment with this name already exists -- open it
    /// with [`ShmStorage::open`] instead.
    pub(crate) fn create(name: &str, size: u64) -> Result<Self> {
        let cname = Self::normalize_name(name)?;
        let size = size as usize;
        unsafe {
            let fd = libc::shm_open(
                cname.as_ptr(),
                libc::O_CREAT | libc::O_EXCL | libc::O_RDWR,
                0o600,
            );
            if fd < 0 {
                return Err(Error::Io(io::Error::last_os_error()));
            }
            if libc::ftruncate(fd, size as libc::off_t) != 0 {
                let err = io::Error::last_os_error();
                libc::close(fd);
                libc::shm_unlink(cname.as_ptr());
                return Err(Error::Io(err));
            }
            let ptr = match Self::map(fd, size) {
                Ok(p) => p,
                Err(e) => {
                    libc::close(fd);
                    libc::shm_unlink(cname.as_ptr());
                    return Err(Error::Io(e));
                }
            };
            libc::close(fd); // the mapping keeps the memory alive; the fd itself isn't needed after mmap
            Ok(ShmStorage {
                ptr,
                size,
                owns: true,
                name: cname,
            })
        }
    }

    /// Opens an existing named shared-memory segment created by
    /// [`ShmStorage::create`], and maps it.
    pub(crate) fn open(name: &str, size: u64) -> Result<Self> {
        let cname = Self::normalize_name(name)?;
        let size = size as usize;
        unsafe {
            let fd = libc::shm_open(cname.as_ptr(), libc::O_RDWR, 0o600);
            if fd < 0 {
                return Err(Error::Io(io::Error::last_os_error()));
            }
            let ptr = match Self::map(fd, size) {
                Ok(p) => p,
                Err(e) => {
                    libc::close(fd);
                    return Err(Error::Io(e));
                }
            };
            libc::close(fd);
            Ok(ShmStorage {
                ptr,
                size,
                owns: false,
                name: cname,
            })
        }
    }
}

impl Storage for ShmStorage {
    fn read_at(&self, buf: &mut [u8], offset: u64) -> Result<()> {
        let offset = offset as usize;
        let in_range = offset
            .checked_add(buf.len())
            .is_some_and(|end| end <= self.size);
        if !in_range {
            return Err(Error::Io(io::Error::new(
                io::ErrorKind::UnexpectedEof,
                "read_at out of range",
            )));
        }
        // SAFETY: offset+buf.len() <= self.size was just checked, and self.ptr
        // maps exactly self.size bytes.
        unsafe {
            ptr::copy_nonoverlapping(self.ptr.add(offset), buf.as_mut_ptr(), buf.len());
        }
        Ok(())
    }

    fn write_at(&self, buf: &[u8], offset: u64) -> Result<()> {
        let offset = offset as usize;
        let in_range = offset
            .checked_add(buf.len())
            .is_some_and(|end| end <= self.size);
        if !in_range {
            return Err(Error::Io(io::Error::new(
                io::ErrorKind::WriteZero,
                "write_at out of range",
            )));
        }
        // SAFETY: offset+buf.len() <= self.size was just checked, and self.ptr
        // maps exactly self.size bytes.
        unsafe {
            ptr::copy_nonoverlapping(buf.as_ptr(), self.ptr.add(offset), buf.len());
        }
        Ok(())
    }

    fn size(&self) -> u64 {
        self.size as u64
    }

    fn close(self) -> Result<()> {
        // Cleanup happens in Drop; see the struct docs.
        Ok(())
    }

    /// On Linux, stores the word and wakes any thread parked in
    /// `wait_u32_at` on `offset` via a real futex wake -- see
    /// [`supports_wait`](Storage::supports_wait). macOS has no public
    /// futex equivalent, so it keeps the trait's plain-store default and
    /// [`wait_u32_at`](Storage::wait_u32_at) stays a no-op there (callers
    /// fall back to sleep-based polling, unchanged from before).
    #[cfg(target_os = "linux")]
    fn store_u32_at(&self, offset: u64, value: u32) -> Result<()> {
        self.write_at(&value.to_le_bytes(), offset)?;
        // SAFETY: write_at above already validated offset+4 <= self.size.
        unsafe { futex_wake(self.word_ptr(offset)) };
        Ok(())
    }

    #[cfg(target_os = "linux")]
    fn supports_wait(&self) -> bool {
        true
    }

    /// Blocks on a real futex `FUTEX_WAIT` for the word at `offset`. See
    /// the crate-level docs' Concurrency model section: this is strictly
    /// better than the plain-aligned-access assumption backing
    /// `load_u32_at`/`store_u32_at`'s defaults, not a replacement for it
    /// -- the futex wait itself still only fires after a real store to
    /// coherent shared memory, per `store_u32_at` above.
    #[cfg(target_os = "linux")]
    fn wait_u32_at(&self, offset: u64, old: u32, timeout: Option<Duration>) {
        if offset + 4 > self.size as u64 {
            return;
        }
        // SAFETY: offset+4 <= self.size was just checked.
        unsafe { futex_wait(self.word_ptr(offset), old, timeout) };
    }
}

#[cfg(target_os = "linux")]
impl ShmStorage {
    /// SAFETY: caller must ensure `offset + 4 <= self.size` (so the
    /// resulting pointer addresses 4 in-bounds, 4-byte-aligned bytes --
    /// true for every offset the ring buffer header defines).
    unsafe fn word_ptr(&self, offset: u64) -> *mut u32 {
        self.ptr.add(offset as usize) as *mut u32
    }
}

// FUTEX_WAIT/FUTEX_WAKE without FUTEX_PRIVATE_FLAG: the private variants
// assume the waiter and the waker are the same process (they skip a VM
// lookup by hashing on the process's mm plus the address), which does not
// hold here -- ShmStorage's whole point is sharing this word across
// independent processes. Using the private flag for a cross-process
// futex is a real, documented bug class (missed wakeups), not just a
// missed optimization. libc doesn't export these two constants for
// non-Android Linux targets, so they're defined locally; their values
// are stable, public kernel UAPI (linux/futex.h).
#[cfg(target_os = "linux")]
const FUTEX_WAIT: libc::c_int = 0;
#[cfg(target_os = "linux")]
const FUTEX_WAKE: libc::c_int = 1;

/// SAFETY: `word` must point to a valid, 4-byte-aligned `u32` that stays
/// alive and mapped for the duration of the call (true for any offset
/// into `ShmStorage`'s mapping, which outlives every `wait_u32_at` call
/// borrowing `&self`).
#[cfg(target_os = "linux")]
unsafe fn futex_wait(word: *mut u32, old: u32, timeout: Option<Duration>) {
    let ts = timeout.map(|d| libc::timespec {
        tv_sec: d.as_secs() as libc::time_t,
        tv_nsec: d.subsec_nanos() as libc::c_long,
    });
    let ts_ptr = ts
        .as_ref()
        .map_or(ptr::null(), |t| t as *const libc::timespec);
    // Matches how the Rust standard library itself issues this syscall
    // (library/std/src/sys/pal/unix/futex.rs): pass `old`/the op code by
    // value rather than pre-casting to a register-width integer -- LLVM's
    // C-variadic lowering applies the same default argument promotion a
    // C compiler would at this call site.
    libc::syscall(libc::SYS_futex, word, FUTEX_WAIT, old, ts_ptr);
}

/// SAFETY: see [`futex_wait`].
#[cfg(target_os = "linux")]
unsafe fn futex_wake(word: *mut u32) {
    const WAKE_ALL: libc::c_int = i32::MAX;
    libc::syscall(libc::SYS_futex, word, FUTEX_WAKE, WAKE_ALL);
}

impl Drop for ShmStorage {
    fn drop(&mut self) {
        unsafe {
            libc::munmap(self.ptr as *mut libc::c_void, self.size);
            if self.owns {
                libc::shm_unlink(self.name.as_ptr());
            }
        }
    }
}
