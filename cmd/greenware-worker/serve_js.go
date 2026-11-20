//go:build js && wasm

package main

import (
	"fmt"
	"syscall/js"

	"m31labs.dev/greenware/internal/workerproto"
)

// serve registers self.onmessage and then blocks forever. This file is the
// only place the worker touches syscall/js.
func serve() error {
	global := js.Global()
	self := global.Get("self")
	if self.IsUndefined() {
		return fmt.Errorf("no self: this module must run in a worker")
	}
	u8 := global.Get("Uint8Array")
	handler := js.FuncOf(func(this js.Value, args []js.Value) any {
		if len(args) == 0 {
			return nil
		}
		reply(self, u8, request(u8, args[0]))
		return nil
	})
	self.Set("onmessage", handler)
	self.Call("postMessage", ReadyMessage)
	select {}
}

// request answers one message event.
func request(u8, ev js.Value) []byte {
	data := ev.Get("data")
	if !data.InstanceOf(u8) {
		if data.InstanceOf(js.Global().Get("ArrayBuffer")) {
			data = u8.New(data)
		} else {
			return workerproto.Refuse("", workerproto.ErrBadRequest, "the message is not a Uint8Array or an ArrayBuffer")
		}
	}
	n := data.Get("byteLength").Int()
	if n > workerproto.MaxRequestBytes {
		return workerproto.Refuse("", workerproto.ErrTooLarge,
			fmt.Sprintf("the request is %d bytes; the limit is %d", n, workerproto.MaxRequestBytes))
	}
	buf := make([]byte, n)
	js.CopyBytesToGo(buf, data)
	return answer(buf)
}

// reply posts the response bytes and transfers their buffer.
func reply(self, u8 js.Value, b []byte) {
	out := u8.New(len(b))
	js.CopyBytesToJS(out, b)
	self.Call("postMessage", out, []any{out.Get("buffer")})
}
