package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/store"
)

func TestCLIResumeFinalizesPersistedAnswer(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		name := "interactive confirmation"
		if explicit {
			name = "explicit resume"
		}
		t.Run(name, func(t *testing.T) {
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
				{ID: store.NewID(now), TurnID: turn.ID, Role: "assistant", CreatedAt: now, Blocks: []llmtypes.ContentBlock{{Type: "text", Text: "saved reply"}}},
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
			if explicit {
				root.SetArgs([]string{"--resume", id, "--events", filepath.Join(data, "fixture-events.jsonl")})
				root.SetIn(strings.NewReader("/quit\n"))
			} else {
				root.SetArgs([]string{})
				root.SetIn(strings.NewReader("y\n/quit\n"))
			}
			if err := root.ExecuteContext(ctx); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), "saved reply") || (strings.Contains(out.String(), "has an interrupted turn") == explicit) {
				t.Fatalf("terminal output %q", out.String())
			}
		})
	}
}
