package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/libs/llm/core/llmcontracts"
	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/provider/failure"
	"github.com/hollis-labs/nanite/internal/provider/fake"
	"github.com/hollis-labs/nanite/internal/skills"
	"github.com/hollis-labs/nanite/internal/slots"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const fixtureAgent = `---
schema_version: "2"
definition_id: def:engine-fixture
revision: "1"
name: engine-fixture
description: Engine test fixture.
behavior:
  purpose: Exercise turn behavior.
requirements: {}
harness_profile:
  permissions:
    profile: default
continuity:
  mode: ephemeral
---
Engine test instructions.
`

func setupEngine(t *testing.T, model llmcontracts.Provider, runtime string) (*Engine, *store.Store, string) {
	t.Helper()
	root := t.TempDir()
	for name, body := range map[string]string{"agent.md": fixtureAgent,
		"host.yaml": "kind: embedded\nlifetime: application\napplication_id: fixture\n" + runtime,
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	db, err := store.Open(t.Context(), filepath.Join(root, ".nanite"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	e, err := New(Config{Dir: root, ToolApprovalDir: filepath.Join(t.TempDir(), "tool-approvals"), GlobalSkillsDir: "-", Provider: model, Store: db, Slots: slots.NewStore(), Tools: &tools.Groups{}, Skills: &skills.Groups{}})
	if err != nil {
		t.Fatal(err)
	}
	subscribeForTest(e)
	t.Cleanup(e.Close)
	now := time.Now().UTC()
	id := store.NewID(now)
	if err := db.CreateConversation(t.Context(), store.Conversation{ID: id, Role: "chat", Status: "active", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	return e, db, id
}

func toolResponse(name string) fake.Response {
	return fake.Response{Events: []llmtypes.StreamEvent{
		{Type: llmtypes.EventToolUse, ToolUse: &llmtypes.ToolUseBlock{ID: "call-1", Name: name}},
		{Type: llmtypes.EventDone},
	}}
}

func TestToolResultPairingAndStepLimit(t *testing.T) {
	for _, tc := range []struct {
		name, runtime, stop string
		steps               int
	}{
		{"continue after unknown tool", "", "end_turn", 2},
		{"max steps", "runtime:\n  max_steps: 1\n", "max_steps", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, db, id := setupEngine(t, fake.New(toolResponse("unknown"), fake.Text("continued")), tc.runtime)
			run, err := e.Send(t.Context(), id, Input{Text: "go"})
			if err != nil {
				t.Fatal(err)
			}
			result, err := run.Wait()
			if err != nil || result.Steps != tc.steps || result.StopReason != tc.stop {
				t.Fatalf("result %+v, %v", result, err)
			}
			h, err := db.History(t.Context(), id)
			if err != nil || len(h) != tc.steps+2 || !h[2].Blocks[0].IsError || h[2].Blocks[0].ToolUseID != h[1].Blocks[0].ID ||
				!strings.Contains(h[2].Blocks[0].Content, "unavailable") {
				t.Fatalf("history %+v, %v", h, err)
			}
		})
	}
}

func TestCancelDuringToolAndBusy(t *testing.T) {
	e, db, id := setupEngine(t, fake.New(toolResponse("wait_tool")), "")
	started := make(chan struct{})
	if err := e.cfg.Tools.SetGroup("test", tools.Tool{
		Name: "wait_tool", InputSchema: json.RawMessage(`{"type":"object"}`),
		Handler: func(ctx context.Context, _ tools.Call) (tools.Result, error) {
			close(started)
			<-ctx.Done()
			return tools.Result{}, ctx.Err()
		},
	}); err != nil {
		t.Fatal(err)
	}
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not start")
	}
	if _, err := e.Send(t.Context(), id, Input{Text: "overlap"}); !errors.Is(err, ErrBusy) {
		t.Fatalf("overlap: %v", err)
	}
	run.Cancel()
	if _, err := run.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait: %v", err)
	}
	h, err := db.History(t.Context(), id)
	if err != nil || len(h) != 3 || h[2].Blocks[0].ToolUseID != "call-1" || !h[2].Blocks[0].IsError {
		t.Fatalf("unpaired tool use after cancellation: %+v, %v", h, err)
	}
}

func TestPartialStreamFailure(t *testing.T) {
	p := fake.New(fake.Response{Events: []llmtypes.StreamEvent{{Type: llmtypes.EventDelta, Content: "partial"}, {Type: llmtypes.EventError, Error: "wire broke"}}})
	e, db, id := setupEngine(t, p, "")
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := run.Wait()
	if err == nil || got.Text != "partial" {
		t.Fatalf("Wait = %+v, %v", got, err)
	}
	h, err := db.History(t.Context(), id)
	if err != nil || len(h) != 2 || h[1].Blocks[0].Text != "partial" {
		t.Fatalf("history %+v, %v", h, err)
	}
	records, err := db.TurnStatus(t.Context(), run.ID())
	if err != nil || records != "failed" {
		t.Fatalf("turn status %q, %v", records, err)
	}
}

type watchedProvider struct {
	llmcontracts.Provider
	requests  []llmtypes.ChatRequest
	summaries int
}

func (p *watchedProvider) StreamChat(ctx context.Context, req llmtypes.ChatRequest) (<-chan llmtypes.StreamEvent, error) {
	req.Messages = append([]llmtypes.ChatMessage(nil), req.Messages...)
	p.requests = append(p.requests, req)
	return p.Provider.StreamChat(ctx, req)
}

func (p *watchedProvider) Complete(context.Context, llmtypes.ChatRequest) (string, error) {
	p.summaries++
	return "durable context", nil
}

func TestCompactionSurvivesNextTurn(t *testing.T) {
	p := &watchedProvider{Provider: fake.New(fake.Response{Err: &failure.Error{Class: failure.Overflow, Cause: errors.New("typed overflow")}}, fake.Text("after compaction"), fake.Text("second turn"))}
	e, db, id := setupEngine(t, p, "")
	j, err := db.OpenJournal(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	seed := store.Turn{ID: store.NewID(now), UserMessageID: store.NewID(now)}
	if err := j.StartTurn(seed, now); err != nil {
		t.Fatal(err)
	}
	for i := range 6 {
		role := "user"
		if i%2 != 0 {
			role = "assistant"
		}
		msg := store.Message{ID: store.NewID(now), TurnID: seed.ID, Role: role, CreatedAt: now, Blocks: []llmtypes.ContentBlock{{Type: "text", Text: strings.Repeat("older words ", 100)}}}
		if err := j.AppendMessage(msg, nil); err != nil {
			t.Fatal(err)
		}
	}
	seed.Status = "completed"
	if err := j.FinishTurn(seed, now); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"first turn", "second turn"} {
		run, err := e.Send(t.Context(), id, Input{Text: text})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := run.Wait(); err != nil {
			t.Fatal(err)
		}
		if err := db.RebuildIndex(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if len(p.requests) != 3 || p.summaries != 1 {
		t.Fatalf("requests %d, summaries %d", len(p.requests), p.summaries)
	}
	if first := p.requests[2].Messages[0].Content; !strings.Contains(first, "durable context") {
		t.Fatalf("second turn started with %q", first)
	}
	h, err := db.History(t.Context(), id)
	if err != nil || len(h) != 10 {
		t.Fatalf("raw history length %d, %v", len(h), err)
	}
}

func TestThinkingReplayPreservesOnlyValidSignatures(t *testing.T) {
	const key = "fixture-secret-value"
	p := &watchedProvider{Provider: fake.New(fake.Response{Events: []llmtypes.StreamEvent{
		{Type: llmtypes.EventThinking, ThinkingBlock: &llmtypes.ThinkingBlock{Thinking: "safe thought", Signature: "signed-safe"}},
		{Type: llmtypes.EventThinking, ThinkingBlock: &llmtypes.ThinkingBlock{Thinking: "unsafe " + key, Signature: "signed-secret"}},
		{Type: llmtypes.EventDelta, Content: "hello"}, {Type: llmtypes.EventDone},
	}}, fake.Text("again"))}
	e, db, id := setupEngine(t, p, "")
	e.cfg.ProviderConfig.Redactor.Add("fixture", key)
	for range 2 {
		run, err := e.Send(t.Context(), id, Input{Text: "go"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := run.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	replay := p.requests[1].Messages[1].ContentBlocks
	if len(replay) != 2 || replay[0].Signature != "signed-safe" || replay[0].Text != "safe thought" {
		t.Fatalf("replay %+v", replay)
	}
	h, err := db.History(t.Context(), id)
	if err != nil || len(h[1].Blocks) != 3 || strings.Contains(h[1].Blocks[1].Text, key) || h[1].Blocks[1].Signature != "" {
		t.Fatalf("redacted thinking missing from raw history, error %v", err)
	}
}

func TestSkillEditTakesEffectOnNextTurn(t *testing.T) {
	call := func(id string) fake.Response {
		return fake.Response{Events: []llmtypes.StreamEvent{
			{Type: llmtypes.EventToolUse, ToolUse: &llmtypes.ToolUseBlock{ID: id, Name: "skill_get", Input: map[string]any{"name": "fixture"}}},
			{Type: llmtypes.EventDone},
		}}
	}
	p := &watchedProvider{Provider: fake.New(call("one"), fake.Text("first"), call("two"), fake.Text("second"))}
	e, db, id := setupEngine(t, p, "")
	dir := filepath.Join(e.cfg.Dir, "skills", "fixture")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"old skill body", "new skill body"} {
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: fixture\ndescription: load fixture\n---\n"+body), 0600); err != nil {
			t.Fatal(err)
		}
		run, err := e.Send(t.Context(), id, Input{Text: "read the skill"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := run.Wait(); err != nil {
			t.Fatal(err)
		}
		history, err := db.History(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		result := history[len(history)-2].Blocks[0].Content
		if !strings.Contains(result, body) || !strings.Contains(result, dir) {
			t.Fatalf("skill result %q", result)
		}
	}
	for _, req := range p.requests {
		for _, slot := range req.SlotBlocks {
			if slot.Name == "skills" && (strings.Contains(slot.Content, "skill body") || !strings.Contains(slot.Content, "fixture: load fixture")) {
				t.Fatalf("skills slot leaked body or lost listing: %q", slot.Content)
			}
		}
	}
}

func TestMCPConfigurationReloadAndWarnings(t *testing.T) {
	p := &watchedProvider{Provider: fake.New(fake.Text("first"), fake.Text("second"), fake.Text("third"))}
	e, _, id := setupEngine(t, p, "")
	server := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "ping", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}},
		func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pong"}}}, nil, nil
		})
	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	defer httpServer.Close()
	t.Setenv("NANITE_MCP_TEST_MISSING", "")
	if err := os.Unsetenv("NANITE_MCP_TEST_MISSING"); err != nil {
		t.Fatal(err)
	}
	authored := []string{
		fmt.Sprintf(`{"mcpServers":{"fixture":{"type":"http","url":%q}}}`, httpServer.URL),
		`{"mcpServers":{"broken":{"command":"unused","env":{"VALUE":"${NANITE_MCP_TEST_MISSING}"}}}}`,
		`{"mcpServers":{}}`,
	}
	for i, file := range authored {
		if err := os.WriteFile(filepath.Join(e.cfg.Dir, ".mcp.json"), []byte(file), 0600); err != nil {
			t.Fatal(err)
		}
		run, err := e.Send(t.Context(), id, Input{Text: "go"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := run.Wait(); err != nil {
			t.Fatal(err)
		}
		req := p.requests[i]
		tool := slices.ContainsFunc(req.Tools, func(t llmtypes.ToolDefinition) bool { return t.Name == "mcp__fixture__ping" })
		warning := slices.ContainsFunc(req.SlotBlocks, func(s llmtypes.SlotBlock) bool {
			return s.Name == "session" && strings.Contains(s.Content, "MCP server")
		})
		if tool != (i == 0) || warning != (i == 1) {
			t.Fatalf("turn %d: MCP tool=%v warning=%v", i, tool, warning)
		}
	}
}

func TestAuthoredInputsReloadTogether(t *testing.T) {
	if os.Getenv("NANITE_TEST_RELOAD_MCP") == "1" {
		server := mcp.NewServer(&mcp.Implementation{Name: "reload-fixture", Version: "1"}, nil)
		mcp.AddTool(server, &mcp.Tool{Name: "probe", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}},
			func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ready"}}}, nil, nil
			})
		if err := server.Run(t.Context(), &mcp.StdioTransport{}); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	p := &watchedProvider{Provider: fake.New(fake.Text("first"), fake.Text("second"))}
	e, _, id := setupEngine(t, p, "runtime:\n  model: model-before\n")
	send := func() {
		t.Helper()
		run, err := e.Send(t.Context(), id, Input{Text: "report"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := run.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	write := func(name, value string) {
		t.Helper()
		path := filepath.Join(e.cfg.Dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	send()
	cfg, err := json.Marshal(map[string]any{"mcpServers": map[string]any{"fixture": map[string]any{
		"command": os.Args[0], "args": []string{"-test.run=^TestAuthoredInputsReloadTogether$"},
		"env": map[string]string{"NANITE_TEST_RELOAD_MCP": "1"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	write("agent.md", strings.Replace(fixtureAgent, "Engine test instructions.", "Revised agent instructions.", 1))
	write("host.yaml", "kind: embedded\nlifetime: application\napplication_id: fixture\nruntime:\n  model: model-after\n")
	write(".mcp.json", string(cfg))
	write("skills/new/SKILL.md", "---\nname: new-skill\ndescription: works after reload\n---\nprivate skill body")
	send()
	findSlot := func(req llmtypes.ChatRequest, name string) string {
		for _, slot := range req.SlotBlocks {
			if slot.Name == name {
				return slot.Content
			}
		}
		return ""
	}
	hasTool := func(req llmtypes.ChatRequest) bool {
		return slices.ContainsFunc(req.Tools, func(t llmtypes.ToolDefinition) bool { return t.Name == "mcp__fixture__probe" })
	}
	before, after := p.requests[0], p.requests[1]
	if before.Model != "model-before" || hasTool(before) || findSlot(before, "skills") != "" ||
		!strings.Contains(findSlot(before, "agent"), "Engine test instructions.") {
		t.Fatal("turn 1 did not use the original inputs")
	}
	if after.Model != "model-after" || !hasTool(after) ||
		!strings.Contains(findSlot(after, "agent"), "Revised agent instructions.") ||
		!strings.Contains(findSlot(after, "skills"), "new-skill: works after reload") ||
		strings.Contains(findSlot(after, "skills"), "private skill body") {
		t.Fatal("turn 2 did not load all revised authored inputs")
	}
}
