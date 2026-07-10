use crate::backend::ShmStorage;
use crate::error::Result;
use crate::header::{validate_capacity, HEADER_SIZE};
use crate::options::Options;
use crate::{Reader, Writer};

/// Creates a new OS shared-memory segment named `name`, sized for the
/// given data capacity (a positive power of two), and returns the
/// [`Writer`] for it. The segment is removed from the OS once the
/// writer's storage is closed via [`Writer::close_storage`]. The consumer
/// opens the same segment with [`open_shm`].
pub fn create_shm(name: &str, capacity: u64, options: Options) -> Result<Writer<ShmStorage>> {
    validate_capacity(capacity)?;
    let st = ShmStorage::create(name, HEADER_SIZE + capacity)?;
    Writer::new(st, capacity, options)
}

/// Opens a shared-memory segment created by [`create_shm`] with the same
/// name and capacity, and returns the [`Reader`] for it.
pub fn open_shm(name: &str, capacity: u64, options: Options) -> Result<Reader<ShmStorage>> {
    validate_capacity(capacity)?;
    let st = ShmStorage::open(name, HEADER_SIZE + capacity)?;
    Reader::new(st, capacity, options)
}
