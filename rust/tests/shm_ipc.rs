#![cfg(unix)]

use std::io::{Read, Write};
use std::time::{Duration, Instant};

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

/// Exercises ShmStorage's blocking Read/Write path specifically -- the
/// round trip above only uses write_all/read_to_end on data that's
/// already available, never a `try_*` returning 0 and actually blocking.
/// On Linux this blocks on a real futex FUTEX_WAIT tied to OFF_TAIL (see
/// backend/shm.rs's Storage::wait_u32_at override); elsewhere it falls
/// back to the pre-existing sleep-based poll. Either way this must not
/// hang, and on Linux the timing assertion below would fail if the futex
/// wiring silently regressed to polling.
#[test]
fn blocking_read_wakes_on_write() {
    let name = format!("shmring-rust-wait-test-{}", std::process::id());
    let w = create_shm(&name, 4096, Options::default()).expect("create_shm");
    let mut r = open_shm(&name, 4096, Options::default()).expect("open_shm");

    const MSG: &[u8] = b"hello from a real futex wakeup\n";
    let barrier = std::sync::Arc::new(std::sync::Barrier::new(2));
    let writer_barrier = barrier.clone();

    // w is now solely owned by this thread for the rest of the test (a
    // Writer must only be used from one thread at a time).
    let writer = std::thread::spawn(move || {
        let mut w = w;
        writer_barrier.wait();
        std::thread::sleep(Duration::from_millis(20));
        w.write_all(MSG).expect("write_all");
        w.close_storage().expect("close_storage");
    });

    barrier.wait();
    let before = Instant::now();
    let mut buf = vec![0u8; MSG.len()];
    r.read_exact(&mut buf).expect("read_exact");
    let elapsed = before.elapsed();
    assert_eq!(&buf, MSG);
    // The write happens ~20ms after the read starts blocking. A real
    // futex wakeup returns within microseconds of that write; 15ms of
    // slack is generous headroom for scheduler noise while still
    // catching a regression to multi-tick polling.
    assert!(
        elapsed < Duration::from_millis(35),
        "read took {elapsed:?} after the write started (~20ms delay); \
         looks like it fell back to polling instead of a real futex wakeup"
    );

    // Drain to EOF (blocks on the same tail futex until the writer
    // thread's close_storage -- via Writer::close's OFF_TAIL nudge --
    // wakes it).
    let mut tail = [0u8; 1];
    let n = r.read(&mut tail).expect("final read");
    assert_eq!(
        n, 0,
        "want Ok(0) (EOF) once the writer has closed and drained"
    );

    r.close().expect("Reader::close");
    writer.join().expect("writer thread panicked");
}
