package store

import (
	"context"
	"slices"
	"testing"
	"time"

	core "github.com/hollis-labs/libs/core/store"
	"github.com/hollis-labs/libs/llm/core/llmtypes"
)

// writeConversation stores a conversation whose messages alternate user text
// and an assistant tool call carrying the text as input (plus a thinking
// block, which must not be indexed). It returns the conversation ID.
func writeConversation(t *testing.T, s *Store, texts ...string) string {
	t.Helper()
	ctx := t.Context()
	now := time.Now().UTC()
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

func seqs(messages []Message) []int {
	var out []int
	for _, m := range messages {
		out = append(out, m.Seq)
	}
	return out
}

func TestSearchMessagesWithinOneConversation(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := writeConversation(t, s, "the deploy key rotates on Tuesdays", "notes/rotation.md", "unrelated chatter")
	b := writeConversation(t, s, "another conversation about rotation")

	hits, err := s.SearchMessages(ctx, a, "Rotation schedule?", 5)
	if err != nil {
		t.Fatal(err)
	}
	if got := seqs(hits); !slices.Equal(got, []int{2}) || hits[0].Role != "assistant" {
		t.Fatalf("hits: %v %+v", got, hits)
	}
	if hits, _ := s.SearchMessages(ctx, a, "secretthought", 5); len(hits) != 0 {
		t.Fatalf("thinking was indexed: %+v", hits)
	}
	if hits, _ := s.SearchMessages(ctx, a, `rotates") OR NOT ("x`, 5); !slices.Equal(seqs(hits), []int{1}) {
		t.Fatalf("FTS syntax in a query must be read as words: %v", seqs(hits))
	}
	if hits, _ := s.SearchMessages(ctx, b, "Tuesdays", 5); len(hits) != 0 {
		t.Fatalf("search crossed conversations: %+v", hits)
	}
	if _, err := s.SearchMessages(ctx, a, " ?! ", 5); err == nil {
		t.Fatal("a query with no words should be refused")
	}
	if between, err := s.MessagesBetween(ctx, a, 2, 3); err != nil || !slices.Equal(seqs(between), []int{2, 3}) {
		t.Fatalf("between: %v %v", seqs(between), err)
	}
	if last, err := s.LastMessages(ctx, a, 2); err != nil || !slices.Equal(seqs(last), []int{2, 3}) {
		t.Fatalf("last: %v %v", seqs(last), err)
	}
	if last, err := s.LastMessages(ctx, a, 10); err != nil || !slices.Equal(seqs(last), []int{1, 2, 3}) {
		t.Fatalf("last 10: %v %v", seqs(last), err)
	}
}

func TestSearchIndexFollowsRebuildsAndUpdates(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var ids []string
	for range 3 {
		ids = append(ids, writeConversation(t, s, "rebuildword one", "rebuildword two", "rebuildword three", "rebuildword four", "rebuildword five"))
	}
	consistent := func() {
		t.Helper()
		var messages, indexed, joined int
		reader := s.db.Reader()
		if err := reader.QueryRowContext(ctx, "SELECT count(*) FROM messages").Scan(&messages); err != nil {
			t.Fatal(err)
		}
		if err := reader.QueryRowContext(ctx, "SELECT count(*) FROM messages_fts").Scan(&indexed); err != nil {
			t.Fatal(err)
		}
		if err := reader.QueryRowContext(ctx, `SELECT count(*) FROM messages_fts JOIN messages m
 ON m.rowid = messages_fts.rowid AND m.conversation_id = messages_fts.conversation_id`).Scan(&joined); err != nil {
			t.Fatal(err)
		}
		if messages != 15 || indexed != messages || joined != messages {
			t.Fatalf("index out of step: messages=%d indexed=%d consistent=%d", messages, indexed, joined)
		}
	}
	consistent()
	// The index rebuild deletes and replays messages; the search index follows.
	for range 2 {
		if err := s.RebuildIndex(ctx); err != nil {
			t.Fatal(err)
		}
		consistent()
	}
	if hits, err := s.SearchMessages(ctx, ids[0], "three", 5); err != nil || !slices.Equal(seqs(hits), []int{3}) {
		t.Fatalf("after rebuild: %v %v", seqs(hits), err)
	}
	// An edited message is re-indexed.
	if err := s.db.WriteTx(ctx, func(ctx context.Context, tx core.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE messages SET blocks_json='[{"type":"text","text":"zeppelin"}]'
 WHERE conversation_id=? AND seq=1`, ids[0])
		return err
	}); err != nil {
		t.Fatal(err)
	}
	consistent()
	if hits, _ := s.SearchMessages(ctx, ids[0], "zeppelin", 5); !slices.Equal(seqs(hits), []int{1}) {
		t.Fatalf("updated message not found: %v", seqs(hits))
	}
	if hits, _ := s.SearchMessages(ctx, ids[0], "one", 5); len(hits) != 0 {
		t.Fatalf("old text of an updated message still matches: %v", seqs(hits))
	}
}

func TestSearchJoinChecksConversation(t *testing.T) {
	// An index row whose rowid points at another conversation's message, as
	// after a VACUUM renumbered rows, must not surface that message.
	ctx := context.Background()
	s, err := Open(ctx, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := writeConversation(t, s, "alpha text")
	b := writeConversation(t, s, "bravo text")
	var rowB int64
	if err := s.db.Reader().QueryRowContext(ctx, "SELECT rowid FROM messages WHERE conversation_id=?", b).Scan(&rowB); err != nil {
		t.Fatal(err)
	}
	if err := s.db.WriteTx(ctx, func(ctx context.Context, tx core.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM messages_fts WHERE rowid=?", rowB); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO messages_fts(rowid, body, conversation_id) VALUES(?, 'orphanword', ?)", rowB, a)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if hits, err := s.SearchMessages(ctx, a, "orphanword", 5); err != nil || len(hits) != 0 {
		t.Fatalf("search in one conversation returned another's message: %v %v", seqs(hits), err)
	}
}
