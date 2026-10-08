package selftools

import (
	"context"
	"testing"
)

func TestAgentRevisionsSelfToolWrites(t *testing.T) {
	db := newTestStore(t)
	st := newTestSelfToolsTransport(db)
	ctx := context.Background()
	result, err := st.CallTool(ctx, "agent_create", map[string]any{"name": "History Tool", "slug": "history-tool", "system_prompt": "Original self-tool prompt"})
	if err != nil || result.IsError {
		t.Fatalf("create = %#v, %v", result, err)
	}
	first, err := db.GetAgentBySlug(ctx, "history-tool")
	if err != nil {
		t.Fatal(err)
	}
	old, err := db.GetAgentRevision(ctx, first.ID, first.Revision)
	if err != nil || old.Profile.SystemPrompt != "Original self-tool prompt" {
		t.Fatalf("create snapshot = %#v, %v", old, err)
	}
	result, err = st.CallTool(ctx, "agent_update", map[string]any{"id": first.ID, "system_prompt": "Updated self-tool prompt"})
	if err != nil || result.IsError {
		t.Fatalf("update = %#v, %v", result, err)
	}
	current, err := db.GetAgent(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	latest, err := db.GetAgentRevision(ctx, current.ID, current.Revision)
	if err != nil || latest.Profile.SystemPrompt != "Updated self-tool prompt" || current.Revision == first.Revision {
		t.Fatalf("update snapshot = %#v, %v", latest, err)
	}
}
