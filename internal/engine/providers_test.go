package engine

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hollis-labs/libs/agent/agentdir"
	"github.com/hollis-labs/nanite/internal/definition"
	"github.com/hollis-labs/nanite/internal/provider"
	"github.com/hollis-labs/nanite/internal/provider/fake"
)

// streamingCompatServer is an OpenAI-compatible server that streams one
// reply naming the server, and records the requested models.
func streamingCompatServer(t *testing.T, name string) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var models []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model  string `json:"model"`
			Stream bool   `json:"stream"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		mu.Lock()
		models = append(models, body.Model)
		mu.Unlock()
		if r.URL.Path != "/v1/chat/completions" {
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
			return
		}
		if !body.Stream {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"x","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"summary from `+name+`"},"finish_reason":"stop"}]}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range []string{
			`{"id":"x","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"hello from ` + name + `"},"finish_reason":""}]}`,
			`{"id":"x","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			`[DONE]`,
		} {
			_, _ = io.WriteString(w, "data: "+chunk+"\n\n")
		}
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), models...) }
}

func TestChatUsesNamedOpenAICompatibleProvider(t *testing.T) {
	srv, models := streamingCompatServer(t, "local")
	e, _, id := setupEngine(t, nil, "")
	e.cfg.Provider = nil
	file := "providers:\n  local:\n    wire: openai-chat\n    base_url: " + srv.URL + "/v1\n    auth: none\nuse:\n  chat: {provider: local, model: llama-3.1-8b}\n"
	if err := os.WriteFile(filepath.Join(e.cfg.Dir, "providers.yaml"), []byte(file), 0600); err != nil {
		t.Fatal(err)
	}
	run, err := e.Send(t.Context(), id, Input{Text: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := run.Wait()
	if err != nil || result.Text != "hello from local" {
		t.Fatalf("result %+v, %v", result, err)
	}
	if got := models(); len(got) != 1 || got[0] != "llama-3.1-8b" {
		t.Fatalf("models requested %v", got)
	}
}

func TestHostProviderOverridesFileEntry(t *testing.T) {
	fileSrv, fileModels := streamingCompatServer(t, "file")
	hostSrv, _ := streamingCompatServer(t, "host")
	e, _, id := setupEngine(t, nil, "")
	e.cfg.Provider = nil
	e.cfg.Providers = map[string]provider.Spec{"local": {Name: "local", Wire: provider.WireOpenAIChat, BaseURL: hostSrv.URL + "/v1", Auth: provider.AuthNone}}
	file := "providers:\n  local:\n    wire: openai-chat\n    base_url: " + fileSrv.URL + "/v1\n    auth: none\nuse:\n  chat: {provider: local, model: m}\n"
	if err := os.WriteFile(filepath.Join(e.cfg.Dir, "providers.yaml"), []byte(file), 0600); err != nil {
		t.Fatal(err)
	}
	if err := sendAndWaitText(t, e, id, "hello from host"); err != nil {
		t.Fatal(err)
	}
	if len(fileModels()) != 0 {
		t.Fatal("the file's endpoint was used")
	}
}

func sendAndWaitText(t *testing.T, e *Engine, id, want string) error {
	t.Helper()
	run, err := e.Send(t.Context(), id, Input{Text: "hi"})
	if err != nil {
		return err
	}
	result, err := run.Wait()
	if err == nil && !strings.Contains(result.Text, want) {
		t.Fatalf("result %q, want %q", result.Text, want)
	}
	return err
}

func TestSubagentUsesItsOwnProvider(t *testing.T) {
	srv, models := streamingCompatServer(t, "sub")
	spawn := subtaskCall("worker", "child work", false)
	// The parent runs on the host's injected fake provider; the child is
	// selected onto a provider only the host defines.
	e, db, id := setupEngine(t, fake.New(spawn, fake.Text("parent done")), "")
	e.cfg.Providers = map[string]provider.Spec{"sub": {Name: "sub", Wire: provider.WireOpenAIChat, BaseURL: srv.URL + "/v1", Auth: provider.AuthNone}}
	file := "use:\n  subagent: {provider: sub, model: small-model}\n"
	if err := os.WriteFile(filepath.Join(e.cfg.Dir, "providers.yaml"), []byte(file), 0600); err != nil {
		t.Fatal(err)
	}
	if err := sendAndWaitText(t, e, id, "parent done"); err != nil {
		t.Fatal(err)
	}
	if got := models(); len(got) != 1 || got[0] != "small-model" {
		t.Fatalf("subagent models %v", got)
	}
	history, err := db.History(t.Context(), id)
	if err != nil || len(history) < 3 || !strings.Contains(history[2].Blocks[0].Content, "hello from sub") {
		t.Fatalf("history %+v, %v", history, err)
	}
}

func TestSummarizerUsesItsOwnProvider(t *testing.T) {
	srv, models := streamingCompatServer(t, "summarizer")
	// Chat stays on the host's fake provider and overflows once, forcing a
	// compaction whose summary comes from the named provider.
	e, db, id := setupEngine(t, overflowThen(fake.Text("after compaction")), "")
	seedLongHistory(t, db, id)
	e.cfg.Providers = map[string]provider.Spec{"small": {Name: "small", Wire: provider.WireOpenAIChat, BaseURL: srv.URL + "/v1", Auth: provider.AuthNone}}
	file := "use:\n  summarizer: {provider: small, model: tiny-summarizer}\n"
	if err := os.WriteFile(filepath.Join(e.cfg.Dir, "providers.yaml"), []byte(file), 0600); err != nil {
		t.Fatal(err)
	}
	if err := sendAndWaitText(t, e, id, "after compaction"); err != nil {
		t.Fatal(err)
	}
	if got := models(); len(got) != 1 || got[0] != "tiny-summarizer" {
		t.Fatalf("summarizer models %v", got)
	}
	summary, _, _, err := db.ModelHistory(t.Context(), id)
	if err != nil || !strings.Contains(summary, "summary from summarizer") {
		t.Fatalf("summary %q, %v", summary, err)
	}
}

func TestInjectedProviderWinsOverFileProviders(t *testing.T) {
	srv, models := streamingCompatServer(t, "file")
	e, _, id := setupEngine(t, fake.New(fake.Text("from the host")), "")
	file := "providers:\n  local:\n    wire: openai-chat\n    base_url: " + srv.URL + "/v1\n    auth: none\nuse:\n  chat: {provider: local, model: m}\n"
	if err := os.WriteFile(filepath.Join(e.cfg.Dir, "providers.yaml"), []byte(file), 0600); err != nil {
		t.Fatal(err)
	}
	if err := sendAndWaitText(t, e, id, "from the host"); err != nil {
		t.Fatal(err)
	}
	if len(models()) != 0 {
		t.Fatal("providers.yaml routed traffic around the host's provider")
	}
}

func TestSubagentSummarizerFallsBackToTheSubagentSelection(t *testing.T) {
	e, _, _ := setupEngine(t, nil, "")
	e.cfg.Provider = nil
	e.cfg.Providers = map[string]provider.Spec{"sub": {Name: "sub", Wire: provider.WireOpenAIChat, BaseURL: "http://127.0.0.1:1/v1", Auth: provider.AuthNone}}
	def := definition.Definition{Runtime: agentdir.Runtime{Provider: "anthropic", Model: "claude-sonnet-5-5"},
		Use: map[string]definition.Selection{definition.UseSubagent: {Provider: "sub", Model: "small-model"}}}
	_, model, err := e.provider(def, definition.UseSubagent)
	if err != nil || model != "small-model" {
		t.Fatalf("subagent selection %q, %v", model, err)
	}
	if _, _, err := e.provider(definition.Definition{Use: map[string]definition.Selection{definition.UseChat: {Provider: "nowhere", Model: "m"}}}, definition.UseChat); err == nil || !strings.Contains(err.Error(), "unknown provider") {
		t.Fatalf("unknown provider: %v", err)
	}
}
