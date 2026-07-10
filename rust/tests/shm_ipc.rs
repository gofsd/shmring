#![cfg(unix)]

use std::io::{Read, Write};

use shmring::{create_shm, open_shm, Options};

/// Smoke test for the real OS-shared-memory backend: two independent
/// mappings of the same named segment, exercised through the public
/// create_shm/open_shm entry points end to end (not just Writer/Reader
/// over MemStorage, which the rest of the suite already covers).
#[test]
fn create_and_open_shm_round_trip() {
    let name = format!("shmring-rust-test-{}", std::process::id());

    let mut w = create_shm(&name, 4096, Options::default()).expect("create_shm");
    let mut r = open_shm(&name, 4096, Options::default()).expect("open_shm");

    w.write_all(b"hello from rust\n").expect("write_all");
    w.close().expect("Writer::close");

    let mut got = Vec::new();
    r.read_to_end(&mut got).expect("read_to_end");
    assert_eq!(got, b"hello from rust\n");

    r.close().expect("Reader::close");
    w.close_storage().expect("Writer::close_storage");
}
