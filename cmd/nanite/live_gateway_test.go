package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLiveGatewayToolTurn(t *testing.T) {
	if os.Getenv("NANITE_LIVE_TEST") != "1" {
		t.Skip("set NANITE_LIVE_TEST=1 to use a configured live gateway")
	}
	// A direct provider key is for local testing only and needs its explicit switch.
	direct := os.Getenv("NANITE_PROVIDER_KEY_ENV") == "1"
	if !direct && (os.Getenv("NANITE_GATEWAY_URL") == "" || os.Getenv("NANITE_GATEWAY_KEY_REF") == "") {
		t.Fatal("set NANITE_GATEWAY_URL and NANITE_GATEWAY_KEY_REF, or NANITE_PROVIDER_KEY_ENV=1 with a provider key")
	}
	restoreConfig, err := useConfigFile(false)
	if err != nil {
		t.Fatal(err)
	}
	defer restoreConfig()
	dir := t.TempDir()
	if err := initAgent(dir); err != nil {
		t.Fatal(err)
	}
	switch name := os.Getenv("NANITE_LIVE_PROVIDER"); name {
	case "", "anthropic":
	case "openai":
		profile := "kind: stable\nlifetime: on-demand\nseat_id: local-agent\nruntime:\n  provider: openai\n"
		if err := os.WriteFile(filepath.Join(dir, "host.yaml"), []byte(profile), 0600); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unsupported NANITE_LIVE_PROVIDER %q", name)
	}
	const marker = "nanite-live-fixture-2874"
	if err := os.WriteFile(filepath.Join(dir, "fixture.txt"), []byte(marker), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	booted, err := start(ctx, filepath.Join(dir, ".nanite"))
	if err != nil {
		t.Fatal(err)
	}
	defer booted.Stop(context.WithoutCancel(ctx))
	root := newRoot(booted.App, dir)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"-p", "Call read_file exactly once with path ./fixture.txt, then repeat its content verbatim.", "--output", "stream-json"})
	if err := root.ExecuteContext(ctx); err != nil {
		t.Fatal(err)
	}
	var calls int
	var finished, readOK bool
	dec := json.NewDecoder(&out)
	for dec.More() {
		var event struct {
			Type  string
			Event struct {
				Name    string
				IsError bool
				Result  struct{ Text string }
			}
		}
		if err := dec.Decode(&event); err != nil {
			t.Fatal(err)
		}
		switch event.Type {
		case "ToolCallStarted":
			calls++
			if event.Event.Name != "read_file" {
				t.Fatalf("unexpected tool %q", event.Event.Name)
			}
		case "ToolCallFinished":
			readOK = !event.Event.IsError && strings.Contains(event.Event.Result.Text, marker)
		case "RunFinished":
			finished = strings.Contains(event.Event.Result.Text, marker)
		}
	}
	if calls != 1 || !readOK || !finished {
		t.Fatalf("calls=%d tool returned fixture=%t final reply contains fixture=%t", calls, readOK, finished)
	}
}
