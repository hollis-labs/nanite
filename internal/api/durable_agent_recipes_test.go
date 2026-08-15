package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

func TestDurableAgentRecipesAPI_ListGetDryRun(t *testing.T) {
	a, mux := newTestAPI(t)
	profile := &store.AgentProfile{Name: "Recipe API Agent", Slug: "recipe-api-agent", SystemPrompt: "x"}
	if err := a.Services.Store.CreateAgent(profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/durable-agent-recipes", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list recipes = %d body=%s", w.Code, w.Body.String())
	}
	var recipes []service.DurableAgentRecipe
	if err := json.NewDecoder(w.Body).Decode(&recipes); err != nil {
		t.Fatalf("decode recipes: %v", err)
	}
	if len(recipes) != 12 || recipes[0].ID != "architect-advisor" {
		t.Fatalf("recipes = %+v", recipes)
	}
	if len(recipes[0].Inputs) == 0 {
		t.Fatalf("recipes[0] missing inputs: %+v", recipes[0])
	}

	req = httptest.NewRequest("GET", "/api/durable-agent-recipes/project-advisor", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get recipe = %d body=%s", w.Code, w.Body.String())
	}
	var recipe service.DurableAgentRecipe
	if err := json.NewDecoder(w.Body).Decode(&recipe); err != nil {
		t.Fatalf("decode recipe: %v", err)
	}
	if len(recipe.Inputs) == 0 || recipe.Inputs[0].ID != "name" {
		t.Fatalf("recipe inputs = %+v", recipe.Inputs)
	}

	body, _ := json.Marshal(DurableAgentRecipeRequest{
		Name:      "Advisor",
		Slug:      "advisor",
		ProfileID: profile.ID,
		WorkRoot:  "/tmp/advisor",
		Metadata: map[string]string{
			"scope_topic": "agridd",
		},
	})
	req = httptest.NewRequest("POST", "/api/durable-agent-recipes/project-advisor/dry-run", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("dry-run recipe = %d body=%s", w.Code, w.Body.String())
	}
	var plan service.DurableAgentRecipePlan
	if err := json.NewDecoder(w.Body).Decode(&plan); err != nil {
		t.Fatalf("decode dry-run: %v", err)
	}
	if !plan.Ready || plan.Instance.Slug != "advisor" || plan.Instance.WorkRoot != "/tmp/advisor" {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestDurableAgentRecipesAPI_ApplyAndStart(t *testing.T) {
	a, mux := newTestAPI(t)
	profile := &store.AgentProfile{Name: "Apply API Agent", Slug: "apply-api-agent", SystemPrompt: "x"}
	if err := a.Services.Store.CreateAgent(profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if err := a.Services.Store.CreateWorkspace(&store.Workspace{ID: "workspace-a", Name: "Workspace A"}); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	body, _ := json.Marshal(DurableAgentRecipeRequest{
		Name:        "Monitor API",
		Slug:        "monitor-api",
		ProfileID:   profile.ID,
		WorkspaceID: "workspace-a",
		Start:       true,
	})
	req := httptest.NewRequest("POST", "/api/durable-agent-recipes/process-monitor/apply", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("apply recipe = %d body=%s", w.Code, w.Body.String())
	}
	var result service.DurableAgentRecipeApplyResult
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode apply: %v", err)
	}
	if result.Instance == nil || result.Instance.CurrentSessionID == "" || result.LaunchResult == nil {
		t.Fatalf("result = %+v", result)
	}
}

func TestDurableAgentRecipesAPI_ErrorCases(t *testing.T) {
	_, mux := newTestAPI(t)
	req := httptest.NewRequest("GET", "/api/durable-agent-recipes/missing", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing recipe = %d body=%s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest("POST", "/api/durable-agent-recipes/project-advisor/apply", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("apply missing profile = %d body=%s", w.Code, w.Body.String())
	}
}
