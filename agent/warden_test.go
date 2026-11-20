package agent_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hollis-labs/libs/harness/core/adapters/runtimeevents"
	"github.com/hollis-labs/libs/security/secrets/tokenfile"
	"github.com/hollis-labs/nanite/agent"
)

// A turn through a provider with Warden auth carries the token on the wire
// and nowhere else: not in logs, runtime events, the run's events, traces or
// the stored conversation.
func TestWardenTokenStaysOnTheWire(t *testing.T) {
	minted := fixtureValue("access")
	nextParent := fixtureValue("next-parent")
	var mu sync.Mutex
	var mints int
	ws := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		_ = r.ParseForm()
		mu.Lock()
		mints++
		mu.Unlock()
		if r.Form.Get("refresh_token") != "warden-parent-fixture-0" || r.Form.Get("audience") != "llm-gateway" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":"invalid_grant"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"access_token":%q,"expires_in":900,"refresh_token":%q}`, minted, nextParent)
	}))
	t.Cleanup(ws.Close)
	var auth string
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auth = r.Header.Get("Authorization")
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range []string{
			`{"id":"x","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"hello through the gateway"},"finish_reason":""}]}`,
			`{"id":"x","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			`[DONE]`,
		} {
			_, _ = io.WriteString(w, "data: "+chunk+"\n\n")
		}
	}))
	t.Cleanup(llm.Close)

	root := t.TempDir()
	cred := filepath.Join(t.TempDir(), "agent.cred")
	if err := tokenfile.Write(cred, "warden-parent-fixture-0"); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"agent.md":       "---\nschema_version: \"2\"\ndefinition_id: def:warden-test\nrevision: \"1\"\nname: warden-test\ndescription: Warden test.\nbehavior:\n  purpose: Test.\nrequirements: {}\nharness_profile:\n  permissions:\n    profile: default\ncontinuity:\n  mode: durable\n---\nTest.\n",
		"host.yaml":      "kind: stable\nlifetime: on-demand\nseat_id: warden-test\nruntime:\n  provider: anthropic\n",
		"providers.yaml": "providers:\n  gateway:\n    wire: openai-chat\n    base_url: " + llm.URL + "/v1\n    auth: warden\n    audience: llm-gateway\n    scopes: [llm-gateway.chat]\nuse:\n  chat: {provider: gateway, model: m}\n",
		"identity.yaml":  "principal: agent:warden-test\nwarden:\n  url: " + ws.URL + "\n  credential_file: " + cred + "\n",
	}
	for name, raw := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var logs bytes.Buffer
	var logMu sync.Mutex
	logger := slog.New(slog.NewJSONHandler(lockedWriter{&logs, &logMu}, &slog.HandlerOptions{Level: slog.LevelDebug}))
	var sinkMu sync.Mutex
	var sunk []string
	sink := runtimeevents.SinkFunc(func(_ context.Context, ev runtimeevents.Event) error {
		raw, _ := json.Marshal(ev)
		sinkMu.Lock()
		sunk = append(sunk, string(raw))
		sinkMu.Unlock()
		return nil
	})
	a, err := agent.Open(t.Context(), agent.Options{Dir: root, GlobalSkillsDir: "-", Logger: logger, Events: sink, TraceFiles: true})
	if err != nil {
		t.Fatal(err)
	}
	c, err := a.NewConversation(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	run, err := c.Send(t.Context(), agent.Input{Text: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	var runEvents []string
	for ev := range run.Events() {
		raw, _ := json.Marshal(ev)
		runEvents = append(runEvents, fmt.Sprintf("%T %s", ev, raw))
	}
	result, err := run.Wait()
	if err != nil || result.Text != "hello through the gateway" {
		t.Fatalf("result %q, %v", result.Text, err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if auth != "Bearer "+minted || mints != 1 {
		t.Fatalf("Authorization %q after %d mints", auth, mints)
	}
	mu.Unlock()
	if got, err := tokenfile.Read(cred); err != nil || got != nextParent {
		t.Fatalf("parent after the turn = %q, %v", got, err)
	}

	haystacks := map[string]string{"logs": logs.String(), "runtime events": strings.Join(sunk, "\n"), "run events": strings.Join(runEvents, "\n")}
	dirRoot, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dirRoot.Close() }()
	err = fs.WalkDir(dirRoot.FS(), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := dirRoot.ReadFile(path)
		if err != nil {
			return err
		}
		haystacks[path] = string(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	traces := 0
	for name, text := range haystacks {
		if strings.Contains(name, "traces") {
			traces++
		}
		for _, secret := range []string{minted, nextParent, "warden-parent-fixture-0"} {
			if strings.Contains(text, secret) {
				t.Errorf("%s carries a Warden secret", name)
			}
		}
	}
	if traces == 0 || len(sunk) == 0 {
		t.Fatalf("checked %d trace files and %d runtime events; want some of each", traces, len(sunk))
	}
}

type lockedWriter struct {
	b  *bytes.Buffer
	mu *sync.Mutex
}

func (w lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

// fixtureValue is a recognizable stand-in for a credential, built at run
// time so the source holds no credential-shaped literal.
func fixtureValue(kind string) string { return "fixture-" + kind + "-7f3a91c2" }
