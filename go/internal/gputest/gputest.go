// Package gputest lets the tests that load a real model skip, not fail,
// when the card is busy (runtime-wire step-2 review): the harness, a
// llama-server and a bench share one 8 GB GPU on taycann, and a load that
// cannot get its context says nothing about the code under test.
package gputest

import (
	"os/exec"
	"strings"
	"testing"
)

// busyMarks are what llama.cpp / whisper.cpp say when memory runs out.
var busyMarks = []string{"cannot create context", "failed to load model", "out of memory",
	"cudaMalloc", "failed to allocate", "unable to load model", "failed to initialize"}

// LoadOK ends the test on a load error: a skip when the error is the
// card's (out of memory), a failure otherwise.
func LoadOK(t testing.TB, err error) {
	t.Helper()
	if err == nil {
		return
	}
	msg := strings.ToLower(err.Error())
	for _, m := range busyMarks {
		if strings.Contains(msg, strings.ToLower(m)) {
			t.Skipf("GPU busy, model load skipped (%v); in use: %s", err, inUse())
		}
	}
	t.Fatal(err)
}

func inUse() string {
	out, err := exec.Command("nvidia-smi", "--query-gpu=memory.used,memory.total",
		"--format=csv,noheader").Output()
	if err != nil {
		return "nvidia-smi unavailable"
	}
	return strings.TrimSpace(string(out))
}
