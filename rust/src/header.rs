use crate::backend::Storage;
use crate::error::{Error, Result};

// Storage layout (all fields little-endian):
//
//   [ 0: 4]  magic     u32     format identifier
//   [ 4: 8]  version   u32     format version
//   [ 8:16]  capacity  u64     size of the data region in bytes (write-once)
//   [16:20]  head      u32     monotonic read counter, owned by the Reader
//   [20:24]  reserved
//   [24:28]  tail      u32     monotonic write counter, owned by the Writer
//   [28:32]  reserved
//   [32:36]  closed    u32     0 = open, 1 = writer closed
//   [36:64]  reserved  28 bytes reserved for future header fields
//   [64: )            data region, `capacity` bytes
//
// head/tail/closed are 32-bit to match the wire format shared with the Go
// and JS implementations (chosen there so the header also fits
// JavaScript's Int32Array for the browser SharedArrayBuffer backend). A
// 32-bit monotonic counter is safe here because ring buffer correctness
// only depends on tail-head, which never exceeds capacity -- see
// MAX_CAPACITY.
pub(crate) const MAGIC: u32 = 0x53484d52; // "SHMR"
pub(crate) const FORMAT_VERSION: u32 = 1;

pub(crate) const HEADER_SIZE: u64 = 64;

pub(crate) const OFF_MAGIC: u64 = 0;
pub(crate) const OFF_VERSION: u64 = 4;
pub(crate) const OFF_CAPACITY: u64 = 8;
pub(crate) const OFF_HEAD: u64 = 16;
pub(crate) const OFF_TAIL: u64 = 24;
pub(crate) const OFF_CLOSED: u64 = 32;

/// Bounds ring buffer capacity so that the true distance between the head
/// and tail counters never approaches the point where u32 wraparound
/// arithmetic (tail-head) could become ambiguous.
pub(crate) const MAX_CAPACITY: u64 = 1 << 31;

/// Reports whether `capacity` is usable as a ring buffer size: a positive
/// power of two, no larger than [`MAX_CAPACITY`].
pub(crate) fn validate_capacity(capacity: u64) -> Result<()> {
    if capacity == 0 || capacity > MAX_CAPACITY || capacity & (capacity - 1) != 0 {
        return Err(Error::InvalidCapacity);
    }
    Ok(())
}

/// Writes a fresh header into `st`, describing a ring buffer with the
/// given data capacity. It is called once, by the side that creates the
/// ring buffer.
pub(crate) fn init_header<S: Storage>(st: &S, capacity: u64) -> Result<()> {
    let mut buf = [0u8; HEADER_SIZE as usize];
    buf[OFF_MAGIC as usize..OFF_MAGIC as usize + 4].copy_from_slice(&MAGIC.to_le_bytes());
    buf[OFF_VERSION as usize..OFF_VERSION as usize + 4]
        .copy_from_slice(&FORMAT_VERSION.to_le_bytes());
    buf[OFF_CAPACITY as usize..OFF_CAPACITY as usize + 8].copy_from_slice(&capacity.to_le_bytes());
    buf[OFF_HEAD as usize..OFF_HEAD as usize + 4].copy_from_slice(&0u32.to_le_bytes());
    buf[OFF_TAIL as usize..OFF_TAIL as usize + 4].copy_from_slice(&0u32.to_le_bytes());
    buf[OFF_CLOSED as usize..OFF_CLOSED as usize + 4].copy_from_slice(&0u32.to_le_bytes());
    st.write_at(&buf, 0)
}

/// Reads the header from `st` and checks that it describes a ring buffer
/// compatible with the given expected capacity.
pub(crate) fn verify_header<S: Storage>(st: &S, capacity: u64) -> Result<()> {
    let mut buf = [0u8; HEADER_SIZE as usize];
    st.read_at(&mut buf, 0)?;
    if u32::from_le_bytes(
        buf[OFF_MAGIC as usize..OFF_MAGIC as usize + 4]
            .try_into()
            .unwrap(),
    ) != MAGIC
    {
        return Err(Error::HeaderMismatch);
    }
    if u32::from_le_bytes(
        buf[OFF_VERSION as usize..OFF_VERSION as usize + 4]
            .try_into()
            .unwrap(),
    ) != FORMAT_VERSION
    {
        return Err(Error::HeaderMismatch);
    }
    if u64::from_le_bytes(
        buf[OFF_CAPACITY as usize..OFF_CAPACITY as usize + 8]
            .try_into()
            .unwrap(),
    ) != capacity
    {
        return Err(Error::HeaderMismatch);
    }
    Ok(())
}

/// Loads a header word via [`Storage::load_u32_at`] -- a plain aligned read
/// for backends over hardware-coherent memory (see
/// [`ShmStorage`](crate::backend::ShmStorage) and
/// [`MemStorage`](crate::backend::MemStorage)'s docs), or a real atomic
/// load for backends that need one (see
/// [`SharedArrayBufferStorage`](crate::backend::SharedArrayBufferStorage)).
pub(crate) fn read_u32_at<S: Storage>(st: &S, off: u64) -> Result<u32> {
    st.load_u32_at(off)
}

pub(crate) fn write_u32_at<S: Storage>(st: &S, off: u64, v: u32) -> Result<()> {
    st.store_u32_at(off, v)
}
