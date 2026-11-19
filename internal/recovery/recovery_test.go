package recovery

import (
	"context"
	"testing"
	"time"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/shell"
	"github.com/hollis-labs/nanite/internal/store"
)

func TestResumePlanCases(t *testing.T) {
	turn := store.InterruptedTurn{ID: "one", UserMessageID: "user"}
	user := store.Message{ID: "user", TurnID: "one", Role: "user", Blocks: []llmtypes.ContentBlock{{Type: "text", Text: "continue"}}}
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
	if err != nil || got.Status != "lost" {
		t.Fatalf("job %+v, %v", got, err)
	}
	notices, err := db.PendingNotices(t.Context(), id)
	if err != nil || len(notices) != 1 {
		t.Fatalf("notices %+v, %v", notices, err)
	}
}
