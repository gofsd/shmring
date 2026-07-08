// Thin JavaScript wrapper around the shmring WebAssembly module. Mirrors
// the Go API (Writer/Reader, write/read, tryWrite/tryRead, close) as
// closely as JS idiom allows: methods throw on error instead of returning
// a Go-style (value, error) pair, and the blocking write()/read() are
// async (Promise-returning) since they can genuinely wait for the other
// side.
//
// Requires a page/worker that is cross-origin isolated, since
// SharedArrayBuffer is unavailable otherwise -- see web/example.

import "./wasm_exec.js"; // side effect: defines globalThis.Go

/**
 * URL of the shmring.wasm binary bundled alongside this module. Bundlers
 * (Vite, webpack, etc.) that understand `new URL(..., import.meta.url)`
 * asset references will resolve and copy it automatically; otherwise pass
 * your own URL/path to loadShmring instead of this one.
 */
export const wasmURL = new URL("./shmring.wasm", import.meta.url);

/**
 * Instantiates shmring.wasm and returns the raw globalThis.shmring
 * bindings it installs. Call this once per thread (once on the main
 * thread, once per Worker) that will be a Writer or Reader.
 * @param {string|URL} wasmUrl
 * @returns {Promise<object>}
 */
export async function loadShmring(wasmUrl) {
  const go = new globalThis.Go();
  const resp = await fetch(wasmUrl);
  const { instance } = await WebAssembly.instantiateStreaming(resp, go.importObject);
  // go.run's Promise only resolves when the Go program's main() returns,
  // which it never does (see web/wasm/main.go) -- do not await it here.
  go.run(instance);
  return globalThis.shmring;
}

function unwrap({ value, err }) {
  if (err) throw new Error(err);
  return value;
}

export class Writer {
  #raw;
  #id;

  constructor(raw, id) {
    this.#raw = raw;
    this.#id = id;
  }

  /** Non-blocking; returns the number of bytes written (may be less than data.length, or 0 if full). */
  tryWrite(data) {
    return unwrap(this.#raw.writerTryWrite(this.#id, data)).n;
  }

  /** Blocks (asynchronously, without freezing the page/worker) until all of data is written. */
  async write(data) {
    return unwrap(await this.#raw.writerWrite(this.#id, data)).n;
  }

  /** Marks the ring buffer closed; the Reader observes EOF once it drains what's left. */
  close() {
    unwrap(this.#raw.writerClose(this.#id));
  }
}

export class Reader {
  #raw;
  #id;

  constructor(raw, id) {
    this.#raw = raw;
    this.#id = id;
  }

  /** Non-blocking; returns {n, eof}. n may be 0 with eof false if nothing is available yet. */
  tryRead(into) {
    return unwrap(this.#raw.readerTryRead(this.#id, into));
  }

  /** Blocks (asynchronously) until at least one byte is available; returns {n, eof}. */
  async read(into) {
    return unwrap(await this.#raw.readerRead(this.#id, into));
  }

  close() {
    unwrap(this.#raw.readerClose(this.#id));
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
  const { writerId, sab } = unwrap(raw.createWriter(capacity));
  return { writer: new Writer(raw, writerId), sab };
}

/**
 * Opens a ring buffer's Reader side from a SharedArrayBuffer produced by
 * createWriter on another thread. capacity must match.
 * @param {object} raw - the value returned by loadShmring
 * @param {SharedArrayBuffer} sab
 * @param {number} capacity
 */
export function openReader(raw, sab, capacity) {
  const { readerId } = unwrap(raw.openReader(sab, capacity));
  return new Reader(raw, readerId);
}
