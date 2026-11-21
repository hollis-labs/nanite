package recovery

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/slots"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/shell"
	"github.com/hollis-labs/nanite/internal/store"
)

func TestResumePlanCases(t *testing.T) {
	turn := store.InterruptedTurn{ID: "one", UserMessageID: "user"}
	// The user message carries the turn's context block in front of what
	// was said; the plan's UserText is only what was said.
	user := store.Message{ID: "user", TurnID: "one", Role: "user", Blocks: []llmtypes.ContentBlock{
		{Type: "text", Name: slots.TurnContextBlock, Text: "<user_context>\nDESK\n</user_context>"}, {Type: "text", Text: "continue"}}}
	assistant := store.Message{ID: "assistant", TurnID: "one", Role: "assistant", Blocks: []llmtypes.ContentBlock{{Type: "text", Text: "complete"}}}
	tool := store.Message{ID: "assistant", TurnID: "one", Role: "assistant", Blocks: []llmtypes.ContentBlock{{Type: "tool_use", ID: "call"}}}
	result := store.Message{ID: "result", TurnID: "one", Role: "tool", Blocks: []llmtypes.ContentBlock{{Type: "tool_result", ToolUseID: "call"}}}
	synthetic := store.Message{ID: "result", TurnID: "one", Role: "tool", Blocks: []llmtypes.ContentBlock{{Type: "tool_result", ToolUseID: "call", Content: SyntheticResult, IsError: true}}}
	for _, tc := range []struct {
		name     string
		history  []store.Message
		generate bool
		missing  int
	}{
		{"user only", []store.Message{user}, true, 0},
		{"incomplete tool", []store.Message{user, tool}, true, 1},
		{"resume interrupted again", []store.Message{user, tool, synthetic}, true, 0},
		{"fully stored assistant", []store.Message{user, assistant}, false, 0},
		{"fully stored tool result", []store.Message{user, tool, result}, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResumePlan(tc.history, turn)
			if err != nil || got.UserText != "continue" || got.Generate != tc.generate || len(got.Missing) != tc.missing {
				t.Fatalf("plan %+v, %v", got, err)
			}
		})
	}
}

