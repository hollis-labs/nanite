package service

import (
	"context"
	"testing"

	"github.com/hollis-labs/nanite/internal/agent"
)

func TestAgentRevisionsBootSourceTransition(t *testing.T) {
	st := newConfigTestStore(t)
	def := &agent.Definition{Name: "History Seed", Slug: "history-seed", SystemPrompt: "Seed prompt", Source: "builtin"}
	if err := upsertAgentDef(st, def); err != nil {
		t.Fatal(err)
	}
	first, err := st.GetAgentBySlug(context.Background(), def.Slug)
	if err != nil {
		t.Fatal(err)
	}
	old, err := st.GetAgentRevision(context.Background(), first.ID, first.Revision)
	if err != nil || old.Profile.SystemPrompt != "Seed prompt" {
		t.Fatalf("seed history = %#v, %v", old, err)
	}
	def.Source = "internal"
	def.SystemPrompt = "Transition prompt"
	if err = upsertAgentDef(st, def); err != nil {
		t.Fatal(err)
	}
	current, err := st.GetAgentBySlug(context.Background(), def.Slug)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := st.GetAgentRevision(context.Background(), current.ID, current.Revision)
	if err != nil || snapshot.Profile.Source != "internal" || snapshot.Profile.SystemPrompt != "Transition prompt" || first.Revision == current.Revision {
		t.Fatalf("source transition history = %#v, %v", snapshot, err)
	}
}
