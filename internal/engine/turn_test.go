package engine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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
	e, err := New(Config{Dir: root, GlobalSkillsDir: "-", Provider: model, Store: db, Slots: slots.NewStore(), Tools: &tools.Groups{}, Skills: &skills.Groups{}})
	if err != nil {
		t.Fatal(err)
	}
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
