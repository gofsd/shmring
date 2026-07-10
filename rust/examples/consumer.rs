//! Opens a shared-memory ring buffer created by the producer example and
//! prints every message it reads until the producer closes the ring and it
//! drains.
//!
//! Mirrors `examples/consumer` in the Go implementation.

use std::io::{BufRead, BufReader};
use std::time::{Duration, Instant};

use shmring::backend::ShmStorage;
use shmring::{open_shm, Options, Reader, Result};

struct Args {
    name: String,
    capacity: u64,
}

impl Args {
    fn parse() -> Self {
        let mut a = Args {
            name: "shmring-example".to_string(),
            capacity: 4096,
        };
        for arg in std::env::args().skip(1) {
            let Some((key, value)) = arg.strip_prefix("--").and_then(|s| s.split_once('=')) else {
                continue;
            };
            match key {
                "name" => a.name = value.to_string(),
                "capacity" => a.capacity = value.parse().expect("--capacity must be a u64"),
                _ => {}
            }
        }
        a
    }
}

/// Retries `open_shm` until the producer has created the segment or
/// `timeout` elapses.
fn open_with_retry(name: &str, capacity: u64, timeout: Duration) -> Result<Reader<ShmStorage>> {
    let deadline = Instant::now() + timeout;
    loop {
        match open_shm(name, capacity, Options::default()) {
            Ok(r) => return Ok(r),
            Err(e) if Instant::now() >= deadline => return Err(e),
            Err(_) => std::thread::sleep(Duration::from_millis(100)),
        }
    }
}

fn main() {
    let args = Args::parse();

    let r = open_with_retry(&args.name, args.capacity, Duration::from_secs(5))
        .unwrap_or_else(|e| panic!("open_shm: {e}"));

    println!(
        "opened shared memory {:?}, reading until the producer closes it",
        args.name
    );

    let mut reader = BufReader::new(r);
    let mut line = String::new();
    loop {
        line.clear();
        match reader.read_line(&mut line) {
            Ok(0) => break,
            Ok(_) => println!("received: {:?}", line.trim_end()),
            Err(e) => panic!("read_line: {e}"),
        }
    }
    println!("producer closed and drained, exiting");
}
