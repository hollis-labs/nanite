//go:build devmode

package muxproxy

import (
	"testing"
)

func TestDaemonEndpoint(t *testing.T) {
	if daemonEndpoint != "unix:~/.agent-mux/run/muxd.sock" {
		t.Fatalf("unexpected daemonEndpoint %q", daemonEndpoint)
	}
}

func TestClientSingleton(t *testing.T) {
	a := Client()
	b := Client()
	if a == nil {
		t.Fatal("Client() returned nil")
	}
	if a != b {
		t.Fatal("Client() not a singleton")
	}
}