func TestRecoverMarksVerifiedProcessJobLost(t *testing.T) {
	db, err := store.Open(t.Context(), t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now()
	id := store.NewID(now)
	if err := db.CreateConversation(t.Context(), store.Conversation{ID: id, Role: "chat", Status: "active", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	process, err := shell.Start(ctx, shell.Command{Script: "sleep 30", Dir: t.TempDir(), Timeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _, _ = process.Wait(); close(done) }()
	defer func() { cancel(); <-done }()
	identity := shell.ProcessIdentity(t.Context(), process.PID())
	if identity == "" {
		t.Fatal("could not identify live shell process group")
	}
	job := store.Job{ID: store.NewID(now), ConversationID: id, Kind: "shell", PID: process.PID(), ProcessIdentity: identity, Spec: "{}"}
	if err := db.StartJob(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	if err := Recover(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("verified group was left running")
	}
	got, err := db.Job(t.Context(), id, "shell", job.ID)
	if err != nil || got.Status != "lost" || got.Reason != store.JobReasonProcessExit {
		t.Fatalf("job %+v, %v", got, err)
	}
	notices, err := db.PendingNotices(t.Context(), id)
	if err != nil || len(notices) != 1 {
		t.Fatalf("notices %+v, %v", notices, err)
	}
}

func TestClosingMarkerPreservesInterruptedPlan(t *testing.T) {
	turn := store.InterruptedTurn{ID: "turn", UserMessageID: "request"}
	base := []store.Message{
		{ID: "request", TurnID: "turn", Role: "user", Blocks: []llmtypes.ContentBlock{{Type: "text", Text: "original request"}}},
		{ID: "work", TurnID: "turn", Role: "assistant", Blocks: []llmtypes.ContentBlock{{Type: "text", Text: "work"}}},
		{ID: "closing", TurnID: "turn", Role: "user", Blocks: []llmtypes.ContentBlock{{Type: "text", Name: ClosingPromptPrefix + "turn_limit", Text: "Close now"}}},
	}
	for _, partial := range []bool{false, true} {
		history := append([]store.Message(nil), base...)
		want := "work"
		if partial {
			history = append(history, store.Message{ID: "partial", TurnID: "turn", Role: "assistant", Blocks: []llmtypes.ContentBlock{{Type: "text", Text: "partial closing"}}})
			want += "partial closing"
		}
		plan, err := ResumePlan(history, turn)
		if err != nil || plan.ClosingReason != "turn_limit" || plan.UserText != "original request" || plan.Steps != 1 || plan.AssistantText != want || plan.Generate {
			t.Fatalf("partial=%v plan=%+v err=%v", partial, plan, err)
		}
	}
}

// Providers that number calls per response reuse an ID in later steps. A
// reused call left pending at an interruption is still answered: pairing is
// per step, never by ID across the turn.
func TestResumePlanPairsPerStep(t *testing.T) {
	turn := store.InterruptedTurn{ID: "turn", UserMessageID: "user"}
	msg := func(id, role string, blocks ...llmtypes.ContentBlock) store.Message {
		return store.Message{ID: id, TurnID: "turn", Role: role, Blocks: blocks}
	}
	call := func(id string) llmtypes.ContentBlock {
		return llmtypes.ContentBlock{Type: "tool_use", ID: id, Name: "read"}
	}
	result := func(id string) llmtypes.ContentBlock {
		return llmtypes.ContentBlock{Type: "tool_result", ToolUseID: id}
	}
	user := msg("user", "user", llmtypes.ContentBlock{Type: "text", Text: "go"})
	for _, tc := range []struct {
		name    string
		history []store.Message
		missing []string
	}{
		{"reused id pending", []store.Message{user, msg("a1", "assistant", call("call_0")), msg("r1", "tool", result("call_0")),
			msg("a2", "assistant", call("call_0"))}, []string{"call_0"}},
		{"reused ids, one answered", []store.Message{user, msg("a1", "assistant", call("call_0"), call("call_1")), msg("r1", "tool", result("call_0"), result("call_1")),
			msg("a2", "assistant", call("call_0"), call("call_1")), msg("r2", "tool", result("call_1"))}, []string{"call_0"}},
		{"reused ids all answered", []store.Message{user, msg("a1", "assistant", call("call_0")), msg("r1", "tool", result("call_0")),
			msg("a2", "assistant", call("call_0")), msg("r2", "tool", result("call_0"))}, nil},
		{"answered in two tool messages", []store.Message{user, msg("a1", "assistant", call("a"), call("b")), msg("r1", "tool", result("a")),
			msg("r2", "tool", result("b"))}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := ResumePlan(tc.history, turn)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, b := range plan.Missing {
				got = append(got, b.ToolUseID)
			}
			if !slices.Equal(got, tc.missing) || plan.Generate != (len(tc.missing) > 0) {
				t.Fatalf("missing %v generate %v, want %v", got, plan.Generate, tc.missing)
			}
		})
	}
}

// End to end through the store: a turn interrupted on a reused call, then
// discarded the way the engine does it, leaves a conversation the
// integrity validator finds clean.
func TestResumePlanReusedIDRecoversToCleanHistory(t *testing.T) {
	db, err := store.Open(t.Context(), t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	id := store.NewID(now)
	if err := db.CreateConversation(t.Context(), store.Conversation{ID: id, Role: "chat", Status: "active", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	j, err := db.OpenJournal(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	turn := store.Turn{ID: store.NewID(now), UserMessageID: store.NewID(now)}
	use := func(mid string) store.Message {
		return store.Message{ID: mid, TurnID: turn.ID, Role: "assistant", CreatedAt: now,
			Blocks: []llmtypes.ContentBlock{{Type: "tool_use", ID: "call_0", Name: "read", Input: &map[string]any{}}}}
	}
	for _, err := range []error{
		j.StartTurn(turn, now),
		j.AppendMessage(store.Message{ID: turn.UserMessageID, TurnID: turn.ID, Role: "user", CreatedAt: now, Blocks: []llmtypes.ContentBlock{{Type: "text", Text: "go"}}}, nil),
		j.AppendMessage(use(store.NewID(now)), nil),
		j.AppendMessage(store.Message{ID: store.NewID(now), TurnID: turn.ID, Role: "tool", CreatedAt: now, Blocks: []llmtypes.ContentBlock{{Type: "tool_result", ToolUseID: "call_0", Content: "ok"}}}, nil),
		j.AppendMessage(use(store.NewID(now)), nil),
		j.InterruptTurn(turn.ID, now),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	history, err := db.History(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := ResumePlan(history, store.InterruptedTurn{ID: turn.ID, UserMessageID: turn.UserMessageID})
	if err != nil || len(plan.Missing) != 1 {
		t.Fatalf("plan %+v, %v", plan, err)
	}
	if err := j.AppendMessage(store.Message{ID: store.NewID(now), TurnID: turn.ID, Role: "tool", Blocks: plan.Missing, CreatedAt: now}, nil); err != nil {
		t.Fatal(err)
	}
	turn.Status = "cancelled"
	if err := j.FinishTurn(turn, now); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	report, err := db.ValidateConversation(t.Context(), id, nil)
	if err != nil || len(report.Findings) != 0 {
		t.Fatalf("after recovery: %v, %v", report.Findings, err)
	}
}
