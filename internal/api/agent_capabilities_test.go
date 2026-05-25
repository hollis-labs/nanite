package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestAgentCapabilitiesAPI_KnownToolsCRUD(t *testing.T) {
	a, mux := newTestAPI(t)
	agentID := seedCapabilityAgent(t, a, store.AgentProfile{
		Name:         "Known Tool Agent",
		Slug:         "known-tool-agent",
		SystemPrompt: "x",
	})

	req := httptest.NewRequest("POST", "/api/agents/"+agentID+"/known-tools", bytes.NewBufferString(`{
		"tool_name":"torque_task_create",
		"pinned":true,
		"sort_order":7,
		"ttl_seconds":3600,
		"reason":"operator pin"
	}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create known tool: %d %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest("GET", "/api/agents/"+agentID+"/known-tools", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "torque_task_create") {
		t.Fatalf("list known tools: %d %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest("GET", "/api/agents/"+agentID+"/known-tools/torque_task_create", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get known tool: %d %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest("PUT", "/api/agents/"+agentID+"/known-tools/torque_task_create", bytes.NewBufferString(`{
		"tool_name":"torque_task_create",
		"pinned":false,
		"sort_order":1,
		"ttl_seconds":120,
		"reason":"cooldown"
	}`))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update known tool: %d %s", w.Code, w.Body.String())
	}
	row, err := a.Services.Store.GetAgentKnownTool(context.Background(), agentID, "torque_task_create")
	if err != nil {
		t.Fatalf("GetAgentKnownTool: %v", err)
	}
	if row.Pinned || row.SortOrder != 1 || row.TTLSeconds != 120 || row.Reason != "cooldown" {
		t.Fatalf("updated known tool mismatch: %+v", row)
	}

	req = httptest.NewRequest("DELETE", "/api/agents/"+agentID+"/known-tools/torque_task_create", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("delete known tool: %d %s", w.Code, w.Body.String())
	}
}

func TestAgentCapabilitiesAPI_KnownSkillsCRUD(t *testing.T) {
	a, mux := newTestAPI(t)
	agentID := seedCapabilityAgent(t, a, store.AgentProfile{
		Name:         "Known Skill Agent",
		Slug:         "known-skill-agent",
		SystemPrompt: "x",
	})

	req := httptest.NewRequest("POST", "/api/agents/"+agentID+"/known-skills", bytes.NewBufferString(`{
		"skill_name":"project_advisor",
		"pinned":true,
		"ttl_seconds":600,
		"reason":"role carry"
	}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create known skill: %d %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest("GET", "/api/agents/"+agentID+"/known-skills/project_advisor", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get known skill: %d %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest("PUT", "/api/agents/"+agentID+"/known-skills/project_advisor", bytes.NewBufferString(`{
		"skill_name":"project_advisor",
		"pinned":false,
		"ttl_seconds":0,
		"reason":"manual reset"
	}`))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update known skill: %d %s", w.Code, w.Body.String())
	}
	row, err := a.Services.Store.GetAgentKnownSkill(context.Background(), agentID, "project_advisor")
	if err != nil {
		t.Fatalf("GetAgentKnownSkill: %v", err)
	}
	if row.Pinned || row.Reason != "manual reset" {
		t.Fatalf("updated known skill mismatch: %+v", row)
	}

	req = httptest.NewRequest("DELETE", "/api/agents/"+agentID+"/known-skills/project_advisor", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("delete known skill: %d %s", w.Code, w.Body.String())
	}
}

func TestAgentCapabilitiesAPI_ProceduresCRUD(t *testing.T) {
	a, mux := newTestAPI(t)
	agentID := seedCapabilityAgent(t, a, store.AgentProfile{
		Name:         "Procedure Agent",
		Slug:         "procedure-agent",
		SystemPrompt: "x",
	})

	req := httptest.NewRequest("POST", "/api/agents/"+agentID+"/procedures", bytes.NewBufferString(`{
		"name":"checklist",
		"body":"Step 1\nStep 2",
		"scope":"agent"
	}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create procedure: %d %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest("GET", "/api/agents/"+agentID+"/procedures/checklist", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get procedure: %d %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest("PUT", "/api/agents/"+agentID+"/procedures/checklist", bytes.NewBufferString(`{
		"name":"checklist",
		"body":"Updated body",
		"scope":"shared"
	}`))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update procedure: %d %s", w.Code, w.Body.String())
	}
	row, err := a.Services.Store.GetAgentProcedure(context.Background(), agentID, "checklist")
	if err != nil {
		t.Fatalf("GetAgentProcedure: %v", err)
	}
	if row.Body != "Updated body" || row.Scope != "shared" {
		t.Fatalf("updated procedure mismatch: %+v", row)
	}

	req = httptest.NewRequest("DELETE", "/api/agents/"+agentID+"/procedures/checklist", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("delete procedure: %d %s", w.Code, w.Body.String())
	}
}

func TestAgentCapabilitiesAPI_KnowledgeSeedsCRUDAndMarkApplied(t *testing.T) {
	a, mux := newTestAPI(t)
	agentID := seedCapabilityAgent(t, a, store.AgentProfile{
		Name:         "Knowledge Agent",
		Slug:         "knowledge-agent",
		SystemPrompt: "x",
	})

	req := httptest.NewRequest("POST", "/api/agents/"+agentID+"/knowledge-seeds", bytes.NewBufferString(`{
		"seed_key":"boot-conventions",
		"namespace":"user/chrispian/knowledge",
		"body":"Prefer rg.",
		"tags":["shell","boot"]
	}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create knowledge seed: %d %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest("GET", "/api/agents/"+agentID+"/knowledge-seeds/boot-conventions", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get knowledge seed: %d %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest("PUT", "/api/agents/"+agentID+"/knowledge-seeds/boot-conventions", bytes.NewBufferString(`{
		"seed_key":"boot-conventions",
		"namespace":"user/chrispian/knowledge",
		"body":"Prefer rg and fd.",
		"tags_json":"[\"shell\",\"search\"]"
	}`))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update knowledge seed: %d %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest("POST", "/api/agents/"+agentID+"/knowledge-seeds/boot-conventions/mark-applied", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("mark applied: %d %s", w.Code, w.Body.String())
	}
	row, err := a.Services.Store.GetAgentKnowledgeSeed(context.Background(), agentID, "boot-conventions")
	if err != nil {
		t.Fatalf("GetAgentKnowledgeSeed: %v", err)
	}
	if row.AppliedAt == "" || row.TagsJSON != `["shell","search"]` {
		t.Fatalf("updated knowledge seed mismatch: %+v", row)
	}

	req = httptest.NewRequest("DELETE", "/api/agents/"+agentID+"/knowledge-seeds/boot-conventions", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("delete knowledge seed: %d %s", w.Code, w.Body.String())
	}
}

func TestAgentCapabilitiesAPI_MissingAgentReturns404(t *testing.T) {
	_, mux := newTestAPI(t)
	req := httptest.NewRequest("GET", "/api/agents/missing/known-tools", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing agent status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
}

func TestAgentCapabilitiesAPI_MissingRowsReturn404(t *testing.T) {
	a, mux := newTestAPI(t)
	agentID := seedCapabilityAgent(t, a, store.AgentProfile{
		Name:         "Missing Row Agent",
		Slug:         "missing-row-agent",
		SystemPrompt: "x",
	})

	paths := []string{
		"/api/agents/" + agentID + "/known-tools/missing",
		"/api/agents/" + agentID + "/known-skills/missing",
		"/api/agents/" + agentID + "/procedures/missing",
		"/api/agents/" + agentID + "/knowledge-seeds/missing",
	}
	for _, path := range paths {
		req := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404; body=%s", path, w.Code, w.Body.String())
		}
	}
}

func TestAgentCapabilitiesAPI_PathBodyMismatchRejected(t *testing.T) {
	a, mux := newTestAPI(t)
	agentID := seedCapabilityAgent(t, a, store.AgentProfile{
		Name:         "Mismatch Agent",
		Slug:         "mismatch-agent",
		SystemPrompt: "x",
	})
	if err := a.Services.Store.InsertAgentKnownTool(context.Background(), store.AgentKnownTool{
		AgentID:  agentID,
		ToolName: "torque_task_create",
	}); err != nil {
		t.Fatalf("InsertAgentKnownTool: %v", err)
	}

	req := httptest.NewRequest("PUT", "/api/agents/"+agentID+"/known-tools/torque_task_create", bytes.NewBufferString(`{
		"tool_name":"different_name"
	}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("mismatch status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
}

func TestAgentCapabilitiesAPI_InternalAgentMutationsConflict(t *testing.T) {
	a, mux := newTestAPI(t)
	agentID := seedCapabilityAgent(t, a, store.AgentProfile{
		Name:         "Internal Capability Agent",
		Slug:         "internal-capability-agent",
		SystemPrompt: "x",
		Source:       "internal",
		SourceRef:    "embedded:profiles/internal-capability-agent.md",
	})

	req := httptest.NewRequest("POST", "/api/agents/"+agentID+"/known-tools", bytes.NewBufferString(`{
		"tool_name":"torque_task_create"
	}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("internal mutation status = %d, want 409; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"manage_class":"internal"`) {
		t.Fatalf("internal mutation response missing manage_class=internal: %s", w.Body.String())
	}
}

func seedCapabilityAgent(t *testing.T, a *API, profile store.AgentProfile) string {
	t.Helper()
	if err := a.Services.Store.CreateAgent(&profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	return profile.ID
}

func TestAgentCapabilitiesAPI_ListKnowledgeSeedsResponseShape(t *testing.T) {
	a, mux := newTestAPI(t)
	agentID := seedCapabilityAgent(t, a, store.AgentProfile{
		Name:         "List Seed Agent",
		Slug:         "list-seed-agent",
		SystemPrompt: "x",
	})
	if err := a.Services.Store.InsertAgentKnowledgeSeed(context.Background(), store.AgentKnowledgeSeed{
		AgentID:   agentID,
		SeedKey:   "alpha",
		Namespace: "user/demo",
		Body:      "hello",
		TagsJSON:  `["one"]`,
	}); err != nil {
		t.Fatalf("InsertAgentKnowledgeSeed: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/agents/"+agentID+"/knowledge-seeds", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list knowledge seeds: %d %s", w.Code, w.Body.String())
	}
	var rows []store.AgentKnowledgeSeed
	if err := json.NewDecoder(w.Body).Decode(&rows); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(rows) != 1 || rows[0].TagsJSON != `["one"]` {
		t.Fatalf("knowledge seed list mismatch: %+v", rows)
	}
}
