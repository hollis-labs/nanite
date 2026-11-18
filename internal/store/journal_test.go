package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	core "github.com/hollis-labs/libs/core/store"
	"github.com/hollis-labs/libs/llm/core/llmtypes"
)

func TestReplayIntoFreshProjection(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s, err := Open(ctx, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	c := Conversation{ID: NewID(now), Role: "chat", Status: "active", CreatedAt: now, UpdatedAt: now}
	if err := s.CreateConversation(ctx, c); err != nil {
		t.Fatal(err)
	}
	journal, err := s.OpenJournal(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	turn := Turn{ID: NewID(now), UserMessageID: NewID(now)}
	if err := journal.StartTurn(turn, now); err != nil {
		t.Fatal(err)
	}
	if err := journal.AppendMessage(Message{ID: turn.UserMessageID, TurnID: turn.ID, Role: "user", CreatedAt: now,
		Blocks: []llmtypes.ContentBlock{{Type: "text", Text: "hello"}}}, nil); err != nil {
		t.Fatal(err)
	}
	turn.Status = "completed"
	if err := journal.FinishTurn(turn, now); err != nil {
		t.Fatal(err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := core.Open(ctx, core.Config{Driver: core.DriverSQLite, SQLite: core.SQLiteConfig{Path: filepath.Join(root, "other.db")}})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rebuilt, err := Open(ctx, root, db)
	if err != nil {
		t.Fatal(err)
	}
	history, err := rebuilt.History(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || len(history[0].Blocks) != 1 || history[0].Blocks[0].Text != "hello" {
		t.Fatalf("history: %+v", history)
	}
	if err := rebuilt.replay(ctx); err != nil {
		t.Fatal(err)
	}
	history, err = rebuilt.History(ctx, c.ID)
	if err != nil || len(history) != 1 {
		t.Fatalf("repeated replay: %v %v", history, err)
	}
}
