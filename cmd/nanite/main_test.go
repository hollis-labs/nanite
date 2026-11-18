package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRootHasCommands(t *testing.T) {
	ctx := context.Background()
	run, err := start(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = run.Stop(ctx) }()
	dir := filepath.Join(t.TempDir(), "agent")
	root := newRoot(run.App, dir)
	for _, name := range []string{"version", "init", "validate", "conversations"} {
		if c, _, err := root.Find([]string{name}); err != nil || c == root {
			t.Errorf("command %q missing: %v", name, err)
		}
	}
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"init"})
	if err := root.ExecuteContext(ctx); err != nil {
		t.Fatal(err)
	}
	root.SetArgs([]string{"validate"})
	if err := root.ExecuteContext(ctx); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "local-agent" {
		t.Fatalf("validate output %q", out.String())
	}
}

func TestPromptViaLocalGateway(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	if err := initAgent(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "host.yaml"), []byte("kind: stable\nlifetime: on-demand\nseat_id: local-agent\nruntime:\n  provider: openai\n  model: test-model\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var called atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Store(true)
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("X-Station-Session") == "" {
			t.Errorf("unexpected gateway request: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintln(w, `data: {"id":"fixture","object":"chat.completion.chunk","created":1,"model":"test-model","choices":[{"index":0,"delta":{"content":"hello from local gateway"},"finish_reason":null}]}`)
		_, _ = fmt.Fprintln(w)
		_, _ = fmt.Fprintln(w, `data: {"id":"fixture","object":"chat.completion.chunk","created":1,"model":"test-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
		_, _ = fmt.Fprintln(w)
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	key := filepath.Join(t.TempDir(), "mock-key")
	if err := os.WriteFile(key, []byte("unit-test-key"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NANITE_GATEWAY_URL", server.URL+"/v1")
	resolved, err := filepath.EvalSymlinks(key)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("NANITE_GATEWAY_KEY_REF", "file://"+resolved)
	started, err := start(ctx, filepath.Join(dir, ".nanite"))
	if err != nil {
		t.Fatal(err)
	}
	defer started.Stop(ctx)
	root := newRoot(started.App, dir)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"-p", "hello"})
	if err := root.ExecuteContext(ctx); err != nil {
		t.Fatal(err)
	}
	if !called.Load() || out.String() != "hello from local gateway\n" {
		t.Fatalf("output %q called=%v", out.String(), called.Load())
	}
}
