package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func countRows(t *testing.T, a *API, table string) int {
	t.Helper()
	var n int
	if err := a.Services.Store.DB.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func TestAgentBuilderDryRun_DoesNotMutateStore(t *testing.T) {
	a, mux := newTestAPI(t)
	profile := &store.AgentProfile{Name: "Builder Profile", Slug: "builder-profile", SystemPrompt: "x"}
	if err := a.Services.Store.CreateAgent(context.Background(), profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	beforeProfiles := countRows(t, a, "agent_profiles")
	beforeInstances := countRows(t, a, "durable_agent_instances")
	beforeSessions := countRows(t, a, "sessions")

	body, _ := json.Marshal(AgentBuilderDryRunRequest{
		SchemaVersion: agentBuilderSchemaVersion,
		Mode:          agentBuilderModeCreateProfileAndInstance,
		Profile: AgentBuilderProfileInput{
			ID:             profile.ID,
			Name:           "Builder Preview",
			Slug:           "builder-preview",
			SystemPrompt:   "You are a preview agent.",
			RoleTools:      `["docs_search"]`,
			RoleSkills:     `["project_advisor"]`,
			ContextPolicy:  `{"window":"rolling"}`,
			Class:          "advisor",
			ActivationMode: "singleton",
			DefaultState:   "sleeping",
		},
		Capabilities: AgentBuilderCapabilitiesInput{
			AssignedSkillIDs: []string{"skill-1"},
			KnownTools: []AgentKnownToolUpsertRequest{
				{ToolName: "docs_search", Pinned: true},
			},
		},
		DurableInstance: AgentBuilderDurableInstanceInput{
			Create:         true,
			LifecycleClass: "advisor",
			Provider:       "anthropic",
			Model:          "claude-sonnet-4",
			RuntimeKind:    "api",
			WorkRoot:       "/tmp/preview",
			Start:          false,
		},
		OperatorNotification: AgentBuilderOperatorNotificationInput{
			TargetKind:   "operator",
			TargetID:     "chris",
			IncludeLinks: true,
		},
	})
	req := httptest.NewRequest("POST", "/api/agent-builder/dry-run", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/agent-builder/dry-run: expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	var resp AgentBuilderDryRunResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Valid {
		t.Fatalf("dry-run invalid: %+v", resp)
	}
	if resp.LaunchPlanPreview == nil {
		t.Fatalf("expected launch plan preview, got %+v", resp)
	}
	if resp.NotificationPreview.TargetID != "chris" || len(resp.NotificationPreview.Links) == 0 {
		t.Fatalf("notification preview = %+v", resp.NotificationPreview)
	}

	if got := countRows(t, a, "agent_profiles"); got != beforeProfiles {
		t.Fatalf("agent_profiles mutated: before=%d after=%d", beforeProfiles, got)
	}
	if got := countRows(t, a, "durable_agent_instances"); got != beforeInstances {
		t.Fatalf("durable_agent_instances mutated: before=%d after=%d", beforeInstances, got)
	}
	if got := countRows(t, a, "sessions"); got != beforeSessions {
		t.Fatalf("sessions mutated: before=%d after=%d", beforeSessions, got)
	}
}

func TestAgentBuilderDryRun_RecipePlanAndNoMutation(t *testing.T) {
	a, mux := newTestAPI(t)
	profile := &store.AgentProfile{Name: "Recipe Builder", Slug: "recipe-builder", SystemPrompt: "x"}
	if err := a.Services.Store.CreateAgent(context.Background(), profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	beforeInstances := countRows(t, a, "durable_agent_instances")
	body, _ := json.Marshal(AgentBuilderDryRunRequest{
		SchemaVersion: agentBuilderSchemaVersion,
		Mode:          agentBuilderModeCreateProfileAndInstance,
		Profile: AgentBuilderProfileInput{
			ID:           profile.ID,
			Name:         "Recipe Builder",
			Slug:         "recipe-builder",
			SystemPrompt: "You are recipe builder.",
		},
		DurableInstance: AgentBuilderDurableInstanceInput{
			Create:   true,
			RecipeID: "project-advisor",
			WorkRoot: "/tmp/recipe-builder",
		},
	})
	req := httptest.NewRequest("POST", "/api/agent-builder/dry-run", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/agent-builder/dry-run: expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	var resp AgentBuilderDryRunResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.DurableRecipePlan == nil || resp.DurableRecipePlan.RecipeID != "project-advisor" {
		t.Fatalf("recipe plan = %+v", resp.DurableRecipePlan)
	}
	if got := countRows(t, a, "durable_agent_instances"); got != beforeInstances {
		t.Fatalf("durable_agent_instances mutated: before=%d after=%d", beforeInstances, got)
	}
}

func TestAgentBuilderDraftAndReview_AreNoWriteDeterministic(t *testing.T) {
	a, mux := newTestAPI(t)
	beforeProfiles := countRows(t, a, "agent_profiles")
	beforeInstances := countRows(t, a, "durable_agent_instances")
	beforeSessions := countRows(t, a, "sessions")

	draftBody, _ := json.Marshal(AgentBuilderDraftRequest{
		SchemaVersion:           agentBuilderSchemaVersion,
		IntakeText:              "Create an agent that watches project health and summarizes blockers.",
		RequestedLifecycleClass: "process",
	})
	req := httptest.NewRequest("POST", "/api/agent-builder/draft", bytes.NewReader(draftBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/agent-builder/draft: expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	var draftResp AgentBuilderDraftResponse
	if err := json.NewDecoder(w.Body).Decode(&draftResp); err != nil {
		t.Fatalf("decode draft response: %v", err)
	}
	if draftResp.Draft.Profile.Name == "" || draftResp.Draft.DurableInstance.LifecycleClass != "process" {
		t.Fatalf("draft response = %+v", draftResp)
	}

	reviewBody, _ := json.Marshal(AgentBuilderReviewRequest{
		SchemaVersion: agentBuilderSchemaVersion,
		CurrentDraft:  draftResp.Draft,
	})
	req = httptest.NewRequest("POST", "/api/agent-builder/review", bytes.NewReader(reviewBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/agent-builder/review: expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	var reviewResp AgentBuilderReviewResponse
	if err := json.NewDecoder(w.Body).Decode(&reviewResp); err != nil {
		t.Fatalf("decode review response: %v", err)
	}
	if reviewResp.MaxRoundsRecommended != 3 {
		t.Fatalf("review response = %+v", reviewResp)
	}

	if got := countRows(t, a, "agent_profiles"); got != beforeProfiles {
		t.Fatalf("agent_profiles mutated: before=%d after=%d", beforeProfiles, got)
	}
	if got := countRows(t, a, "durable_agent_instances"); got != beforeInstances {
		t.Fatalf("durable_agent_instances mutated: before=%d after=%d", beforeInstances, got)
	}
	if got := countRows(t, a, "sessions"); got != beforeSessions {
		t.Fatalf("sessions mutated: before=%d after=%d", beforeSessions, got)
	}
}

func TestAgentBuilderDryRun_UpdateProfileRejectsInternal(t *testing.T) {
	a, mux := newTestAPI(t)
	internalAgent := &store.AgentProfile{
		Name:         "Internal Builder",
		Slug:         "internal-builder",
		SystemPrompt: "x",
		Source:       "internal",
	}
	if err := a.Services.Store.CreateAgent(context.Background(), internalAgent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	body, _ := json.Marshal(AgentBuilderDryRunRequest{
		SchemaVersion: agentBuilderSchemaVersion,
		Mode:          agentBuilderModeUpdateProfile,
		Profile: AgentBuilderProfileInput{
			ID:           internalAgent.ID,
			Name:         "Updated Internal",
			SystemPrompt: "Updated prompt",
		},
	})
	req := httptest.NewRequest("POST", "/api/agent-builder/dry-run", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/agent-builder/dry-run: expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	var resp AgentBuilderDryRunResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Valid {
		t.Fatalf("expected invalid dry-run for internal profile, got %+v", resp)
	}
}
