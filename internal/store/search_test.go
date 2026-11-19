package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
)

func TestSearchMessagesWithinOneConversation(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	write := func(texts ...string) string {
		c := Conversation{ID: NewID(now), Role: "chat", Status: "active", CreatedAt: now, UpdatedAt: now}
		if err := s.CreateConversation(ctx, c); err != nil {
			t.Fatal(err)
		}
		j, err := s.OpenJournal(ctx, c.ID)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = j.Close() }()
		turn := Turn{ID: NewID(now), UserMessageID: NewID(now)}
		if err := j.StartTurn(turn, now); err != nil {
			t.Fatal(err)
		}
		for i, text := range texts {
			id := NewID(now)
			if i == 0 {
				id = turn.UserMessageID
			}
			blocks := []llmtypes.ContentBlock{{Type: "text", Text: text}}
			role := "user"
			if i%2 == 1 {
				// An assistant message: a tool call whose input carries the text,
				// plus a thinking block that must not be indexed.
				input := map[string]any{"path": text}
				blocks = []llmtypes.ContentBlock{{Type: "thinking", Text: "secretthought"}, {Type: "tool_use", ID: "t", Name: "read_file", Input: &input}}
				role = "assistant"
			}
			if err := j.AppendMessage(Message{ID: id, TurnID: turn.ID, Role: role, CreatedAt: now, Blocks: blocks}, nil); err != nil {
				t.Fatal(err)
			}
		}
		return c.ID
	}
	a := write("the deploy key rotates on Tuesdays", "notes/rotation.md", "unrelated chatter")
	b := write("another conversation about rotation")

	hits, err := s.SearchMessages(ctx, a, "Rotation schedule?", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Seq != 2 || hits[0].Role != "assistant" || !strings.Contains(hits[0].Snippet, "[rotation]") {
		t.Fatalf("hits: %+v", hits)
	}
	if hits, _ := s.SearchMessages(ctx, a, "secretthought", 5); len(hits) != 0 {
		t.Fatalf("thinking was indexed: %+v", hits)
	}
	if hits, _ := s.SearchMessages(ctx, a, `rotates") OR NOT ("x`, 5); len(hits) != 1 || hits[0].Seq != 1 {
		t.Fatalf("FTS syntax in a query must be read as words: %+v", hits)
	}
	if hits, _ := s.SearchMessages(ctx, b, "Tuesdays", 5); len(hits) != 0 {
		t.Fatalf("search crossed conversations: %+v", hits)
	}
	if _, err := s.SearchMessages(ctx, a, " ?! ", 5); err == nil {
		t.Fatal("a query with no words should be refused")
	}

	between, err := s.MessagesBetween(ctx, a, 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(between) != 2 || between[0].Seq != 2 || between[1].Seq != 3 {
		t.Fatalf("between: %+v", between)
	}

	// The index rebuild deletes and replays messages; the search index follows.
	if err := s.RebuildIndex(ctx); err != nil {
		t.Fatal(err)
	}
	if hits, err := s.SearchMessages(ctx, a, "Tuesdays", 5); err != nil || len(hits) != 1 {
		t.Fatalf("after rebuild: %+v %v", hits, err)
	}
	var indexed int
	if err := s.db.Reader().QueryRowContext(ctx, "SELECT count(*) FROM messages_fts").Scan(&indexed); err != nil || indexed != 4 {
		t.Fatalf("index rows after rebuild = %d, %v", indexed, err)
	}
}
