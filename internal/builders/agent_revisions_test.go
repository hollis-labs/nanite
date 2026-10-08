package builders

import (
	"context"
	"testing"
)

func TestAgentRevisionsBuilderWrites(t *testing.T) {
	db := newTestStore(t)
	builder := NewAgentBuilder(db)
	_, err := builder.BuildFunc(map[string]string{"name": "History Builder", "slug": "history-builder", "system_prompt": "Builder prompt"})
	if err != nil {
		t.Fatal(err)
	}
	current, err := db.GetAgentBySlug(context.Background(), "history-builder")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := db.GetAgentRevision(context.Background(), current.ID, current.Revision)
	if err != nil || snapshot.Profile.SystemPrompt != "Builder prompt" || snapshot.Profile.Name != "History Builder" {
		t.Fatalf("builder snapshot = %#v, %v", snapshot, err)
	}
}
