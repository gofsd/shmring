use std::io;

use js_sys::{Atomics, Int32Array, SharedArrayBuffer, Uint8Array};

use crate::backend::Storage;
use crate::error::{Error, Result};

/// A [`Storage`] backed by a JavaScript `SharedArrayBuffer`. It's what lets
/// a ring buffer span two different threads in a browser -- typically the
/// main thread and a Web Worker.
///
/// A `SharedArrayBuffer` is created on one side ([`new`](Self::new)) and
/// transferred to the other (e.g. via `postMessage`, which shares rather
/// than copies a `SharedArrayBuffer`); each side then wraps its own handle
/// to the same underlying buffer with [`wrap`](Self::wrap) and builds a
/// `Writer`/`Reader` on top of it as usual. Both sides run their own,
/// independent wasm module instance -- this crate compiled to
/// `wasm32-unknown-unknown` doesn't need or use Rust-level threading for
/// this; coordination is entirely through the JavaScript engine's own
/// cross-thread-safe `Atomics`, which is why plain stable
/// `wasm32-unknown-unknown` is sufficient here (no nightly, no
/// `-Z build-std`).
///
/// Requires the page to be cross-origin isolated (the
/// `Cross-Origin-Opener-Policy`/`Cross-Origin-Embedder-Policy` response
/// headers), which is what makes `SharedArrayBuffer` available in the
/// first place.
pub struct SharedArrayBufferStorage {
    buf: SharedArrayBuffer,
    bytes: Uint8Array, // Uint8Array view over buf, for bulk read_at/write_at
    words: Int32Array, // Int32Array view over buf, for Atomics
    size: u64,
}

impl SharedArrayBufferStorage {
    /// Allocates a fresh `SharedArrayBuffer` of `size` bytes and wraps it.
    /// `size` must be a positive multiple of 4 (so every offset the ring
    /// buffer header uses is a whole `Int32Array` index for `Atomics`).
    pub fn new(size: u64) -> Result<Self> {
        let size_u32 = valid_size(size)?;
        Self::from_buffer(SharedArrayBuffer::new(size_u32), size)
    }

    /// Wraps an existing `SharedArrayBuffer`, for example one received from
    /// another thread via `postMessage`.
    pub fn wrap(buf: SharedArrayBuffer) -> Result<Self> {
        let size = buf.byte_length() as u64;
        valid_size(size)?;
        Self::from_buffer(buf, size)
    }

    fn from_buffer(buf: SharedArrayBuffer, size: u64) -> Result<Self> {
        let bytes = Uint8Array::new(&buf);
        let words = Int32Array::new(&buf);
        Ok(SharedArrayBufferStorage {
            buf,
            bytes,
            words,
            size,
        })
    }

    /// Returns the underlying `SharedArrayBuffer`, to transfer to a Worker
    /// (e.g. `worker.postMessage(storage.buffer())`).
    pub fn buffer(&self) -> SharedArrayBuffer {
        self.buf.clone()
    }

    fn check_range(&self, offset: u64, len: u64) -> Result<()> {
        let in_range = offset.checked_add(len).is_some_and(|end| end <= self.size);
        if !in_range {
            return Err(Error::Io(io::Error::new(
                io::ErrorKind::UnexpectedEof,
                "backend: offset out of range",
            )));
        }
        Ok(())
    }

    fn word_index(&self, offset: u64) -> Result<u32> {
        if !offset.is_multiple_of(4) || offset + 4 > self.size {
            return Err(Error::Io(io::Error::new(
                io::ErrorKind::InvalidInput,
                "backend: misaligned or out-of-range atomic offset",
            )));
        }
        Ok((offset / 4) as u32)
    }
}

fn valid_size(size: u64) -> Result<u32> {
    if size == 0 || !size.is_multiple_of(4) {
        return Err(Error::Io(io::Error::new(
            io::ErrorKind::InvalidInput,
            "backend: SharedArrayBuffer size must be a positive multiple of 4",
        )));
    }
    u32::try_from(size).map_err(|_| {
        Error::Io(io::Error::new(
            io::ErrorKind::InvalidInput,
            "backend: size does not fit in a JavaScript SharedArrayBuffer length (u32)",
        ))
    })
}

fn js_err(context: &str, v: wasm_bindgen::JsValue) -> Error {
    Error::Io(io::Error::other(format!("backend: {context}: {v:?}")))
}

