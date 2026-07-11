// Thin JavaScript wrapper around the shmring WebAssembly module. Mirrors
// the Go API (Writer/Reader, write/read, tryWrite/tryRead, close) as
// closely as JS idiom allows: methods throw on error instead of returning
// a Go-style (value, error) pair, and the blocking write()/read() are
// async (Promise-returning) since they can genuinely wait for the other
// side.
//
// Compiled from rust/ via wasm-bindgen (see rust/src/wasm_api.rs), not Go
// -- but this wrapper's public API (loadShmring, wasmURL, Writer, Reader,
// createWriter, openReader, and every method's call signature) is
// unchanged, so web/example and web/e2e don't need to know or care.
//
// Requires a page/worker that is cross-origin isolated, since
// SharedArrayBuffer is unavailable otherwise -- see web/example.

import init, * as wasmModule from "./shmring_wasm.js";

/**
 * URL of the shmring_wasm_bg.wasm binary bundled alongside this module.
 * Bundlers (Vite, webpack, etc.) that understand `new URL(..., import.meta.url)`
 * asset references will resolve and copy it automatically; otherwise pass
 * your own URL/path to loadShmring instead of this one.
 */
export const wasmURL = new URL("./shmring_wasm_bg.wasm", import.meta.url);

/**
 * Instantiates the shmring wasm module and returns its raw bindings
 * (createWriter/openReader/WasmWriter/WasmReader). Call this once per
 * thread (once on the main thread, once per Worker) that will be a Writer
 * or Reader.
 * @param {string|URL} wasmUrl
 * @returns {Promise<object>}
 */
export async function loadShmring(wasmUrl) {
  await init({ module_or_path: wasmUrl });
  return wasmModule;
}

export class Writer {
  #raw;

  constructor(raw) {
    this.#raw = raw;
  }

  /** Non-blocking; returns the number of bytes written (may be less than data.length, or 0 if full). */
  tryWrite(data) {
    return this.#raw.try_write(data);
  }

  /** Blocks (asynchronously, without freezing the page/worker) until all of data is written. */
  async write(data) {
    return await this.#raw.write(data);
  }

  /** Marks the ring buffer closed; the Reader observes EOF once it drains what's left. */
  close() {
    this.#raw.close();
  }
}

export class Reader {
  #raw;

  constructor(raw) {
    this.#raw = raw;
  }

  /** Non-blocking; returns {n, eof}. n may be 0 with eof false if nothing is available yet. into is mutated in place. */
  tryRead(into) {
    const { n, eof } = this.#raw.try_read(into);
    return { n, eof };
  }

  /** Blocks (asynchronously) until at least one byte is available; mutates into and returns {n, eof}. */
  async read(into) {
    const { data, n, eof } = await this.#raw.read(into.length);
    if (n > 0) into.set(data);
    return { n, eof };
  }

  close() {
    this.#raw.close();
  }
}

/**
 * Creates a new ring buffer. Returns the Writer plus the SharedArrayBuffer
 * to transfer to whichever thread should be the Reader (e.g.
 * worker.postMessage({sab, capacity})).
 * @param {object} raw - the value returned by loadShmring
 * @param {number} capacity - power of two, in bytes
 */
export function createWriter(raw, capacity) {
  const { writer, sab } = raw.createWriter(capacity);
  return { writer: new Writer(writer), sab };
}

/**
 * Opens a ring buffer's Reader side from a SharedArrayBuffer produced by
 * createWriter on another thread. capacity must match.
 * @param {object} raw - the value returned by loadShmring
 * @param {SharedArrayBuffer} sab
 * @param {number} capacity
 */
export function openReader(raw, sab, capacity) {
  return new Reader(raw.openReader(sab, capacity));
}
