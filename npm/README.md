# shmring (web)

Browser build of [shmring](https://github.com/gofsd/shmring): a
fixed-capacity, single-producer/single-consumer byte ring buffer, backed
here by a `SharedArrayBuffer` and compiled from the same Go source that runs
natively on desktop and Android. One thread (main thread or a Worker)
creates the ring and gets a `Writer`; another thread opens the same
`SharedArrayBuffer` and gets a `Reader`. Bytes written on one side become
readable on the other, in order.

## Requirements

`SharedArrayBuffer` is only available on pages that are [cross-origin
isolated](https://developer.mozilla.org/en-US/docs/Web/API/crossOriginIsolated),
which requires your server to send:

```
Cross-Origin-Opener-Policy: same-origin
Cross-Origin-Embedder-Policy: require-corp
```

## Install

```sh
npm install shmring
```

## Usage

`loadShmring` instantiates the wasm module and must be called once per
thread that will act as a Writer or Reader (once on the main thread, once
per Worker).

**Main thread** (creates the ring, writes into it):

```js
import { loadShmring, createWriter, wasmURL } from "shmring";

const raw = await loadShmring(wasmURL);
const { writer, sab } = createWriter(raw, 4096); // capacity: power of two, bytes

const worker = new Worker("worker.js", { type: "module" });
worker.postMessage({ sab, capacity: 4096 });

const encoder = new TextEncoder();
await writer.write(encoder.encode("hello\n"));
writer.close(); // signals EOF once the reader drains what's left
```

**Worker** (`worker.js`; opens the same ring, reads from it):

```js
import { loadShmring, openReader, wasmURL } from "shmring";

const readyPromise = loadShmring(wasmURL);

self.onmessage = async (e) => {
  const raw = await readyPromise;
  const { sab, capacity } = e.data;
  const reader = openReader(raw, sab, capacity);

  const buf = new Uint8Array(256);
  for (;;) {
    const { n, eof } = await reader.read(buf);
    if (n > 0) postMessage(buf.slice(0, n));
    if (eof) {
      reader.close();
      return;
    }
  }
};
```

`wasmURL` is a `new URL("./shmring.wasm", import.meta.url)` reference;
bundlers that understand that pattern (Vite, webpack 5+, esbuild) will
resolve and copy the `.wasm` asset automatically. If yours doesn't, fetch
`node_modules/shmring/shmring.wasm` yourself and pass that URL/path instead.

## API

- `loadShmring(wasmUrl) -> Promise<raw>` — instantiate the wasm module for
  the current thread.
- `createWriter(raw, capacity) -> { writer, sab }` — create a new ring;
  `sab` is the `SharedArrayBuffer` to hand to the Reader's thread.
- `openReader(raw, sab, capacity) -> reader` — open the Reader side of a
  ring created by `createWriter` on another thread.
- `Writer#write(data) -> Promise<n>` / `Writer#tryWrite(data) -> n` —
  blocking (async) and non-blocking writes.
- `Writer#close()` — marks the ring closed; already-written data is still
  readable until drained.
- `Reader#read(into) -> Promise<{n, eof}>` / `Reader#tryRead(into) ->
  {n, eof}` — blocking (async) and non-blocking reads; `eof` is true once
  the writer has closed and all data has been drained.
- `Reader#close()` — releases this thread's handle on the `SharedArrayBuffer`.

See the [main README](https://github.com/gofsd/shmring#web) and
[web/example](https://github.com/gofsd/shmring/tree/main/web/example) for a
complete working demo.
