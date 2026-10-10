package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
	llmtypes "github.com/hollis-labs/substrate/llm-core/llmtypes"
)

func TestClientContextNativeQueueDetachAndPrivacy(t *testing.T) {
	hold := make(chan struct{})
	release := sync.OnceFunc(func() { close(hold) })
	f := newHandleMessageFixture(t, []characterizationProviderStep{{events: doneEvents("first answer"), hold: hold}, {events: doneEvents("second answer")}, {events: doneEvents("third answer")}})
	bindTestDefinedConfiguration(t, f)
	t.Cleanup(release)
	request, cancelRequest := context.WithCancel(context.Background())
	request = WithClientContextSnapshot(chat.WithDeltaMode(request, chat.DeltaModeLive), clientContextFixture(t, "/first-view"))
	first, err := f.svc.SubmitCognitiveTurn(request, f.session, "duplicate question")
	if err != nil {
		t.Fatal(err)
	}
	firstStream, _ := f.svc.streams.GetStream(first)
	waitForDelta(t, firstStream, "first generation")
	cancelRequest() // generation and its owned observation survive HTTP loss
	second, err := f.svc.SubmitCognitiveTurn(WithClientContextSnapshot(t.Context(), clientContextFixture(t, "/second-view")), f.session, "duplicate question")
	if err != nil {
		t.Fatal(err)
	}
	third, err := f.svc.SubmitCognitiveTurn(t.Context(), f.session, "third question")
	if err != nil {
		t.Fatal(err)
	}
	if f.provider.callCount() != 1 {
		t.Fatal("queued turn started before predecessor")
	}
	release()
	drainStream(firstStream)
	for _, id := range []string{second, third} {
		stream, _ := f.svc.streams.GetStream(id)
		drainStream(stream)
	}
	requests := f.provider.requestsSnapshot()
	if len(requests) != 3 {
		t.Fatal("provider requests", len(requests))
	}
	for index, req := range requests {
		var observations int
		for i, msg := range req.Messages {
			if strings.Contains(msg.Content, "<client_context_data>") {
				observations++
				if msg.Role != "user" || i+1 >= len(req.Messages) || req.Messages[i+1].Content != "duplicate question" || len(msg.ContentBlocks) != 0 {
					t.Fatal("misbound descriptor", index, i)
				}
				wanted := "/first-view"
				if index == 1 {
					wanted = "/second-view"
				}
				if !strings.Contains(msg.Content, wanted) {
					t.Fatal("cross-turn descriptor", index)
				}
			}
			if index < 2 && msg.Content == "third question" {
				t.Fatal("future queued input leaked")
			}
		}
		if index < 2 && observations != 1 || index == 2 && observations != 0 {
			t.Fatal("descriptor inheritance", index, observations)
		}
		if index > 0 {
			found := false
			for _, msg := range req.Messages {
				if msg.Content == "first answer" {
					found = true
				}
			}
			if !found {
				t.Fatal("completed predecessor lost after queued admission", index)
			}
		}
		serialized, _ := json.Marshal(req.SlotBlocks)
		if strings.Contains(string(serialized), "view") { // check exact private marker, not generic text
			if strings.Contains(string(serialized), "first-view") || strings.Contains(string(serialized), "second-view") {
				t.Fatal("descriptor entered slot blocks")
			}
		}
	}
	messages, err := f.st.ListMessages(t.Context(), f.session, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range messages {
		data, _ := json.Marshal(msg)
		if strings.Contains(string(data), "first-view") || strings.Contains(string(data), "second-view") {
			t.Fatal("descriptor persisted in transcript")
		}
	}
	for _, id := range []string{first, second, third} {
		snapshot, snapErr := f.svc.streams.CognitiveTurns().Get(f.session, id)
		if snapErr != nil || snapshot.State != "completed" {
			t.Fatal(snapshot.State, snapErr)
		}
		data, _ := json.Marshal(snapshot)
		if strings.Contains(string(data), "-view") {
			t.Fatal("descriptor persisted in status")
		}
	}
	f.context.mu.Lock()
	last := f.context.last
	f.context.mu.Unlock()
	for _, msg := range last.Messages {
		if strings.Contains(msg.Content, "<client_context_data>") {
			t.Fatal("descriptor entered compaction/inspector source")
		}
	}
}

func TestClientContextNativeCanceledQueueHasNoInheritance(t *testing.T) {
	hold := make(chan struct{})
	release := sync.OnceFunc(func() { close(hold) })
	f := newHandleMessageFixture(t, []characterizationProviderStep{{events: doneEvents("first"), hold: hold}, {events: doneEvents("successor")}})
	bindTestDefinedConfiguration(t, f)
	t.Cleanup(release)
	first, err := f.svc.SubmitCognitiveTurn(chat.WithDeltaMode(t.Context(), chat.DeltaModeLive), f.session, "first")
	if err != nil {
		t.Fatal(err)
	}
	stream, _ := f.svc.streams.GetStream(first)
	waitForDelta(t, stream, "first")
	canceled, err := f.svc.SubmitCognitiveTurn(WithClientContextSnapshot(t.Context(), clientContextFixture(t, "/canceled-view")), f.session, "canceled")
	if err != nil {
		t.Fatal(err)
	}
	if !f.svc.CancelCognitiveTurn(f.session, canceled) {
		t.Fatal("cancel refused")
	}
	successor, err := f.svc.SubmitCognitiveTurn(t.Context(), f.session, "successor")
	if err != nil {
		t.Fatal(err)
	}
	release()
	drainStream(stream)
	for _, id := range []string{canceled, successor} {
		consumer, _ := f.svc.streams.GetStream(id)
		drainStream(consumer)
	}
	requests := f.provider.requestsSnapshot()
	if len(requests) != 2 {
		t.Fatal(len(requests))
	}
	for _, msg := range requests[1].Messages {
		if strings.Contains(msg.Content, "canceled-view") {
			t.Fatal("canceled snapshot inherited")
		}
	}
}

func TestClientContextBudgetPreservesAnchorOrRefuses(t *testing.T) {
	ctx := chat.WithWorkingHistoryThrough(WithClientContextSnapshot(t.Context(), clientContextFixture(t, "/budget-view")), "accepted")
	original := []llmtypes.ChatMessage{{Role: "user", Content: strings.Repeat("old", 1000)}, {Role: "user", Content: "question"}}
	run := &runState{chatMessages: original, clientInput: newClientContextInput(ctx, original, original)}
	observation, _ := clientContextPromptSlot(ctx)
	reserved := chat.EstimateMessagesTokens([]llmtypes.ChatMessage{observation})
	if err := clientContextBudget(ctx, run, reserved+50); err != nil {
		t.Fatal(err)
	}
	projected, err := run.clientInput.project(ctx, run.chatMessages)
	if err != nil || len(projected) != 2 || projected[1].Content != "question" || run.breakdown.Total > run.breakdown.Ceiling {
		t.Fatal(projected, err, run.breakdown)
	}
	if len(run.chatMessages) != 1 || strings.Contains(run.chatMessages[0].Content, "client_context") {
		t.Fatal("transient projection mutated persistent history")
	}
	run.chatMessages[0] = llmtypes.ChatMessage{Role: "user", Content: "another question"}
	if _, err = run.clientInput.project(ctx, run.chatMessages); !errors.Is(err, chat.ErrWorkingHistoryBoundary) {
		t.Fatal("replaced anchor admitted", err)
	}
	if err = clientContextBudget(ctx, run, 10); err == nil {
		t.Fatal("descriptor silently dropped for budget")
	}
}

func TestClientContextClearBetweenValidationAndAssemblyRefuses(t *testing.T) {
	f := newHandleMessageFixture(t, nil)
	bindTestDefinedConfiguration(t, f)
	user := &store.Message{ID: "accepted", SessionID: f.session, Role: "user", Content: "question"}
	if err := f.st.CreateMessage(t.Context(), user); err != nil {
		t.Fatal(err)
	}
	ctx := chat.WithWorkingHistoryThrough(WithClientContextSnapshot(t.Context(), clientContextFixture(t, "/cleared-view")), user.ID)
	validated, err := f.svc.completeWorkingHistory(ctx, f.session)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.st.DB.ExecContext(t.Context(), `UPDATE messages SET is_compacted=1 WHERE id=?`, user.ID); err != nil {
		t.Fatal(err)
	}
	ch := make(chan chat.StreamEvent, 16)
	prepared := f.svc.prepareTurn(validated, f.session, "question", ch)
	if prepared.setup != nil || f.provider.callCount() != 0 {
		t.Fatal("cleared boundary reached provider")
	}
}
