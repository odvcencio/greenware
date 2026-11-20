package main

import (
	"encoding/json"
	"os"
	"testing"

	"m31labs.dev/greenware/internal/workerproto"
)

// answer is the whole worker apart from the JavaScript edge: a request message
// in, a response message out.
func TestAnswer(t *testing.T) {
	src, err := os.ReadFile("../../examples/goalpost/goalpost.gw")
	if err != nil {
		t.Fatal(err)
	}
	msg, _ := json.Marshal(workerproto.Request{Schema: workerproto.RequestSchema, ID: "1", Root: "goalpost.gw", Output: "practice",
		Files: map[string][]byte{"goalpost.gw": src}})
	var resp workerproto.Response
	if err := json.Unmarshal(answer(msg), &resp); err != nil || !resp.OK || len(resp.Files) != 3 || resp.ID != "1" {
		t.Fatalf("%v %+v", err, resp)
	}
}

// A native build is not the worker and says so.
func TestServeNative(t *testing.T) {
	if err := serve(); err == nil {
		t.Skip("running as a worker")
	}
}
