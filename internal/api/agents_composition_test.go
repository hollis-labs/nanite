package api

import (
	"fmt"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// seedTestModel inserts a minimal providers + models row directly via SQL
// (the store package has no CreateModel/CreateProvider write helper this
// test can call -- the model SET comes from pkg/models.AllSeeded() via
// store.SyncModelsFromRegistry at container boot, with models.dev enriching
// those entries' pricing and limits rather than adding any, and providers are
// seeded via config; this lightweight test DB runs neither). Returns the minted model row's id (the models.id PK,
// agent_profiles.model_id's FK target -- distinct from models.model_id,
// the wire model identifier string).
func seedTestModel(t *testing.T, a *testAPI) string {
	t.Helper()
	if _, err := a.store.DB.Exec(
		`INSERT INTO providers (id, name, provider_type) VALUES (?, ?, ?)`,
		"prov-composition-test", "Test Provider", "anthropic",
	); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	modelID := "model-composition-test"
	if _, err := a.store.DB.Exec(
		`INSERT INTO models (id, provider_id, model_id, display_name) VALUES (?, ?, ?, ?)`,
		modelID, "prov-composition-test", "claude-test-model", "Claude Test Model",
	); err != nil {
		t.Fatalf("seed model: %v", err)
	}
	return modelID
}

func TestRetiredAgentCompositionDoesNotBecomeHostOrActorAuthority(t *testing.T) {
	a, mux := newTestAPI(t)
	p := retiredAgentHistory(t, a, "user")
	role := &store.Role{Slug: "composition-role", Name: "Composition Role", SystemPrompt: "You help."}
	if err := a.store.CreateRole(t.Context(), role); err != nil {
		t.Fatal(err)
	}
	modelID := seedTestModel(t, a)
	before := retiredAgentState(t, a)
	for _, body := range []string{
		`{"name":"Composed","slug":"composed","role_id":"missing","consumer_id":"missing","model_id":"missing"}`,
		fmt.Sprintf(`{"name":"Composed","slug":"composed","role_id":%q,"consumer_id":"blt-loom-001","model_id":%q}`, role.ID, modelID),
		`{"role_id":"","consumer_id":"","model_id":""}`,
		`{"description":"unrelated edit"}`,
	} {
		retiredAgentRequest(t, mux, "POST", "/api/agents", body)
		retiredAgentRequest(t, mux, "PUT", "/api/agents/"+p.ID, body)
		assertRetiredAgentState(t, a, before)
	}
}
