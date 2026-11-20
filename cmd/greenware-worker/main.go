// Command greenware-worker is the Greenware compiler as a browser worker: the
// same compiler as `greenware build`, over the message protocol of
// internal/workerproto, built with standard Go for GOOS=js GOARCH=wasm.
//
//	CGO_ENABLED=0 GOOS=js GOARCH=wasm go build -o greenware-worker.wasm ./cmd/greenware-worker
//
// The page loads the module in a Web Worker with Go's wasm_exec.js. The module
// registers one message handler, self.onmessage, and posts the string
// "greenware.worker.ready/1" when it is listening. It publishes no other
// global. A message is a Uint8Array (or ArrayBuffer) holding a request
// document, or an event whose data is one. The reply is posted as a Uint8Array
// that holds a response document, with its buffer transferred. See
// docs/reference/worker.md.
//
// Why a handler on self and not a registered function: a worker's only door is
// its message port, and a page can already post to it. A global function would
// be a second door that only code on the worker's own thread can call.
//
// The build runs on the worker's single thread, so a page cancels a running
// build by terminating the worker. The module has no state between requests.
package main

import (
	"fmt"
	"os"

	"m31labs.dev/greenware/internal/workerproto"
)

// ReadyMessage is the string the worker posts when it is listening.
const ReadyMessage = "greenware.worker.ready/1"

// answer turns one request message into one response message. It is the whole
// of the worker apart from the JavaScript calls in serve_js.go.
func answer(msg []byte) []byte {
	return workerproto.HandleBytes(msg)
}

func main() {
	if err := serve(); err != nil {
		fmt.Fprintln(os.Stderr, "greenware-worker:", err)
		os.Exit(2)
	}
}