impl Storage for SharedArrayBufferStorage {
    /// Reads via a bulk, non-atomic copy out of the `SharedArrayBuffer`.
    /// Safe for the ring buffer's payload bytes because access to them is
    /// already gated by the atomic head/tail handshake (see
    /// [`load_u32_at`](Storage::load_u32_at)).
    fn read_at(&self, buf: &mut [u8], offset: u64) -> Result<()> {
        if buf.is_empty() {
            return Ok(());
        }
        self.check_range(offset, buf.len() as u64)?;
        let start = offset as u32;
        let end = start + buf.len() as u32;
        self.bytes.subarray(start, end).copy_to(buf);
        Ok(())
    }

    /// Writes via a bulk, non-atomic copy into the `SharedArrayBuffer`. See
    /// [`read_at`](Storage::read_at) for why that's safe here.
    fn write_at(&self, buf: &[u8], offset: u64) -> Result<()> {
        if buf.is_empty() {
            return Ok(());
        }
        self.check_range(offset, buf.len() as u64)?;
        let start = offset as u32;
        let end = start + buf.len() as u32;
        self.bytes.subarray(start, end).copy_from(buf);
        Ok(())
    }

    fn size(&self) -> u64 {
        self.size
    }

    /// A no-op: JavaScript's garbage collector owns the `SharedArrayBuffer`.
    fn close(self) -> Result<()> {
        Ok(())
    }

    /// Implements the atomic load via `Atomics.load`.
    fn load_u32_at(&self, offset: u64) -> Result<u32> {
        let idx = self.word_index(offset)?;
        let v = Atomics::load(&self.words, idx).map_err(|e| js_err("Atomics.load", e))?;
        Ok(v as u32)
    }

    /// Implements the atomic store via `Atomics.store`, then wakes any
    /// thread (typically a Web Worker) parked in `Atomics.wait`/
    /// `Atomics.waitAsync` on this same word via `Atomics.notify` -- a
    /// cheap no-op if nobody's waiting. This is what lets
    /// [`wasm_api`](crate::wasm_api)'s async `write`/`read` await a real
    /// wakeup instead of polling on a fixed timer; see
    /// [`wait_async`](SharedArrayBufferStorage::wait_async).
    fn store_u32_at(&self, offset: u64, value: u32) -> Result<()> {
        let idx = self.word_index(offset)?;
        Atomics::store(&self.words, idx, value as i32).map_err(|e| js_err("Atomics.store", e))?;
        Atomics::notify(&self.words, idx).map_err(|e| js_err("Atomics.notify", e))?;
        Ok(())
    }
}

impl SharedArrayBufferStorage {
    /// Starts an `Atomics.waitAsync` for the word at `offset`, bounded by
    /// `timeout_ms` as a safety net (a real `store_u32_at` elsewhere wakes
    /// this immediately via `Atomics.notify`, regardless of the timeout).
    ///
    /// Returns `None` if the word no longer holds `old` already (the spec
    /// checks this atomically as part of starting the wait, the same
    /// compare-on-entry a real futex does -- see
    /// [`Atomics.waitAsync`](https://developer.mozilla.org/en-US/docs/Web/JavaScript/Reference/Global_Objects/Atomics/waitAsync)),
    /// in which case the caller should just retry its real condition
    /// immediately rather than await anything. Otherwise returns
    /// `Some(promise)`; the promise resolves once notified or once
    /// `timeout_ms` elapses, whichever comes first -- like a real futex
    /// wait, resolving does not by itself mean the word actually changed,
    /// only that it's worth checking again.
    ///
    /// wasm-only (`Atomics.waitAsync` doesn't exist off the web platform)
    /// and crate-internal: [`wasm_api`](crate::wasm_api) is the only
    /// caller, reaching in via
    /// [`Writer::storage`](crate::Writer::storage)/[`Reader::storage`](crate::Reader::storage).
    pub(crate) fn wait_async(
        &self,
        offset: u64,
        old: u32,
        timeout_ms: f64,
    ) -> Result<Option<js_sys::Promise>> {
        let idx = self.word_index(offset)?;
        let outcome = Atomics::wait_async_with_timeout(&self.words, idx, old as i32, timeout_ms)
            .map_err(|e| js_err("Atomics.waitAsync", e))?;
        let is_async = js_sys::Reflect::get(&outcome, &wasm_bindgen::JsValue::from_str("async"))
            .map_err(|e| js_err("Atomics.waitAsync result .async", e))?
            .as_bool()
            .unwrap_or(false);
        if !is_async {
            return Ok(None);
        }
        let value = js_sys::Reflect::get(&outcome, &wasm_bindgen::JsValue::from_str("value"))
            .map_err(|e| js_err("Atomics.waitAsync result .value", e))?;
        Ok(Some(js_sys::Promise::from(value)))
    }
}
