package store

import (
	"context"
	"reflect"
	"testing"
	"time"

	core "github.com/hollis-labs/libs/core/store"
	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/integrity"
	"github.com/hollis-labs/nanite/internal/transcript"
)

// writeIntegrityConversation writes, through the real journal, a completed turn with
// a tool step, a compaction and a closing host note, then an interrupted turn
// with an unanswered call.
func writeIntegrityConversation(ctx context.Context, t *testing.T, s *Store) (Conversation, Turn) {
	t.Helper()
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
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	var parent *string
	msg := func(turn, role string, blocks ...llmtypes.ContentBlock) string {
		t.Helper()
		m := Message{ID: NewID(now), TurnID: turn, Role: role, Blocks: blocks, CreatedAt: now}
		must(j.AppendMessage(m, parent))
		parent = &m.ID
		return m.ID
	}
	first := Turn{ID: NewID(now), UserMessageID: NewID(now)}
	must(j.StartTurn(first, now))
	must(j.AppendMessage(Message{ID: first.UserMessageID, TurnID: first.ID, Role: "user", CreatedAt: now,
		Blocks: []llmtypes.ContentBlock{{Type: "text", Name: "nanite.turn_context", Text: "ctx"}, {Type: "text", Text: "hello"}}}, nil))
	parent = &first.UserMessageID
	msg(first.ID, "assistant", llmtypes.ContentBlock{Type: "tool_use", ID: "call-1", Name: "read", Input: &map[string]any{}})
	through := msg(first.ID, "tool", llmtypes.ContentBlock{Type: "tool_result", ToolUseID: "call-1", Content: "ok"})
	must(j.RecordCompaction(NewID(now), now, Compaction{TurnID: first.ID, ThroughMessageID: through, Summary: "s", Before: 3, After: 1}))
	must(j.RecordPermission(NewID(now), now, map[string]string{"tool": "read"}))
	msg(first.ID, "user", llmtypes.ContentBlock{Type: "text", Name: integrity.ClosingPromptPrefix + "max_steps", Text: "limit"})
	msg(first.ID, "assistant", llmtypes.ContentBlock{Type: "text", Text: "done"})
	first.Status = "completed"
	must(j.FinishTurn(first, now))

	second := Turn{ID: NewID(now), UserMessageID: NewID(now)}
	must(j.StartTurn(second, now))
	must(j.AppendMessage(Message{ID: second.UserMessageID, TurnID: second.ID, Role: "user", CreatedAt: now,
		Blocks: []llmtypes.ContentBlock{{Type: "text", Text: "again"}}}, parent))
	parent = &second.UserMessageID
	msg(second.ID, "assistant", llmtypes.ContentBlock{Type: "tool_use", ID: "call-2", Name: "read", Input: &map[string]any{}})
	must(j.InterruptTurn(second.ID, now))
	must(s.AddNotice(ctx, Notice{ID: NewID(now), ConversationID: c.ID, Kind: "tether_mail", Body: "mail"}))
	return c, second
}

func TestValidateConversationWrittenByJournal(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c, interrupted := writeIntegrityConversation(ctx, t, s)

	report, err := s.ValidateConversation(ctx, c.ID, &integrity.Hold{TurnID: interrupted.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Safe() || report.Worst() != integrity.Recoverable {
		t.Fatalf("findings = %v", report.Findings)
	}
	codes := map[integrity.Code]bool{}
	for _, f := range report.Findings {
		codes[f.Code] = true
	}
	if len(codes) != 2 || !codes[integrity.CodeInterruptedTurn] || !codes[integrity.CodeToolUseWithoutResult] {
		t.Fatalf("findings = %v", report.Findings)
	}

	// Project must agree with the store's own projection row for row.
	index, err := s.IntegrityIndex(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := transcript.Replay(s.transcripts, c.ID, transcript.Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	want := integrity.Project(c.ID, entries)
	if len(index.Notices) != 1 || index.Notices[0].DeliveredTurnID != "" {
		t.Fatalf("notices = %+v", index.Notices)
	}
	index.Notices = nil
	if !reflect.DeepEqual(index, want) {
		t.Fatalf("index differs from Project:\n got %+v\nwant %+v", index, want)
	}
}

func TestValidateConversationIndexDisagreementAndRebuild(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c, interrupted := writeIntegrityConversation(ctx, t, s)
	if err := s.db.WriteTx(ctx, func(ctx context.Context, tx core.Tx) error {
		_, err := tx.ExecContext(ctx, "UPDATE turns SET status='completed' WHERE id=?", interrupted.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	report, err := s.ValidateConversation(ctx, c.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	unsafe := report.Unsafe()
	if len(unsafe) != 1 || unsafe[0].Code != integrity.CodeIndexDisagreement || unsafe[0].Repair != integrity.RepairRebuildIndex {
		t.Fatalf("findings = %v", report.Findings)
	}
	if err := s.RebuildIndex(ctx); err != nil {
		t.Fatal(err)
	}
	if report, err = s.ValidateConversation(ctx, c.ID, nil); err != nil || !report.Safe() {
		t.Fatalf("after rebuild: %v %v", report.Findings, err)
	}
}

func TestValidateConversationRefusesBadID(t *testing.T) {
	s, err := Open(context.Background(), t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.ValidateConversation(context.Background(), "../x", nil); err == nil {
		t.Fatal("path-unsafe conversation ID accepted")
	}
	report, err := s.ValidateConversation(context.Background(), "absent", nil)
	if err != nil || len(report.Findings) != 0 {
		t.Fatalf("absent conversation: %v %v", report.Findings, err)
	}
}
