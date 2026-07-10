use std::io::{Read, Write};
use std::time::Duration;

use shmring::backend::MemStorage;
use shmring::{Error, Options, Reader, Writer};

/// Builds a Writer/Reader pair sharing one in-process MemStorage.
fn new_pair(capacity: u64) -> (Writer<MemStorage>, Reader<MemStorage>) {
    // Writer::new/Reader::new only require storage.size() >= header size +
    // capacity; oversizing here avoids coupling the test to the private
    // header size constant.
    let st = MemStorage::new(capacity + 4096);
    let w = Writer::new(st.clone(), capacity, Options::default()).expect("Writer::new");
    let r = Reader::new(st, capacity, Options::default()).expect("Reader::new");
    (w, r)
}

#[test]
fn try_write_try_read_round_trip() {
    let (mut w, mut r) = new_pair(16);

    let n = w.try_write(b"hello").unwrap();
    assert_eq!(n, 5);

    let mut buf = [0u8; 5];
    let n = r.try_read(&mut buf).unwrap();
    assert_eq!(n, 5);
    assert_eq!(&buf, b"hello");
}

#[test]
fn try_read_empty_returns_zero_ok() {
    let (_w, mut r) = new_pair(16);
    let mut buf = [0u8; 4];
    assert_eq!(r.try_read(&mut buf).unwrap(), 0);
}

#[test]
fn try_write_full_returns_zero_ok() {
    let (mut w, _r) = new_pair(8);
    w.try_write(b"12345678").unwrap();
    assert_eq!(w.try_write(b"x").unwrap(), 0);
}

#[test]
fn wraparound() {
    let (mut w, mut r) = new_pair(8);

    // Prime the buffer so head/tail sit in the middle of the ring, then
    // write a payload that straddles the wraparound point.
    w.try_write(b"1234").unwrap();
    let mut discard = [0u8; 4];
    r.try_read(&mut discard).unwrap();

    let payload = b"abcdefgh";
    assert_eq!(w.try_write(payload).unwrap(), 8);

    let mut got = [0u8; 8];
    assert_eq!(r.try_read(&mut got).unwrap(), 8);
    assert_eq!(&got, payload);
}

#[test]
fn blocking_write_read_across_threads() {
    let (mut w, mut r) = new_pair(4);

    const TOTAL: usize = 10_000;
    let data: Vec<u8> = (0..TOTAL).map(|i| (i % 256) as u8).collect();
    let expected = data.clone();

    let writer = std::thread::spawn(move || {
        w.write_all(&data).unwrap();
    });
    let reader = std::thread::spawn(move || {
        let mut got = vec![0u8; TOTAL];
        r.read_exact(&mut got).unwrap();
        got
    });

    writer.join().unwrap();
    let got = reader.join().unwrap();
    assert_eq!(got, expected);
}

#[test]
fn close_drains_then_eof() {
    let (mut w, mut r) = new_pair(16);

    w.try_write(b"bye").unwrap();
    w.close().unwrap();

    let mut buf = [0u8; 3];
    assert_eq!(r.try_read(&mut buf).unwrap(), 3);

    let err = r.try_read(&mut buf).unwrap_err();
    assert!(matches!(err, Error::Eof), "got {err:?}, want Eof");
}

#[test]
fn write_after_close_returns_closed() {
    let (mut w, _r) = new_pair(16);
    w.close().unwrap();
    let err = w.try_write(b"x").unwrap_err();
    assert!(matches!(err, Error::Closed), "got {err:?}, want Closed");
}

#[test]
fn blocking_read_returns_ok_zero_after_close() {
    let (mut w, mut r) = new_pair(16);

    let (tx, rx) = std::sync::mpsc::channel();
    std::thread::spawn(move || {
        let mut buf = [0u8; 1];
        let _ = tx.send(r.read(&mut buf));
    });

    std::thread::sleep(Duration::from_millis(10)); // let the reader start blocking
    w.close().unwrap();

    match rx.recv_timeout(Duration::from_secs(2)) {
        Ok(Ok(n)) => assert_eq!(n, 0, "io::Read::read at EOF should return Ok(0)"),
        Ok(Err(e)) => panic!("unexpected error: {e}"),
        Err(_) => panic!("Read did not return after Writer closed"),
    }
}

#[test]
fn write_timeout_elapses_when_full() {
    let (mut w, _r) = new_pair(4);
    w.try_write(b"1234").unwrap(); // fill the buffer so the next write must block

    let err = w
        .write_timeout(b"more", Duration::from_millis(20))
        .unwrap_err();
    assert!(matches!(err, Error::Timeout), "got {err:?}, want Timeout");
}

#[test]
fn new_writer_rejects_non_power_of_two_capacity() {
    let st = MemStorage::new(128);
    let err = Writer::new(st, 10, Options::default()).unwrap_err();
    assert!(
        matches!(err, Error::InvalidCapacity),
        "got {err:?}, want InvalidCapacity"
    );
}

#[test]
fn new_reader_rejects_capacity_mismatch() {
    let st = MemStorage::new(64 + 16);
    Writer::new(st.clone(), 16, Options::default()).unwrap();
    let err = Reader::new(st, 8, Options::default()).unwrap_err();
    assert!(
        matches!(err, Error::HeaderMismatch),
        "got {err:?}, want HeaderMismatch"
    );
}

#[test]
fn new_writer_rejects_undersized_storage() {
    let st = MemStorage::new(16);
    let err = Writer::new(st, 16, Options::default()).unwrap_err();
    assert!(
        matches!(err, Error::StorageTooSmall),
        "got {err:?}, want StorageTooSmall"
    );
}
