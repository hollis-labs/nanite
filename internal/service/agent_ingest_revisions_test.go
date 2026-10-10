package service

import (
	"errors"
	"testing"

	"github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/store"
)

func TestRetiredBootSourceTransitionRefusesAndPreservesHistoricalRevision(t *testing.T) {
	st := newConfigTestStore(t)
	if _, err := st.DB.ExecContext(t.Context(), `INSERT INTO agent_profiles(id,name,slug,system_prompt,source) VALUES('history-seed','History Seed','history-seed','old body','builtin')`); err != nil {
		t.Fatal(err)
	}
	first, err := st.GetHistoricalAgentProfile(t.Context(), "history-seed")
	if err != nil {
		t.Fatal(err)
	}
	if err = upsertAgentDef(st, &agent.Definition{ID: first.ID, Slug: first.Slug, Source: "internal", SystemPrompt: "new body"}); !errors.Is(err, store.ErrImmutableAgentProfile) {
		t.Fatal("source transition reactivated", err)
	}
	current, err := st.GetHistoricalAgentProfile(t.Context(), first.ID)
	if err != nil || current.Revision != first.Revision || current.Source != first.Source || current.SystemPrompt != first.SystemPrompt {
		t.Fatalf("historical revision changed: %+v %v", current, err)
	}
}
