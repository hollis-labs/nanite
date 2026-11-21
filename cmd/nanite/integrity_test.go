package main

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/agent"
	"github.com/hollis-labs/nanite/internal/store"
)

// A conversation that fails integrity checks is not resumed: --resume
// returns the typed refusal, main prints its findings, and --discard still
// closes the turn.
func TestCLIResumeRefusesUnsafeConversation(t *testing.T) {
	dir := t.TempDir()
	if err := initAgent(dir); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	data := filepath.Join(dir, ".nanite")
	db, err := store.Open(ctx, data, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	id := store.NewID(now)
	if err := db.CreateConversation(ctx, store.Conversation{ID: id, Role: "chat", Status: "active", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	journal, err := db.OpenJournal(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	turn := store.Turn{ID: store.NewID(now), UserMessageID: store.NewID(now)}
	if err := journal.StartTurn(turn, now); err != nil {
		t.Fatal(err)
	}
	for _, message := range []store.Message{
		{ID: turn.UserMessageID, TurnID: turn.ID, Role: "user", CreatedAt: now, Blocks: []llmtypes.ContentBlock{{Type: "text", Text: "question"}}},
		{ID: store.NewID(now), TurnID: turn.ID, Role: "tool", CreatedAt: now, Blocks: []llmtypes.ContentBlock{{Type: "tool_result", ToolUseID: "nobody", Content: "x"}}},
	} {
		if err := journal.AppendMessage(message, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	started, err := start(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	defer started.Stop(ctx)
	root := newRoot(started.App, dir)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"--resume", id, "--events", filepath.Join(data, "fixture-events.jsonl")})
	root.SetIn(strings.NewReader("/quit\n"))
	err = root.ExecuteContext(ctx)
	if !errors.Is(err, agent.ErrIntegrity) {
		t.Fatalf("--resume = %v", err)
	}
	var printed bytes.Buffer
	printIntegrity(&printed, err)
	for _, want := range []string{"unsafe orphan_tool_result", "tool_use_id=nobody", "--discard " + id, "rebuild-index"} {
		if !strings.Contains(printed.String(), want) {
			t.Fatalf("printed findings lack %q:\n%s", want, printed.String())
		}
	}
	if strings.Contains(printed.String(), "question") {
		t.Fatalf("printed findings carry content:\n%s", printed.String())
	}

	started.Stop(ctx)
	// A second invocation, as an operator would run it.
	again, err := start(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Stop(ctx)
	root = newRoot(again.App, dir)
	var stderr bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&stderr)
	root.SetArgs([]string{"--discard", id, "--events", filepath.Join(data, "fixture-events.jsonl")})
	root.SetIn(strings.NewReader("/quit\n"))
	if err := root.ExecuteContext(ctx); err != nil {
		t.Fatalf("--discard after a refused resume: %v", err)
	}
	if !strings.Contains(stderr.String(), "Discarded the interrupted turn") {
		t.Fatalf("stderr %q", stderr.String())
	}
}

func TestPrintIntegrityIgnoresOtherErrors(t *testing.T) {
	var printed bytes.Buffer
	printIntegrity(&printed, errors.New("other"))
	if printed.Len() != 0 {
		t.Fatalf("printed %q", printed.String())
	}
}
