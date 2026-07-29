//! JavaScript-facing bindings, compiled only for `wasm32-unknown-unknown`.
//!
//! `#[wasm_bindgen]` cannot export an `async fn` taking `&self`/`&mut self`
//! on a struct (the generated JS-facing future must be `'static`, and a
//! borrow of `self` isn't). So every exported method here is synchronous;
//! the blocking `write`/`read` clone an owned `Rc` handle and hand it to
//! [`wasm_bindgen_futures::future_to_promise`], which drives the loop on
//! its own -- each iteration takes a short-lived `borrow_mut()` around a
//! single `try_write`/`try_read` call, drops it, then awaits a wakeup, so
//! no borrow ever spans an `.await`. This is the direct analog of the Go
//! wasm build's goroutine-wrapped-in-a-Promise trick
//! (`web/wasm/promise.go`), expressed as native async instead.
//!
//! The wait between attempts is `Atomics.waitAsync`
//! ([`SharedArrayBufferStorage::wait_async`](crate::backend::SharedArrayBufferStorage::wait_async)),
//! not a fixed timer: it resolves as soon as the other side's
//! `store_u32_at` calls `Atomics.notify` (see backend/wasm.rs), so a
//! writer/reader parked here wakes within about a microtask of the actual
//! event instead of on the next poll tick. `WAIT_TIMEOUT_MS` is a safety
//! net for that wait, not the primary wakeup path.
//!
//! `Rc`, not `Arc`: each wasm module instance is single-threaded (see the
//! crate docs' Concurrency model / `SharedArrayBufferStorage`'s docs), so
//! `Send`/`Sync` bounds would claim a safety property that doesn't exist
//! and isn't needed.

use std::cell::RefCell;
use std::rc::Rc;

use js_sys::{SharedArrayBuffer, Uint8Array};
use wasm_bindgen::prelude::*;
use wasm_bindgen_futures::{future_to_promise, JsFuture};

use crate::backend::SharedArrayBufferStorage;
use crate::header::{HEADER_SIZE, OFF_HEAD, OFF_TAIL};
use crate::{Error, Options, Reader, Writer};

/// Safety-net bound for a single `Atomics.waitAsync` call: if the other
/// side's notify is somehow missed, this is the longest a writer/reader
/// waits before retrying its real condition anyway. A real notify
/// resolves the wait far sooner than this, regardless of its value.
const WAIT_TIMEOUT_MS: f64 = 200.0;

fn to_js_err(e: Error) -> JsValue {
    JsValue::from_str(&e.to_string())
}

/// `{n, eof}`, mirroring the Go wasm build's `readerTryRead`/`readerRead`
/// return shape. Returned by [`WasmReader::try_read`], where `into` is
/// mutated in place by wasm-bindgen's `&mut [u8]` copy-in/copy-out.
#[wasm_bindgen(getter_with_clone)]
pub struct ReadResult {
    pub n: u32,
    pub eof: bool,
}

/// `{data, n, eof}`, returned by the async [`WasmReader::read`]. Unlike
/// `try_read`, an async call can't rely on wasm-bindgen's mutable-slice
/// copy-out (that only wraps a single synchronous call boundary, and the
/// actual read happens after this function has already returned a pending
/// `Promise`) -- so the bytes come back explicitly as `data` instead of
/// being written into a caller-supplied buffer.
#[wasm_bindgen(getter_with_clone)]
pub struct AsyncReadResult {
    pub data: Uint8Array,
    pub n: u32,
    pub eof: bool,
}

/// `{writer, sab}`, returned by [`create_writer`].
#[wasm_bindgen(getter_with_clone)]
pub struct CreateWriterResult {
    pub writer: WasmWriter,
    pub sab: SharedArrayBuffer,
}

/// Creates a new ring buffer over a fresh `SharedArrayBuffer`. Transfer
/// `sab` to whichever thread should be the reader (e.g.
/// `worker.postMessage(sab)`) and open it there with [`open_reader`].
#[wasm_bindgen(js_name = createWriter)]
pub fn create_writer(capacity: u32) -> Result<CreateWriterResult, JsValue> {
    let storage =
        SharedArrayBufferStorage::new(HEADER_SIZE + capacity as u64).map_err(to_js_err)?;
    let sab = storage.buffer();
    let writer = Writer::new(storage, capacity as u64, Options::default()).map_err(to_js_err)?;
    Ok(CreateWriterResult {
        writer: WasmWriter(Rc::new(RefCell::new(writer))),
        sab,
    })
}

/// Opens a ring buffer's reader side from a `SharedArrayBuffer` produced by
/// [`create_writer`] on another thread. `capacity` must match.
#[wasm_bindgen(js_name = openReader)]
pub fn open_reader(sab: SharedArrayBuffer, capacity: u32) -> Result<WasmReader, JsValue> {
    let storage = SharedArrayBufferStorage::wrap(sab).map_err(to_js_err)?;
    let reader = Reader::new(storage, capacity as u64, Options::default()).map_err(to_js_err)?;
    Ok(WasmReader(Rc::new(RefCell::new(Some(reader)))))
}

