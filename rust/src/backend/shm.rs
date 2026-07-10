use std::ffi::CString;
use std::io;
use std::ptr;

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
