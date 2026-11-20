package main

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/hollis-labs/nanite/internal/control"
)

func TestStopAllCommandUsesRunningAgent(t *testing.T) {
	dir := t.TempDir()
	data := filepath.Join(dir, ".nanite")
	if err := os.Mkdir(data, 0700); err != nil {
		t.Fatal(err)
	}
	var called atomic.Bool
	server, err := control.Start(data, func(context.Context) error { called.Store(true); return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if err := runArgs(t.Context(), []string{"--dir", dir, "stop", "--all"}); err != nil {
		t.Fatal(err)
	}
	if !called.Load() {
		t.Fatal("stop not invoked")
	}
	if err := runArgs(t.Context(), []string{"--dir", dir, "stop"}); err == nil {
		t.Fatal("missing --all accepted")
	}
}
