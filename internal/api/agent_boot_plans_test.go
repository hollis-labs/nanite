package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestAgentBootPlanGetReturnsEmptyDefault(t *testing.T) {
	a, mux := newTestAPI(t)
	agent := &store.AgentProfile{Name: "Bootless", Slug: "bootless", SystemPrompt: "x"}
	if err := a.Services.Store.CreateAgent(agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agents/"+agent.ID+"/boot-plan", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET boot-plan: got %d body=%s", w.Code, w.Body.String())
	}
	var doc store.AgentBootPlanDocument
	if err := json.NewDecoder(w.Body).Decode(&doc); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if doc.AgentID != agent.ID || doc.SchemaVersion != store.AgentBootPlanSchemaVersion1 {
		t.Fatalf("unexpected empty doc: %+v", doc)
	}
	if len(doc.PlantItems) != 0 || len(doc.Callbacks) != 0 {
		t.Fatalf("expected empty lists, got %+v", doc)
	}
}

func TestAgentBootPlanPutGetDelete(t *testing.T) {
	a, mux := newTestAPI(t)
	agent := &store.AgentProfile{Name: "Bootful", Slug: "bootful", SystemPrompt: "x"}
	if err := a.Services.Store.CreateAgent(agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	doc := store.AgentBootPlanDocument{
		AgentID:       agent.ID,
		SchemaVersion: store.AgentBootPlanSchemaVersion1,
		PlantItems: []store.AgentBootPlantItem{
			{
				ID:              "boot-readme",
				Name:            "Boot README",
				SourceKind:      "literal_file",
				Content:         "# hello\n",
				TargetRelPath:   "docs/README.md",
				EntryKind:       "file",
				Timing:          []string{"create", "start"},
				OverwritePolicy: "if_missing",
				FailurePolicy:   "fail_boot",
				Enabled:         true,
			},
		},
		Callbacks: []store.AgentBootCallback{
			{
				ID:             "post-boot-note",
				Name:           "Post Boot Note",
				Timing:         "after_boot",
				CallbackType:   "message_injection",
				Message:        "Boot complete",
				TimeoutSeconds: 30,
				FailurePolicy:  "warn",
				Enabled:        true,
			},
		},
	}
	body, _ := json.Marshal(doc)
	req := httptest.NewRequest(http.MethodPut, "/api/agents/"+agent.ID+"/boot-plan", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT boot-plan: got %d body=%s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/agents/"+agent.ID+"/boot-plan", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET boot-plan after put: got %d body=%s", w.Code, w.Body.String())
	}
	var got store.AgentBootPlanDocument
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	if len(got.PlantItems) != 1 || got.PlantItems[0].TargetRelPath != "docs/README.md" {
		t.Fatalf("unexpected stored plan: %+v", got)
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/agents/"+agent.ID+"/boot-plan", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("DELETE boot-plan: got %d body=%s", w.Code, w.Body.String())
	}
}

