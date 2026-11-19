package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/recovery"
	"github.com/hollis-labs/nanite/internal/store"
)

// interruptedFixture leaves a conversation whose only turn stopped after the
// model asked for a tool and before the tool answered.
func interruptedFixture(t *testing.T, dir string) (conversation, turn string) {
	t.Helper()
	ctx := t.Context()
	if err := initAgent(dir); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(ctx, filepath.Join(dir, ".nanite"), nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	conversation = store.NewID(now)
	if err := db.CreateConversation(ctx, store.Conversation{ID: conversation, Role: "chat", Status: "active", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	journal, err := db.OpenJournal(ctx, conversation)
	if err != nil {
		t.Fatal(err)
	}
	record := store.Turn{ID: store.NewID(now), UserMessageID: store.NewID(now)}
	if err := journal.StartTurn(record, now); err != nil {
		t.Fatal(err)
	}
	for _, message := range []store.Message{
		{ID: record.UserMessageID, TurnID: record.ID, Role: "user", CreatedAt: now, Blocks: []llmtypes.ContentBlock{{Type: "text", Text: "question"}}},
		{ID: store.NewID(now), TurnID: record.ID, Role: "assistant", CreatedAt: now, Blocks: []llmtypes.ContentBlock{{Type: "tool_use", ID: "call-1", Name: "bash", Input: &map[string]any{"command": "sleep 60"}}}},
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
	return conversation, record.ID
}

func TestCLIDiscardsInterruptedTurn(t *testing.T) {
	for name, run := range map[string]struct {
		args  func(conversation string) []string
		input string
	}{
		"flag":   {args: func(id string) []string { return []string{"--discard", id} }},
		"prompt": {args: func(string) []string { return nil }, input: "discard\n/quit\n"},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			dir := t.TempDir()
			conversation, turn := interruptedFixture(t, dir)
			data := filepath.Join(dir, ".nanite")
			started, err := start(ctx, data)
			if err != nil {
				t.Fatal(err)
			}
			root := newRoot(started.App, dir)
			var out, errOut bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&errOut)
			root.SetArgs(append(run.args(conversation), "--events", filepath.Join(data, "fixture-events.jsonl")))
			root.SetIn(strings.NewReader(run.input))
			if err := root.ExecuteContext(ctx); err != nil {
				t.Fatalf("%v\n%s%s", err, out.String(), errOut.String())
			}
			started.Stop(ctx)

			db, err := store.Open(ctx, data, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			if left, err := db.Interrupted(ctx, conversation); err != nil || left != nil {
				t.Fatalf("still interrupted: %+v, %v", left, err)
			}
			if status, err := db.TurnStatus(ctx, turn); err != nil || status != "cancelled" {
				t.Fatalf("turn status %q, %v", status, err)
			}
			history, err := db.History(ctx, conversation)
			if err != nil || len(history) != 3 {
				t.Fatalf("history has %d messages, %v", len(history), err)
			}
			last := history[2]
			if last.Role != "tool" || len(last.Blocks) != 1 || last.Blocks[0].ToolUseID != "call-1" || last.Blocks[0].Content != recovery.SyntheticResult {
				t.Fatalf("unanswered tool call was not closed: %+v", last)
			}
		})
	}
}

func TestCLIDiscardRejectsConversationWithoutInterruptedTurn(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	conversation, _ := interruptedFixture(t, dir)
	data := filepath.Join(dir, ".nanite")
	for i, wantErr := range []bool{false, true} {
		started, err := start(ctx, data)
		if err != nil {
			t.Fatal(err)
		}
		root := newRoot(started.App, dir)
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs([]string{"--discard", conversation, "--events", filepath.Join(data, "fixture-events.jsonl")})
		err = root.ExecuteContext(ctx)
		started.Stop(ctx)
		if (err != nil) != wantErr {
			t.Fatalf("attempt %d: err=%v", i+1, err)
		}
	}
}
