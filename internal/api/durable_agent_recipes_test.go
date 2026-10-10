package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

func TestDurableAgentRecipesAPI_ListGetDryRun(t *testing.T) {
	a, mux := newTestAPI(t)
	profile := &store.AgentProfile{Name: "Recipe API Agent", Slug: "recipe-api-agent", SystemPrompt: "x"}
	if err := storetest.PriorAuthorizedActor(t.Context(), a.store, profile); err != nil {
		t.Fatalf("PriorAuthorizedActor: %v", err)
	}

	before := durableAPIRefusalState(t, a)
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
	if len(recipes) != 15 || recipes[0].ID != "architect-advisor" {
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
	if plan.Instance.ID != "" || plan.Instance.URN != "" || !plan.Instance.CreatedAt.IsZero() {
		t.Fatalf("dry-run issued identity: %+v", plan.Instance)
	}
	assertDurableAPIRefusalState(t, a, before)
	if !plan.Ready || plan.Instance.Slug != "advisor" || plan.Instance.WorkRoot != "/tmp/advisor" {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestDurableAgentRecipesAPI_ApplyRequiresVerifiedAuthority(t *testing.T) {
	a, mux := newTestAPI(t)
	profile := &store.AgentProfile{Name: "Apply API Agent", Slug: "apply-api-agent", SystemPrompt: "x"}
	if err := storetest.PriorAuthorizedActor(t.Context(), a.store, profile); err != nil {
		t.Fatalf("PriorAuthorizedActor: %v", err)
	}
	prior := &store.DurableAgentInstance{Name: "Prior recipe instance", Slug: "prior-recipe-instance", ProfileID: profile.ID}
	persistPriorAPIInstance(t, a.store, prior)
	seedDurableAPIHistory(t, a)
	before := durableAPIRefusalState(t, a)
	for _, start := range []bool{false, true} {
		request := DurableAgentRecipeRequest{Name: "Monitor API", Slug: "monitor-api", ProfileID: profile.ID, Start: start}
		result, err := a.Services.DurableAgentRecipes.Apply(t.Context(), "process-monitor", durableAgentRecipeRequestToService(request))
		var phase *service.DurableAgentRecipeApplyError
		if result != nil || !errors.Is(err, store.ErrVerifiedActorRequired) || !errors.As(err, &phase) || phase.Stage != "create" {
			t.Fatalf("production apply = %+v, %v; want create-stage verified-authority refusal", result, err)
		}
		body, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest("POST", "/api/durable-agent-recipes/process-monitor/apply", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), store.ErrVerifiedActorRequired.Error()) {
			t.Fatalf("apply recipe = %d body=%s", w.Code, w.Body.String())
		}
		assertDurableAPIRefusalState(t, a, before)
	}
	if _, err := os.Stat(filepath.Join(a.Services.WorkingDir, ".nanite", "durable-agents")); !os.IsNotExist(err) {
		t.Fatalf("refused apply produced filesystem projection: %v", err)
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