func TestAgentBootPlanPutRejectsUnsafeTarget(t *testing.T) {
	a, mux := newTestAPI(t)
	agent := &store.AgentProfile{Name: "Unsafe", Slug: "unsafe", SystemPrompt: "x"}
	if err := a.Services.Store.CreateAgent(agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	body, _ := json.Marshal(store.AgentBootPlanDocument{
		PlantItems: []store.AgentBootPlantItem{
			{
				ID:              "escape",
				Name:            "Escape",
				SourceKind:      "literal_file",
				Content:         "x",
				TargetRelPath:   "../escape.txt",
				EntryKind:       "file",
				Timing:          []string{"create"},
				OverwritePolicy: "if_missing",
				FailurePolicy:   "fail_boot",
				Enabled:         true,
			},
		},
	})
	req := httptest.NewRequest(http.MethodPut, "/api/agents/"+agent.ID+"/boot-plan", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("PUT invalid boot-plan: got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "target_rel_path") {
		t.Fatalf("expected target_rel_path validation in body: %s", w.Body.String())
	}
}

func TestAgentBootPlanDryRunRedactsSecretAndFlagsCallbacks(t *testing.T) {
	a, mux := newTestAPI(t)
	agent := &store.AgentProfile{Name: "Dry Runner", Slug: "dry-runner", SystemPrompt: "x"}
	if err := a.Services.Store.CreateAgent(agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	body, _ := json.Marshal(store.AgentBootPlanDocument{
		PlantItems: []store.AgentBootPlantItem{
			{
				ID:              "secret-file",
				Name:            "Secret File",
				SourceKind:      "literal_file",
				Content:         "top-secret",
				TargetRelPath:   "secrets/token.txt",
				EntryKind:       "file",
				Timing:          []string{"create"},
				Secret:          true,
				OverwritePolicy: "always",
				FailurePolicy:   "warn",
				Enabled:         true,
			},
		},
		Callbacks: []store.AgentBootCallback{
			{
				ID:             "curl-home",
				Name:           "Curl Home",
				Timing:         "after_boot",
				CallbackType:   "http_request",
				Request:        &store.AgentBootRequestSpec{Method: "POST", URL: "https://example.com/boot"},
				TimeoutSeconds: 30,
				FailurePolicy:  "warn",
				Enabled:        true,
				Env:            map[string]string{"TOKEN": "secret"},
			},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/agents/"+agent.ID+"/boot-plan/dry-run", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("POST boot-plan dry-run: got %d body=%s", w.Code, w.Body.String())
	}
	var resp AgentBootPlanDryRunResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode dry-run response: %v", err)
	}
	if !resp.Valid {
		t.Fatalf("dry-run unexpectedly invalid: %+v", resp)
	}
	if len(resp.PlantOperations) != 1 || !resp.PlantOperations[0].ContentPreviewRedacted {
		t.Fatalf("secret plant item not redacted: %+v", resp.PlantOperations)
	}
	if len(resp.CallbackOrder) != 1 || !resp.CallbackOrder[0].EnvRedacted {
		t.Fatalf("callback env not redacted: %+v", resp.CallbackOrder)
	}
	if len(resp.UnsupportedNotes) == 0 {
		t.Fatalf("expected unsupported callback note, got %+v", resp)
	}
}

func TestAgentBuilderDryRunCarriesBootPlanPreview(t *testing.T) {
	a, mux := newTestAPI(t)
	profile := &store.AgentProfile{Name: "Builder Boot", Slug: "builder-boot", SystemPrompt: "x"}
	if err := a.Services.Store.CreateAgent(profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	body, _ := json.Marshal(AgentBuilderDryRunRequest{
		SchemaVersion: agentBuilderSchemaVersion,
		Mode:          agentBuilderModeCreateProfile,
		Profile: AgentBuilderProfileInput{
			ID:           profile.ID,
			Name:         "Builder Boot",
			Slug:         "builder-boot",
			SystemPrompt: "x",
		},
		BootPlan: &store.AgentBootPlanDocument{
			PlantItems: []store.AgentBootPlantItem{
				{
					ID:              "builder-readme",
					Name:            "Builder README",
					SourceKind:      "literal_file",
					Content:         "hi",
					TargetRelPath:   "README.md",
					EntryKind:       "file",
					Timing:          []string{"create"},
					OverwritePolicy: "if_missing",
					FailurePolicy:   "fail_boot",
					Enabled:         true,
				},
			},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/agent-builder/dry-run", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("POST agent-builder dry-run: got %d body=%s", w.Code, w.Body.String())
	}
	var resp AgentBuilderDryRunResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode builder dry-run response: %v", err)
	}
	if resp.BootPlanPreview == nil || !resp.BootPlanPreview.Valid {
		t.Fatalf("expected valid boot plan preview, got %+v", resp.BootPlanPreview)
	}
}
