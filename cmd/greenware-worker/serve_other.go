//go:build !(js && wasm)

package main

import "errors"

// serve reports that this build is not the worker. The worker is built with
// GOOS=js GOARCH=wasm; a native build exists so that `go build ./...` and
// `go test ./...` cover the package.
func serve() error {
	return errors.New("this command runs in a browser worker; build it with GOOS=js GOARCH=wasm")
}
