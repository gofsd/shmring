import { loadShmring, openReader } from "../shmring.js";

// Kick off wasm loading but don't await it at the top level: onmessage must
// be attached synchronously, before the first await, or a postMessage sent
// right after `new Worker(...)` can arrive before anyone is listening and
// be lost (the event fires with no handler attached and is gone for good).
const readyPromise = loadShmring("shmring.wasm");

self.onmessage = async (e) => {
  const raw = await readyPromise;
  const { sab, capacity } = e.data;
  const reader = openReader(raw, sab, capacity);

  const decoder = new TextDecoder();
  const buf = new Uint8Array(256);
  let pending = "";

  for (;;) {
    const { n, eof } = await reader.read(buf);
    if (n > 0) {
      pending += decoder.decode(buf.subarray(0, n), { stream: true });
      let idx;
      while ((idx = pending.indexOf("\n")) !== -1) {
        postMessage({ type: "message", text: pending.slice(0, idx) });
        pending = pending.slice(idx + 1);
      }
    }
    if (eof) {
      reader.close();
      postMessage({ type: "done" });
      return;
    }
  }
};