/// The producer side of a ring buffer, exposed to JavaScript. `Writer`'s
/// native `close` doesn't consume `self`, so a plain `Rc<RefCell<Writer<_>>>`
/// is enough (contrast [`WasmReader`], whose native `close` does consume).
#[derive(Clone)]
#[wasm_bindgen]
pub struct WasmWriter(Rc<RefCell<Writer<SharedArrayBufferStorage>>>);

#[wasm_bindgen]
impl WasmWriter {
    /// Non-blocking; returns the number of bytes written (may be less than
    /// `data.len()`, or 0 if full).
    pub fn try_write(&self, data: &[u8]) -> Result<u32, JsValue> {
        self.0
            .borrow_mut()
            .try_write(data)
            .map(|n| n as u32)
            .map_err(to_js_err)
    }

    /// Blocks (asynchronously, without freezing the page/worker) until all
    /// of `data` is written.
    pub fn write(&self, data: Vec<u8>) -> js_sys::Promise {
        let inner = self.0.clone();
        future_to_promise(async move {
            let mut written = 0usize;
            while written < data.len() {
                let wait = {
                    let mut w = inner.borrow_mut();
                    let n = w.try_write(&data[written..]).map_err(to_js_err)?;
                    written += n;
                    if n > 0 {
                        None
                    } else {
                        let old = w.cached_head();
                        w.storage()
                            .wait_async(OFF_HEAD, old, WAIT_TIMEOUT_MS)
                            .map_err(to_js_err)?
                    }
                };
                if let Some(promise) = wait {
                    // Resolves on the reader's next notify (or the
                    // safety-net timeout); either way, loop back and
                    // re-check the real condition via try_write.
                    let _ = JsFuture::from(promise).await;
                }
            }
            Ok(JsValue::from(written as u32))
        })
    }

    /// Marks the ring buffer closed; the reader observes EOF once it drains
    /// what's left.
    pub fn close(&self) -> Result<(), JsValue> {
        self.0.borrow_mut().close().map_err(to_js_err)
    }
}

/// The consumer side of a ring buffer, exposed to JavaScript. Wrapped in
/// `Option` because native `Reader::close` consumes `self` by value (an
/// intentional, RAII-friendly design already in the crate) -- `close` here
/// takes the `Reader` out of the `Option` to actually consume it, leaving
/// `None` behind; every other method treats `None` as already-closed.
#[wasm_bindgen]
pub struct WasmReader(Rc<RefCell<Option<Reader<SharedArrayBufferStorage>>>>);

#[wasm_bindgen]
impl WasmReader {
    /// Non-blocking; returns `{n, eof}`. `n` may be 0 with `eof` false if
    /// nothing is available yet.
    pub fn try_read(&self, into: &mut [u8]) -> Result<ReadResult, JsValue> {
        let mut guard = self.0.borrow_mut();
        let reader = guard.as_mut().ok_or_else(|| to_js_err(Error::Closed))?;
        match reader.try_read(into) {
            Ok(n) => Ok(ReadResult {
                n: n as u32,
                eof: false,
            }),
            Err(Error::Eof) => Ok(ReadResult { n: 0, eof: true }),
            Err(e) => Err(to_js_err(e)),
        }
    }

    /// Blocks (asynchronously) until at least one byte is available;
    /// resolves to `{data, n, eof}` for up to `len` bytes.
    pub fn read(&self, len: u32) -> js_sys::Promise {
        let inner = self.0.clone();
        future_to_promise(async move {
            let mut scratch = vec![0u8; len as usize];
            loop {
                let wait = {
                    let mut guard = inner.borrow_mut();
                    let reader = guard.as_mut().ok_or(Error::Closed)?;
                    match reader.try_read(&mut scratch) {
                        Ok(0) => {
                            let old = reader.cached_tail();
                            reader
                                .storage()
                                .wait_async(OFF_TAIL, old, WAIT_TIMEOUT_MS)
                                .map_err(to_js_err)?
                        }
                        Ok(n) => {
                            let data = Uint8Array::new_with_length(n as u32);
                            data.copy_from(&scratch[..n]);
                            return Ok(JsValue::from(AsyncReadResult {
                                data,
                                n: n as u32,
                                eof: false,
                            }));
                        }
                        Err(Error::Eof) => {
                            return Ok(JsValue::from(AsyncReadResult {
                                data: Uint8Array::new_with_length(0),
                                n: 0,
                                eof: true,
                            }));
                        }
                        Err(e) => return Err(to_js_err(e)),
                    }
                };
                if let Some(promise) = wait {
                    // Resolves on the writer's next notify (or the
                    // safety-net timeout); either way, loop back and
                    // re-check the real condition via try_read.
                    let _ = JsFuture::from(promise).await;
                }
            }
        })
    }

    pub fn close(&self) -> Result<(), JsValue> {
        if let Some(reader) = self.0.borrow_mut().take() {
            reader.close().map_err(to_js_err)?;
        }
        Ok(())
    }
}

impl From<Error> for JsValue {
    fn from(e: Error) -> JsValue {
        to_js_err(e)
    }
}
