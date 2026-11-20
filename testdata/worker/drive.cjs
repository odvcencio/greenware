// Test driver for cmd/greenware-worker, not part of the compiler or the game.
// It acts as the page: loads the wasm with Go's own wasm_exec.js, posts one
// request to self.onmessage and prints "sha256 bytes name" for every file.
//   node drive.cjs WASM WASM_EXEC.JS SOURCE.gw OUTPUT RUNS
// Timings go to stderr as one JSON line. Files go to stdout, sorted by name.
const fs = require("fs"), path = require("path"), crypto = require("crypto");
const [wasmPath, execPath, srcPath, output, runs] = process.argv.slice(2);
globalThis.self = globalThis; // a worker's global scope is self
require(path.resolve(execPath));
const inbox = []; let wake = null;
globalThis.postMessage = (m) => (wake ? (([w]) => ((wake = null), w(m)))([wake]) : inbox.push(m));
const next = () => new Promise((r) => (inbox.length ? r(inbox.shift()) : (wake = r)));
const sha = (b) => crypto.createHash("sha256").update(b).digest("hex");
(async () => {
  const t0 = performance.now(), go = new Go();
  const { instance } = await WebAssembly.instantiate(fs.readFileSync(wasmPath), go.importObject);
  go.run(instance);
  const ready = await next(), cold = performance.now() - t0;
  const name = path.basename(srcPath);
  const req = Buffer.from(JSON.stringify({ schema: "greenware.worker.request/1", id: "post", root: name, output,
    files: { [name]: fs.readFileSync(srcPath).toString("base64") }, options: {} }));
  const ms = []; let resp;
  for (let i = 0; i < Number(runs); i++) {
    const t = performance.now();
    self.onmessage({ data: new Uint8Array(req) });
    resp = JSON.parse(Buffer.from(await next()).toString());
    ms.push(performance.now() - t);
  }
  const files = resp.files.map((f) => ({ n: f.name, b: Buffer.from(f.glb, "base64") }));
  files.push({ n: resp.record.name, b: Buffer.from(resp.record.bytes, "base64") });
  for (const f of files.sort((a, b) => (a.n < b.n ? -1 : 1))) console.log(sha(f.b), f.b.length, f.n);
  ms.sort((a, b) => a - b);
  console.error(JSON.stringify({ ready, ok: resp.ok, cold_start_ms: Math.round(cold), first_ms: Math.round(ms[0]), runs: ms.length,
    p50_ms: Math.round(ms[ms.length >> 1]), p95_ms: Math.round(ms[Math.ceil(ms.length * 0.95) - 1]) }));
  process.exit(0);
})();
