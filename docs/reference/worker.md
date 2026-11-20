# The browser worker

`cmd/greenware-worker` is the Greenware compiler as a Web Worker module. It is the same compiler as `greenware build`: it calls `ast.LowerWith` and `compiler.BuildWith`, and it has no second evaluator. The build is standard Go for `GOOS=js GOARCH=wasm`. TinyGo is not a target.

```sh
CGO_ENABLED=0 GOOS=js GOARCH=wasm go build -o greenware-worker.wasm ./cmd/greenware-worker
```

The page loads the module in a Web Worker with Go's own `wasm_exec.js` (`$(go env GOROOT)/lib/wasm/wasm_exec.js`). Load the Go runtime and the module, call `go.run(instance)`, and wait for the string `greenware.worker.ready/1`.

## The one door

The module sets `self.onmessage` and publishes no other global. A worker is reached only through its message port, so a registered global function would be a second door that only code on the worker's own thread could use. (The game's rule that an engine publishes no global functions holds here for the same reason.) `syscall/js` is used in one file, `cmd/greenware-worker/serve_js.go`. Everything else is `internal/workerproto`, which is plain Go and is tested natively.

A message is a `Uint8Array` or an `ArrayBuffer` that holds a request document, or an event whose `data` is one. The reply is a `Uint8Array` that holds a response document, posted with its buffer transferred. One request gets one reply, in order.

## Request

One JSON document, at most 8 MiB. Unknown keys, a different `schema` and trailing data are refused.

| Key | Meaning |
|---|---|
| `schema` | `greenware.worker.request/1` |
| `id` | Any string. The reply carries it back. |
| `root` | The source file to compile, a key of `files`. It ends in `.gw`. |
| `output` | One output name. Empty builds every output. |
| `files` | Path to bytes (base64). The sealed project snapshot. |
| `options.max_work` | The `total_work` ceiling. Zero is the default, 20,000,000. |
| `options.runtime_points` | The `runtime_points` limit. Zero is the default. |
| `options.runtime_safe` | `required` (the default) or `off`. The worker refuses `off`. |

Paths are relative, slash-separated and clean, with no `..`, no leading `/` and no backslash. At most 64 files, 2 MiB per file and 4 MiB in all: the `snapshot` ticket class. A project import or a table is read from `files` and from nowhere else; a file the page did not send is `GW-MISSING`. The project root is the snapshot root, and the root file's folder is the base of its relative paths. `greenware build` finds the root as the nearest `greenware.mod`; for a project whose root is not the folder it was started from, send the files relative to that root to get the same input record.

## Response

One JSON document, at most 16 MiB. An answer that would be larger is replaced by a `too_large` error. Keys come in the order below; the same request gives the same bytes.

| Key | Meaning |
|---|---|
| `schema` | `greenware.worker.response/1` |
| `id` | The request's `id`. |
| `ok` | True when the build wrote every file. |
| `error` | `{code, message}` when the request itself failed: `bad_request`, `too_large`, `canceled`, `internal`. |
| `files` | The GLBs, every output in order, its levels in order: `{output, name, level, sha256, glb}`. `glb` is base64. |
| `record` | `{name, sha256, bytes}`: the exact build record file `greenware build` writes. |
| `diagnostics` | A `greenware.diagnostics/1` document, with `GW-` codes and byte, line and column spans into the root file. Present when anything was reported, a warning included. |

A source that does not build is not an error: it is `ok` false with `diagnostics`.

## runtime_safe required

The worker always imposes `runtime_safe required` (compiler.md 9.4). Admission runs after the front end and before any geometry is built. It refuses:

| Case | Code |
|---|---|
| `max_work` above the edition default, which is the worker's ceiling. A request may lower the ceiling, not raise it. | `GW-LIMIT` |
| A `check rig` audit, a `check runtime` audit, or a mount with a fit check. They are the full athlete audit, which goes to the native build or the service, and they have no certified worker charge. | `GW-RUNTIME-UNSAFE` |

Everything else the compiler builds today has a bounded ticket in the cost schedule, so admission passes it on to `BuildWith`, which reserves the tickets and refuses with `GW-LIMIT` before it builds. The compiler has no boolean, subdivision, remeshing, mesh import, weight solving, sculpt or unwrap; a source that asks for one gets `GW-UNSUPPORTED` from the front end, as it does natively.

Not provided: a wall-clock budget (one thread cannot watch itself), acceptance and outline checks (a request has no acceptance file, and the worker has no renderer), and a `geometry_only` option.

## Cancellation

`BuildWith` has no phase boundary to check, so a build that has started runs to its end. A native host can set `Host.Canceled`, which `workerproto` asks between four phases: parse and lower, admission, build, encode. The WebAssembly worker has one thread, so nothing can answer true while it builds. A page cancels a running build with `Worker.terminate()` and starts a new worker. The module keeps no state between requests.

## Checks

- `go test ./internal/workerproto/... ./cmd/greenware-worker/...` runs natively. It checks the goal post's six GLBs against `testdata/compiler/approved/examples-goalpost.provisional.sha256`, the response against `compiler.Build`, the refusals, the caps and cancellation.
- `GOOS=js GOARCH=wasm go vet ./cmd/greenware-worker/` checks the browser file.
- `testdata/worker/drive.cjs` runs the real module in Node. It is a 36-line test driver for this compiler repository, not part of the compiler and not game code. It loads the module with `wasm_exec.js`, posts a request to `self.onmessage`, and prints `sha256 bytes name` for every file and the record, and the timings.

```sh
node testdata/worker/drive.cjs greenware-worker.wasm "$(go env GOROOT)/lib/wasm/wasm_exec.js" examples/goalpost/goalpost.gw "" 30
```
