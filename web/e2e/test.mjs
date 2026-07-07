// End-to-end check for the web/example demo: drives a real headless
// Chrome, loads the page over a server that sets the COOP/COEP headers
// SharedArrayBuffer requires, and confirms the main thread (Writer) and a
// Web Worker (Reader) actually exchange data through the compiled
// shmring.wasm module.
//
// Usage: node test.mjs [baseURL]   (baseURL defaults to http://localhost:8080)
// Requires: `mage web:build` to have produced web/example/shmring.wasm,
// and a devserver (e.g. `go run ../devserver`) already running at baseURL.
import { existsSync } from "node:fs";
import puppeteer from "puppeteer-core";

const baseURL = process.argv[2] ?? "http://localhost:8080";
const chromePath =
  process.env.CHROME_PATH ??
  ["/usr/bin/google-chrome", "/usr/bin/chromium", "/usr/bin/chromium-browser"].find(existsSync);

if (!chromePath) {
  console.error("FAIL: no Chrome/Chromium found; set CHROME_PATH");
  process.exit(1);
}

const browser = await puppeteer.launch({
  executablePath: chromePath,
  headless: "new",
  args: ["--no-sandbox", "--disable-dev-shm-usage"],
});

try {
  const page = await browser.newPage();
  page.on("pageerror", (err) => console.error(`[pageerror] ${err.message}`));
  page.on("workercreated", (worker) => {
    worker.on("error", (err) => console.error(`[worker error] ${err.message}`));
  });

  await page.goto(`${baseURL}/example/`, { waitUntil: "networkidle0" });

  const crossOriginIsolated = await page.evaluate(() => window.crossOriginIsolated);
  if (!crossOriginIsolated) {
    throw new Error("page is not cross-origin isolated (missing COOP/COEP headers?) -- SharedArrayBuffer unavailable");
  }

  await page.waitForFunction(() => document.getElementById("status").textContent === "done", {
    timeout: 15000,
  });

  const logText = await page.evaluate(() => document.getElementById("log").textContent);
  const sent = (logText.match(/sent:/g) || []).length;
  const received = (logText.match(/received:/g) || []).length;
  if (sent === 0 || sent !== received) {
    throw new Error(`sent/received mismatch: sent=${sent} received=${received}\n${logText}`);
  }

  console.log(`PASS: ${sent} messages round-tripped through main thread <-> worker via SharedArrayBuffer`);
} finally {
  await browser.close();
}
