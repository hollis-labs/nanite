package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/libs/agent/agentdir"
	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/provider/fake"
	"github.com/hollis-labs/nanite/internal/provider/station"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/tools"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func profileFixture(t *testing.T, label string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "agent.md"), []byte(strings.ReplaceAll(fixtureAgent, "Engine test instructions.", label)), 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}
func sendProfile(t *testing.T, e *Engine, id string, in Input) {
	t.Helper()
	r, err := e.Send(t.Context(), id, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestTurnProfileSwitchAndValidation(t *testing.T) {
	p := &watchedProvider{Provider: fake.New(fake.Text("a"), fake.Text("b"), fake.Text("base"))}
	e, db, id := setupEngine(t, p, "")
	a, b := profileFixture(t, "PROFILE ALPHA"), profileFixture(t, "PROFILE BETA")
	sendProfile(t, e, id, Input{Text: "one", AgentDir: a})
	sendProfile(t, e, id, Input{Text: "two", AgentDir: b})
	sendProfile(t, e, id, Input{Text: "three"})
	for i, want := range []string{"PROFILE ALPHA", "PROFILE BETA", "Engine test instructions."} {
		if !strings.Contains(systemText(p.requests[i]), want) {
			t.Fatalf("request %d missed %s", i, want)
		}
	}
	for _, name := range []string{"host.yaml", "providers.yaml", "identity.yaml", "permissions.yaml", ".mcp.json"} {
		dir := profileFixture(t, "invalid")
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0600); err != nil {
			t.Fatal(err)
		}
		if r, err := e.Send(t.Context(), id, Input{Text: "bad", AgentDir: dir}); err == nil || r != nil || !strings.Contains(err.Error(), "host-owned") {
			t.Fatalf("%s: run=%v err=%v", name, r, err)
		}
	}
	for _, header := range []string{"Authorization", "cookie", "HOST", "Content-Type", "Transfer-Encoding", "Connection", "Proxy-Authorization", "X-Forwarded-For", "X-Nanite-Principal", "X-Station-Session", "X-Run-Token", "Mcp-Session-Id"} {
		if r, err := e.Send(t.Context(), id, Input{Text: "bad", Headers: map[string]string{header: "value"}}); err == nil || r != nil {
			t.Fatalf("accepted %s", header)
		}
	}
	h, err := db.History(t.Context(), id)
	if err != nil || len(h) != 6 {
		t.Fatalf("invalid turn changed history: %d %v", len(h), err)
	}
}

func TestTurnProfileSteeringAndPreparedDigest(t *testing.T) {
	e, _, id := setupEngine(t, fake.New(fake.Text("should not run")), "")
	dir := profileFixture(t, "profile")
	raw := `{"presets":[{"name":"careful","reflexes":["halt"]}],"reflexes":[{"name":"halt","trigger_kind":"text","trigger":{"pattern":"."},"action_kind":"halt","action":{"message":"profile answer"}}]}`
	if err := os.WriteFile(filepath.Join(dir, "steering.json"), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	prepared, err := e.Steer(t.Context(), id, Input{Text: "hi", AgentDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if !prepared.Halted() {
		t.Fatal("profile reflex did not fire")
	}
	if err := e.SetMode(t.Context(), id, "careful"); err != nil {
		t.Fatal(err)
	}
	r, err := e.Send(t.Context(), id, Input{Text: "hi", AgentDir: dir, Steer: prepared})
	if err != nil {
		t.Fatal(err)
	}
	result, err := r.Wait()
	if err != nil || result.Text != "profile answer" {
		t.Fatalf("%+v %v", result, err)
	}
	prepared, err = e.Steer(t.Context(), id, Input{Text: "again", AgentDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent.md"), []byte(fixtureAgent+"changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Send(t.Context(), id, Input{Text: "again", AgentDir: dir, Steer: prepared}); err == nil {
		t.Fatal("accepted stale prepared steering")
	}
}

func TestBackgroundSubtaskKeepsProfileAfterSwitch(t *testing.T) {
	childReady, release := make(chan struct{}), make(chan struct{})
	p := &routedProvider{root: fake.New(subtaskCall("lead", "lead", true), fake.Text("started"), fake.Text("switched")), children: map[string]*fake.Provider{
		"lead":   fake.New(toolResponse("wait_profile"), subtaskCall("helper", "helper", false), returnCall("ok", "done")),
		"helper": fake.New(returnCall("ok", "helper done")),
	}}
	var mcpMu sync.Mutex
	var callHeaders []string
	server := sdk.NewServer(&sdk.Implementation{Name: "background", Version: "1"}, nil)
	sdk.AddTool(server, &sdk.Tool{Name: "echo"}, func(context.Context, *sdk.CallToolRequest, struct{}) (*sdk.CallToolResult, any, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "ok"}}}, nil, nil
	})
	handler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mcpMu.Lock()
		if r.Method == http.MethodPost && r.Header.Get("X-Profile") != "" {
			callHeaders = append(callHeaders, r.Header.Get("X-Profile"))
		}
		if r.Method != http.MethodPost && r.Header.Get("X-Profile") != "" {
			t.Error("background headers leaked onto connection request")
		}
		mcpMu.Unlock()
		handler.ServeHTTP(w, r)
	}))
	defer srv.Close()
	e, _, id := setupEngine(t, p, "")
	defer e.mcp.Close()
	a, b := profileFixture(t, "PROFILE ALPHA"), profileFixture(t, "PROFILE BETA")
	writeVariant(t, a, "lead", "---\ncan_spawn: true\n---\nLEAD ALPHA")
	writeVariant(t, a, "helper", "HELPER ALPHA")
	writeVariant(t, b, "helper", "HELPER BETA")
	if err := e.cfg.Tools.SetGroup("wait", tools.Tool{Name: "wait_profile", ReadOnly: true, InputSchema: json.RawMessage(`{"type":"object"}`), Handler: func(ctx context.Context, _ tools.Call) (tools.Result, error) {
		if station.Headers(ctx)["X-Profile"] != "alpha" {
			t.Error("child lost parent headers")
		}
		close(childReady)
		select {
		case <-release:
		case <-ctx.Done():
			return tools.Result{}, ctx.Err()
		}
		lease, warnings := e.mcp.Reconcile(ctx, agentdir.MCPConfig{Servers: map[string]agentdir.MCPServer{"fixture": {Type: "http", URL: srv.URL}}})
		defer lease.Close()
		if len(warnings) > 0 {
			t.Errorf("MCP reconcile: %v", warnings)
		}
		offered, warnings := lease.Tools(ctx)
		if len(warnings) > 0 || len(offered) != 1 {
			t.Errorf("MCP discovery: %v", warnings)
			return tools.Result{Text: "unavailable"}, nil
		}
		if _, err := offered[0].Handler(ctx, tools.Call{Input: json.RawMessage(`{}`)}); err != nil {
			return tools.Result{}, err
		}
		return tools.Result{Text: "released"}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	sendProfile(t, e, id, Input{Text: "start", AgentDir: a, Headers: map[string]string{"X-Profile": "alpha"}})
	select {
	case <-childReady:
	case <-time.After(5 * time.Second):
		t.Fatal("child did not start")
	}
	sendProfile(t, e, id, Input{Text: "switch", AgentDir: b, Headers: map[string]string{"X-Profile": "beta"}})
	// Change the old directory too: a descendant must use the captured variants.
	writeVariant(t, a, "helper", "HELPER MUTATED")
	close(release)
	select {
	case <-e.Notices():
	case <-time.After(5 * time.Second):
		t.Fatal("background child did not finish")
	}
	mcpMu.Lock()
	if len(callHeaders) < 2 {
		t.Errorf("background MCP calls missing: %v", callHeaders)
	}
	for _, header := range callHeaders {
		if header != "alpha" {
			t.Errorf("background MCP profile=%q", header)
		}
	}
	mcpMu.Unlock()
	p.mu.Lock()
	defer p.mu.Unlock()
	reqs := p.requests["helper"]
	if len(reqs) == 0 || !strings.Contains(systemText(reqs[0]), "HELPER ALPHA") || strings.Contains(systemText(reqs[0]), "HELPER MUTATED") {
		t.Fatalf("helper did not keep original variant: %+v", reqs)
	}
}

func TestResumeUsesPersistedTurnProfile(t *testing.T) {
	p := &watchedProvider{Provider: fake.New(fake.Text("resumed"))}
	e, db, id := setupEngine(t, p, "")
	dir := profileFixture(t, "RECOVERED PROFILE")
	now := time.Now().UTC()
	turn := store.Turn{ID: store.NewID(now), UserMessageID: store.NewID(now), AgentDir: dir, Headers: map[string]string{"X-Profile": "recovered"}}
	j, err := db.OpenJournal(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if err = j.StartTurn(turn, now); err != nil {
		t.Fatal(err)
	}
	if err = j.AppendMessage(store.Message{ID: turn.UserMessageID, TurnID: turn.ID, Role: "user", CreatedAt: now, Blocks: []llmtypes.ContentBlock{{Type: "text", Text: "continue"}}}, nil); err != nil {
		t.Fatal(err)
	}
	if err = j.InterruptTurn(turn.ID, now); err != nil {
		t.Fatal(err)
	}
	if err = j.Close(); err != nil {
		t.Fatal(err)
	}
	held, err := db.Interrupted(t.Context(), id)
	if err != nil || held.AgentDir != dir || held.Headers["X-Profile"] != "recovered" {
		t.Fatalf("persisted: %+v %v", held, err)
	}
	// A new engine has no cached profile and must hydrate the stored selection.
	fresh, err := New(e.cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	r, err := fresh.Resume(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.Wait(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(systemText(p.requests[0]), "RECOVERED PROFILE") {
		t.Fatal("resumed default profile")
	}
}

func TestProfileSwitchPreservesConversationSteering(t *testing.T) {
	p := &watchedProvider{Provider: fake.New(fake.Text("a"), fake.Text("b"))}
	e, _, id := setupEngine(t, p, "")
	steeringFor(t, e, nil, nil)
	if err := e.SetMode(t.Context(), id, "focus"); err != nil {
		t.Fatal(err)
	}
	original, err := e.rootSteerer(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"a", "b"} {
		dir := profileFixture(t, label)
		if err := os.WriteFile(filepath.Join(dir, "steering.json"), []byte(`{"presets":[{"name":"other"}]}`), 0600); err != nil {
			t.Fatal(err)
		}
		sendProfile(t, e, id, Input{Text: label, AgentDir: dir})
		if mode, err := e.Mode(t.Context(), id); err != nil || mode != "focus" {
			t.Fatalf("mode=%q err=%v", mode, err)
		}
		current, err := e.rootSteerer(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if current.steer.Selector() != original.steer.Selector() {
			t.Fatal("profile switch replaced conversation selector")
		}
	}
}

func TestProfileContextRejectsAnotherAgentsSnapshot(t *testing.T) {
	e, _, _ := setupEngine(t, fake.New(), "")
	other, _, id := setupEngine(t, fake.New(), "")
	ctx, err := e.TurnContext(t.Context(), Input{AgentDir: profileFixture(t, "original")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Send(ctx, id, Input{Text: "wrong agent"}); err == nil {
		t.Fatal("accepted another agent's profile")
	}
}

func TestProfileRulesOverrideDefaultsWithoutChangingMode(t *testing.T) {
	p := &watchedProvider{Provider: fake.New(fake.Text("done"))}
	e, _, id := setupEngine(t, p, "")
	steeringFor(t, e, nil, nil)
	if err := e.SetMode(t.Context(), id, "general"); err != nil {
		t.Fatal(err)
	}
	dir := profileFixture(t, "profile")
	if err := os.WriteFile(filepath.Join(dir, "steering.json"), []byte(`{"presets":[{"name":"general","instructions":"PROFILE FOCUS RULES"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	sendProfile(t, e, id, Input{Text: "hello", AgentDir: dir})
	if !strings.Contains(systemText(p.requests[0]), "PROFILE FOCUS RULES") {
		t.Fatal("profile rules did not apply to conversation mode")
	}
}

func TestTurnHeaderRedactionRefusesBeforeStorage(t *testing.T) {
	e, db, id := setupEngine(t, fake.New(), "")
	value := "registered-" + "sensitive-fixture-value"
	e.cfg.ProviderConfig.Redactor.Add("fixture", value)
	for _, inherited := range []bool{false, true} {
		ctx := t.Context()
		in := Input{Text: "refused", Headers: map[string]string{"X-Profile": value}}
		if inherited {
			ctx = station.WithHeaders(ctx, in.Headers)
			in.Headers = nil
		}
		if run, err := e.Send(ctx, id, in); err == nil || run != nil || !strings.Contains(err.Error(), "X-Profile") || strings.Contains(err.Error(), value) {
			t.Fatalf("run=%v err=%v", run, err)
		}
	}
	history, err := db.History(t.Context(), id)
	if err != nil || len(history) != 0 {
		t.Fatalf("refused headers changed history: %v %v", history, err)
	}
	if held, err := db.Interrupted(t.Context(), id); err != nil || held != nil {
		t.Fatalf("refused headers created turn: %v %v", held, err)
	}
}

func TestProfileToolPackagesCannotRequestHostCredentials(t *testing.T) {
	e, _, id := setupEngine(t, fake.New(), "")
	for _, extra := range []string{"env: [PROFILE_FIXTURE_ENV]\n", "needs:\n  - audience: fixture\n    scopes: [read]\n    env: FIXTURE_CREDENTIAL\n"} {
		dir := profileFixture(t, "profile")
		writeToolPackage(t, dir, "echo", echoManifest+extra, "#!/bin/sh\ncat\n")
		if run, err := e.Send(t.Context(), id, Input{Text: "no", AgentDir: dir}); err == nil || run != nil || !strings.Contains(err.Error(), "cannot request") {
			t.Fatalf("run=%v err=%v", run, err)
		}
	}
}

type profileHeaderProvider struct {
	*watchedProvider
	headers map[string]string
}

func (p *profileHeaderProvider) StreamChat(ctx context.Context, req llmtypes.ChatRequest) (<-chan llmtypes.StreamEvent, error) {
	p.headers = station.Headers(ctx)
	return p.watchedProvider.StreamChat(ctx, req)
}

func TestResumeDoesNotInheritCallerProfile(t *testing.T) {
	for _, stored := range []bool{false, true} {
		name := "empty"
		if stored {
			name = "stored"
		}
		t.Run(name, func(t *testing.T) {
			p := &profileHeaderProvider{watchedProvider: &watchedProvider{Provider: fake.New(fake.Text("resumed"))}}
			e, db, id := setupEngine(t, p, "")
			now := time.Now().UTC()
			turn := store.Turn{ID: store.NewID(now), UserMessageID: store.NewID(now)}
			want := "Engine test instructions."
			if stored {
				turn.AgentDir = profileFixture(t, "STORED PROFILE")
				turn.Headers = map[string]string{"X-Profile": "stored"}
				want = "STORED PROFILE"
			}
			j, err := db.OpenJournal(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			if err := j.StartTurn(turn, now); err != nil {
				t.Fatal(err)
			}
			if err := j.AppendMessage(store.Message{ID: turn.UserMessageID, TurnID: turn.ID, Role: "user", CreatedAt: now, Blocks: []llmtypes.ContentBlock{{Type: "text", Text: "continue"}}}, nil); err != nil {
				t.Fatal(err)
			}
			if err := j.InterruptTurn(turn.ID, now); err != nil {
				t.Fatal(err)
			}
			if err := j.Close(); err != nil {
				t.Fatal(err)
			}
			ctx, err := e.TurnContext(t.Context(), Input{AgentDir: profileFixture(t, "CALLER PROFILE"), Headers: map[string]string{"X-Profile": "caller"}})
			if err != nil {
				t.Fatal(err)
			}
			run, err := e.Resume(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := run.Wait(); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(systemText(p.requests[0]), want) || strings.Contains(systemText(p.requests[0]), "CALLER PROFILE") {
				t.Fatal("resume inherited caller profile")
			}
			if p.headers["X-Profile"] != turn.Headers["X-Profile"] {
				t.Fatalf("headers=%v want %v", p.headers, turn.Headers)
			}
		})
	}
}
