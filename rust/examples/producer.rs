//! Creates a shared-memory ring buffer and streams numbered messages into
//! it. Run the consumer example against the same `--name` in another
//! terminal (or process) to see them come out the other side.
//!
//! Mirrors `examples/producer` in the Go implementation.

use std::io::Write;
use std::time::Duration;

use shmring::{create_shm, Options};

struct Args {
    name: String,
    capacity: u64,
    count: u32,
    interval: Duration,
}

impl Args {
    fn parse() -> Self {
        let mut a = Args {
            name: "shmring-example".to_string(),
            capacity: 4096,
            count: 20,
            interval: Duration::from_millis(200),
        };
        for arg in std::env::args().skip(1) {
            let Some((key, value)) = arg.strip_prefix("--").and_then(|s| s.split_once('=')) else {
                continue;
            };
            match key {
                "name" => a.name = value.to_string(),
                "capacity" => a.capacity = value.parse().expect("--capacity must be a u64"),
                "count" => a.count = value.parse().expect("--count must be a u32"),
                "interval-ms" => {
                    a.interval =
                        Duration::from_millis(value.parse().expect("--interval-ms must be a u64"))
                }
                _ => {}
            }
        }
        a
    }
}

fn main() {
    let args = Args::parse();

    let mut w = create_shm(&args.name, args.capacity, Options::default())
        .unwrap_or_else(|e| panic!("create_shm: {e}"));

    println!(
        "created shared memory {:?} (capacity={}), producing {} messages",
        args.name, args.capacity, args.count
    );

    for i in 0..args.count {
        let msg = format!("message {i}\n");
        w.write_all(msg.as_bytes())
            .unwrap_or_else(|e| panic!("write_all: {e}"));
        println!("sent: {:?}", msg.trim_end());
        std::thread::sleep(args.interval);
    }

    w.close().unwrap_or_else(|e| panic!("close: {e}"));
    println!("done, waiting a moment for the consumer to drain before removing the segment");
    std::thread::sleep(Duration::from_secs(2));
    w.close_storage()
        .unwrap_or_else(|e| panic!("close_storage: {e}"));
}
