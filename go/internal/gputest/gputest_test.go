package gputest

import (
	"errors"
	"testing"
)

func TestBusyLoadSkips(t *testing.T) {
	var sub *testing.T
	t.Run("busy", func(t *testing.T) {
		sub = t
		LoadOK(t, errors.New("llm: cannot create context"))
		t.Error("a busy card did not skip")
	})
	if !sub.Skipped() {
		t.Error("not skipped")
	}
	t.Run("ok", func(t *testing.T) { LoadOK(t, nil) })
}
