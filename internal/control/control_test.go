package control

import (
	"context"
	"sync/atomic"
	"testing"
)

func TestStopUsesPrivateEndpoint(t *testing.T) {
	data := t.TempDir()
	var called atomic.Int32
	server, err := Start(data, func(context.Context) error { called.Add(1); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := Stop(t.Context(), data); err != nil {
		t.Fatal(err)
	}
	if called.Load() != 1 {
		t.Fatal("stop not called")
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if err := Stop(t.Context(), data); err == nil {
		t.Fatal("closed endpoint accepted")
	}
}
