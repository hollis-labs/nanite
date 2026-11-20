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
	// The starter writes a commented example variant, and validate accepts it.
	if strings.TrimSpace(out.String()) != "assistant\nvariants: reviewer" {
		t.Fatalf("validate output %q", out.String())
	}
	// So is the lead example in the docs, alongside the starter's reviewer.
	council, err := os.ReadFile(filepath.Join("..", "..", "examples", "variants", "council.md"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "variants", "council.md"), council, 0o600); err != nil { // #nosec G703 -- A path under the test's temporary directory.
		t.Fatal(err)
	}
	out.Reset()
	if err := root.ExecuteContext(ctx); err != nil || !strings.Contains(out.String(), "variants: council, reviewer") {
		t.Fatalf("validate with the council example: %q, %v", out.String(), err)
	}
	if err := os.Remove(filepath.Join(dir, "variants", "council.md")); err != nil {
		t.Fatal(err)
	}

	// Authored subtask variants are validated too.
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
	if err := os.Remove(filepath.Join(dir, "variants", "broken.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "variants", "remote.md"), []byte("---\nprovider: nowhere\nmodel: m\n---\nBody\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var warnings bytes.Buffer
	root.SetErr(&warnings)
	if err := root.ExecuteContext(ctx); err != nil || !strings.Contains(warnings.String(), `provider "nowhere" is not defined here; it must come from the embedding host`) {
		t.Fatalf("validate with a host-only variant provider: %v, warnings %q", err, warnings.String())
	}
	if err := os.Remove(filepath.Join(dir, "variants", "remote.md")); err != nil {
		t.Fatal(err)
	}

	// identity.yaml is validated, and auth warden needs its warden section.
	warden := "providers:\n  gateway:\n    wire: openai-chat\n    base_url: https://llm-gateway.example.test/v1\n    auth: warden\n    audience: llm-gateway\n"
	if err := os.WriteFile(filepath.Join(dir, "providers.yaml"), []byte(warden), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := root.ExecuteContext(ctx); err == nil || !strings.Contains(err.Error(), "identity.yaml has no warden section") {
		t.Fatalf("validate accepted auth warden without identity: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "identity.yaml"), []byte("principal: agent:a\nwarden:\n  url: https://warden.example.test\n  credential_file: "+filepath.Join(t.TempDir(), "agent.cred")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := root.ExecuteContext(ctx); err != nil {
		t.Fatalf("validate with identity.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "identity.yaml"), []byte("principal: agent:a\nwarden:\n  url: https://warden.example.test\n  credential_file: relative.cred\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := root.ExecuteContext(ctx); err == nil || !strings.Contains(err.Error(), "identity.yaml") {
		t.Fatalf("validate accepted a bad identity.yaml: %v", err)
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
