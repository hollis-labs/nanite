package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
)

// TestEnvelopeRespond_E2E_TranscriptThreadedIntoContext exercises the full
// typed-response loop: create an envelope instance, POST a ResponseV1, then
// assemble context the way a following chat turn would and assert the
// envelope_response message is surfaced to the provider as a user-role turn
// with the marker prefix. See plans/phase-3-s5-envelope-typed-responses.md §T11.
func TestEnvelopeRespond_E2E_TranscriptThreadedIntoContext(t *testing.T) {
	a, mux := newTestAPI(t)

	sess := &store.Session{}
	if err := a.Services.Store.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	agent := &store.AgentProfile{Name: "e", Slug: "e", SystemPrompt: "x"}
	if err := a.Services.Store.CreateAgent(agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	inst := seedEnvelopeInstance(t, a, sess.ID, "collect_feedback")

	body, _ := json.Marshal(chat.ResponseV1{
		V: 1, Kind: "collect_feedback", ID: inst.ID, Status: chat.StatusSubmitted,
		Answers: []chat.Answer{{QuestionID: "q1", Value: "yes"}},
	})
	if w := doPost(mux, "/api/envelopes/"+inst.ID+"/respond", body); w.Code != http.StatusOK {
		t.Fatalf("respond: %d body=%s", w.Code, w.Body.String())
	}

	// Next-turn simulation: assemble context as the LLM adapter would.
	client := chat.NewContextClient(a.Services.Store)
	sources, err := client.AssembleSlotSources(context.Background(), sess, agent)
	if err != nil {
		t.Fatalf("AssembleSlotSources: %v", err)
	}

	var found bool
	for _, m := range sources.Messages {
		if m.Role == "user" && startsWith(m.Content, "[envelope:collect_feedback status:submitted]") {
			found = true
			// answers must be preserved through default handler passthrough.
			if !containsStr2(m.Content, `"answers"`) {
				t.Fatalf("answers not threaded into transcript content: %q", m.Content)
			}
			break
		}
	}
	if !found {
		t.Fatalf("envelope_response not found in assembled context; messages=%+v", sources.Messages)
	}
}

// TestEnvelopeRespond_E2E_SilentHandlerNotInContext verifies a silent handler
// persists the response without leaving a transcript message for the next turn.
func TestEnvelopeRespond_E2E_SilentHandlerNotInContext(t *testing.T) {
	a, mux := newTestAPI(t)

	sess := &store.Session{}
	if err := a.Services.Store.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	agent := &store.AgentProfile{Name: "s", Slug: "s", SystemPrompt: "x"}
	if err := a.Services.Store.CreateAgent(agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	const kind = "e2e-silent"
	chat.RegisterResponseHandler(kind, chat.HandlerFunc(func(_ context.Context, _ store.EnvelopeInstance, _ chat.ResponseV1) (chat.HandlerResult, error) {
		return chat.HandlerResult{Silent: true}, nil
	}))
	t.Cleanup(func() { chat.UnregisterResponseHandler(kind) })

	inst := seedEnvelopeInstance(t, a, sess.ID, kind)
	body, _ := json.Marshal(chat.ResponseV1{V: 1, Kind: kind, ID: inst.ID, Status: chat.StatusSubmitted})
	if w := doPost(mux, "/api/envelopes/"+inst.ID+"/respond", body); w.Code != http.StatusOK {
		t.Fatalf("respond: %d", w.Code)
	}

	client := chat.NewContextClient(a.Services.Store)
	sources, err := client.AssembleSlotSources(context.Background(), sess, agent)
	if err != nil {
		t.Fatalf("AssembleSlotSources: %v", err)
	}
	if len(sources.Messages) != 0 {
		t.Fatalf("silent handler leaked into context: %+v", sources.Messages)
	}

	// Response still persisted on the instance.
	got, _ := a.Services.Store.GetEnvelopeInstance(inst.ID)
	if got.RespondedAt == nil || got.ResponseStatus != "submitted" {
		t.Fatalf("instance not updated: %+v", got)
	}
}

func startsWith(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

func containsStr2(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
