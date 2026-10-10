package chat

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/hollis-labs/nanite/internal/contextbroker"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

type workingHistoryIntentSource struct {
	mu      sync.Mutex
	queries []string
	onFetch func() error
}

func (*workingHistoryIntentSource) Name() string { return "working-history-test" }
func (s *workingHistoryIntentSource) Fetch(_ context.Context, intent contextbroker.Intent, _ int) ([]contextbroker.ContextItem, error) {
	s.mu.Lock()
	s.queries = append(s.queries, intent.QueryText)
	s.mu.Unlock()
	if s.onFetch != nil {
		return nil, s.onFetch()
	}
	return nil, nil
}
func (s *workingHistoryIntentSource) captured() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.queries...)
}

func TestWorkingHistoryIntentUsesAdmittedQuestionBeforeBroker(t *testing.T) {
	cb, db := newTestBroker(t)
	session := &store.Session{}
	if err := db.CreateSession(t.Context(), session); err != nil {
		t.Fatal(err)
	}
	agent := &store.AgentProfile{Name: "Prior actor", Slug: "prior-intent-actor"}
	if err := storetest.PriorAuthorizedActor(t.Context(), db, agent); err != nil {
		t.Fatal(err)
	}
	current := &store.Message{SessionID: session.ID, Role: "user", Content: "current admitted question"}
	future := &store.Message{SessionID: session.ID, Role: "user", Content: "future queued question"}
	for _, row := range []*store.Message{current, future} {
		if err := db.CreateMessage(t.Context(), row); err != nil {
			t.Fatal(err)
		}
	}
	source := &workingHistoryIntentSource{onFetch: func() error {
		return db.CreateMessage(t.Context(), &store.Message{SessionID: session.ID, Role: "user", Content: "arrived during enrichment"})
	}}
	cb.ContextBroker = contextbroker.New(contextbroker.DefaultBudget(), source)
	ctx := WithWorkingHistoryThrough(t.Context(), current.ID)
	slots, err := cb.AssembleSlotSources(ctx, session, agent)
	if err != nil {
		t.Fatal(err)
	}
	queries := source.captured()
	if len(queries) != 1 || queries[0] != current.Content || slots.Intent.QueryText != current.Content {
		t.Fatalf("broker selected another turn: queries=%q intent=%q", queries, slots.Intent.QueryText)
	}
	if len(slots.Messages) != 1 || slots.Messages[0].Content != current.Content {
		t.Fatalf("conversation diverged from validated intent: %+v", slots.Messages)
	}

	// Ordinary, unmarked assembly keeps its existing latest-question behavior.
	source.onFetch = nil
	plain, err := cb.AssembleSlotSources(t.Context(), session, agent)
	if err != nil || plain.Intent.QueryText != "arrived during enrichment" || len(plain.Messages) != 3 {
		t.Fatalf("unmarked assembly changed: %+v %v", plain, err)
	}
	before := len(source.captured())
	if _, err = db.DB.ExecContext(t.Context(), `UPDATE messages SET is_compacted = 1 WHERE id = ?`, current.ID); err != nil {
		t.Fatal(err)
	}
	for _, marked := range []context.Context{ctx, WithWorkingHistoryThrough(t.Context(), "missing-row")} {
		result, refusal := cb.AssembleSlotSources(marked, session, agent)
		if !errors.Is(refusal, ErrWorkingHistoryBoundary) || result != nil {
			t.Fatalf("missing/cleared boundary admitted: %+v %v", result, refusal)
		}
	}
	if len(source.captured()) != before {
		t.Fatal("refused history reached broker")
	}
}
