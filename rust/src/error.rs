use std::fmt;
use std::io;

/// Errors returned by shmring operations.
#[derive(Debug)]
pub enum Error {
    /// Returned by write operations performed after the [`Writer`](crate::Writer)
    /// has been closed, and by read operations once the writer has closed
    /// and all buffered data has been drained.
    Closed,
    /// Returned when a requested capacity is not a positive power of two.
    InvalidCapacity,
    /// Returned by [`Reader::new`](crate::Reader::new)/[`open_shm`](crate::open_shm)
    /// when the header found in storage doesn't match what this version of
    /// shmring expects (wrong magic, incompatible format version, or a
    /// capacity different from the one requested).
    HeaderMismatch,
    /// Returned when the provided storage is smaller than the header plus
    /// the requested capacity.
    StorageTooSmall,
    /// Returned by [`Reader`](crate::Reader) reads once the writer has
    /// closed and all buffered data has been drained. This is the
    /// reader-side end-of-stream signal, distinct from [`Error::Closed`]
    /// (which is returned by writes to an already-closed writer).
    Eof,
    /// A blocking call's deadline elapsed before it could complete.
    Timeout,
    /// An I/O error from the underlying storage.
    Io(io::Error),
}

impl fmt::Display for Error {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Error::Closed => write!(f, "shmring: ring buffer closed"),
            Error::InvalidCapacity => {
                write!(f, "shmring: capacity must be a positive power of two")
            }
            Error::HeaderMismatch => write!(
                f,
                "shmring: storage header does not match expected ring buffer format"
            ),
            Error::StorageTooSmall => {
                write!(
                    f,
                    "shmring: storage is too small for the requested capacity"
                )
            }
            Error::Eof => write!(f, "shmring: end of stream"),
            Error::Timeout => write!(f, "shmring: deadline exceeded"),
            Error::Io(e) => write!(f, "shmring: {e}"),
        }
    }
}

impl std::error::Error for Error {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        match self {
            Error::Io(e) => Some(e),
            _ => None,
        }
    }
}

impl From<io::Error> for Error {
    fn from(e: io::Error) -> Self {
        Error::Io(e)
    }
}

/// A [`Result`](std::result::Result) with [`Error`] as its error type.
pub type Result<T> = std::result::Result<T, Error>;
