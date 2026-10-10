package builders

import (
	"errors"
	"reflect"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

func TestAgentRevisionsBuilderWrites(t *testing.T) {
	db := newTestStore(t)
	retained := &store.AgentProfile{Name: "Historical builder", Slug: "history-builder", SystemPrompt: "Retained prompt", Source: "user"}
	// Private retained data never grants builder or runtime authority.
	if err := storetest.HistoricalProfile(t.Context(), db, retained); err != nil {
		t.Fatal(err)
	}
	before := builderBoundarySnapshot(t, db)
	builder := NewAgentBuilder(db)
	for _, model := range []string{"", "claimed-default-model"} {
		result, err := builder.BuildFunc(map[string]string{"name": "History Builder", "slug": "history-builder", "system_prompt": "Replacement prompt", "model": model})
		if !errors.Is(err, store.ErrImmutableAgentProfile) || result != nil {
			t.Fatalf("BuildFunc = %+v, %v; want immutable refusal", result, err)
		}
		if after := builderBoundarySnapshot(t, db); !reflect.DeepEqual(before, after) {
			t.Fatalf("builder revised history or authority: %#v -> %#v", before, after)
		}
	}
	revision, err := db.GetAgentRevision(t.Context(), retained.ID, retained.Revision)
	if err != nil || revision.Profile.SystemPrompt != retained.SystemPrompt || revision.ID != retained.Revision {
		t.Fatalf("retained revision = %+v, %v", revision, err)
	}
}
