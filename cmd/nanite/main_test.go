package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/hollis-labs/libs/harness/core/adapters/runtimeevents"
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
	if strings.TrimSpace(out.String()) != "assistant" {
		t.Fatalf("validate output %q", out.String())
	}

	// Authored subtask variants are validated too.
	if err := os.MkdirAll(filepath.Join(dir, "variants"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "variants", "lead.md"), []byte("---\ncan_spawn: true\n---\nYou lead.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := root.ExecuteContext(ctx); err != nil || !strings.Contains(out.String(), "variants: lead") {
		t.Fatalf("validate with a variant: %q, %v", out.String(), err)
	}
	if err := os.WriteFile(filepath.Join(dir, "variants", "broken.md"), []byte("---\ncan_spawm: true\n---\nBody\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := root.ExecuteContext(ctx); err == nil || !strings.Contains(err.Error(), "variants/broken.md") {
		t.Fatalf("validate accepted a bad variant: %v", err)
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
	eventPath := filepath.Join(dir, ".nanite", "events.jsonl")
	info, err := os.Stat(eventPath)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("event log permissions: %v %v", info, err)
	}
	raw, err := os.ReadFile(eventPath) // #nosec G304 -- Reads the event fixture under the temporary agent directory.
	if err != nil {
		t.Fatal(err)
	}
	var last uint64
	kinds := make(map[runtimeevents.EventKind]bool)
	for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte("\n")) {
		var event runtimeevents.Event
		if err := json.Unmarshal(line, &event); err != nil {
			t.Fatal(err)
		}
		if err := event.Validate(); err != nil {
			t.Fatal(err)
		}
		if event.Sequence != last+1 {
			t.Fatalf("runtime sequence = %d, preceding %d", event.Sequence, last)
		}
		last = event.Sequence
		kinds[event.Kind] = true
	}
	for _, kind := range []runtimeevents.EventKind{runtimeevents.KindSessionReady, runtimeevents.KindSessionProcessing,
		runtimeevents.KindTurnStarted, runtimeevents.KindAgentDelta, runtimeevents.KindTurnCompleted, runtimeevents.KindSessionIdle} {
		if !kinds[kind] {
			t.Errorf("missing %s", kind)
		}
	}

}
