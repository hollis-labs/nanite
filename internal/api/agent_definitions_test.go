package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
	mesh "github.com/hollis-labs/substrate/mesh"
)

func TestPinnedDefinitionAuthorHostAndAdmissionWire(t *testing.T) {
	a, mux := newTestAPI(t)
	allowTestNativeModel(a)
	a.Services.AgentDefinitions.Models = a.Services.CognitiveViews.Models
	request := func(method, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(method, path, bytes.NewReader(data)))
		return w
	}
	catalog := request(http.MethodGet, "/api/agent-definitions", nil)
	if catalog.Code != 200 {
		t.Fatal(catalog.Body.String())
	}
	var defs []struct {
		Ref      mesh.DefinitionRef `json:"definition_ref"`
		Artifact string             `json:"artifact"`
	}
	if err := json.Unmarshal(catalog.Body.Bytes(), &defs); err != nil || len(defs) == 0 {
		t.Fatalf("catalog: %v %s", err, catalog.Body.String())
	}
	concrete := strings.ReplaceAll(defs[0].Artifact, defs[0].Ref.ID, "def:wire-authored")
	authored := request(http.MethodPost, "/api/agent-definitions/author", map[string]any{"concrete": concrete})
	if authored.Code != 200 {
		t.Fatal(authored.Body.String())
	}
	var output struct {
		Artifact string `json:"artifact"`
	}
	if err := json.Unmarshal(authored.Body.Bytes(), &output); err != nil {
		t.Fatal(err)
	}
	// Use the standalone archived default so installation needs no resource sidecar.
	base, err := service.EmbeddedDefinition()
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := a.store.GetAgentDefinitionArtifact(t.Context(), base.Ref.MeshRef())
	if err != nil {
		t.Fatal(err)
	}
	source := strings.ReplaceAll(string(artifact.Data), base.Ref.DefinitionID, "def:wire-installed")
	installed := request(http.MethodPost, "/api/agent-definitions", map[string]any{"artifact": source, "resources": []any{}})
	if installed.Code != 201 {
		t.Fatal(installed.Body.String())
	}
	var pin mesh.DefinitionRef
	if err = json.Unmarshal(installed.Body.Bytes(), &pin); err != nil {
		t.Fatal(err)
	}
	changed := request(http.MethodPost, "/api/agent-definitions", map[string]any{"artifact": source + "\nDifferent immutable bytes.\n"})
	if changed.Code != 409 {
		t.Fatalf("rebound pin: %d %s", changed.Code, changed.Body.String())
	}
	input := map[string]any{"title": "Wire host", "slug": "wire-host", "definition_ref": pin, "enabled": true, "settings": store.NativeHostSettings{Version: "1", Runtime: "api", Provider: "fixture", Model: "fixture-model"}}
	created := request(http.MethodPost, "/api/agent-host-settings", input)
	if created.Code != 201 {
		t.Fatal(created.Body.String())
	}
	var host store.AgentHostSettings
	if err = json.Unmarshal(created.Body.Bytes(), &host); err != nil {
		t.Fatal(err)
	}
	viewInput := map[string]any{"definition_ref": service.DefinitionRefFromMesh(pin), "host_settings": service.HostSettingsRef{ID: host.ID, Revision: host.Revision}, "metadata": map[string]any{"actor": "urn:claimed", "tools": "*"}}
	accepted := request(http.MethodPost, "/api/agent/v1/sessions", viewInput)
	if accepted.Code != 201 {
		t.Fatalf("native admission: %d %s", accepted.Code, accepted.Body.String())
	}
	before, err := a.store.ListSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	input["revision"] = host.Revision
	input["enabled"] = false
	updated := request(http.MethodPut, "/api/agent-host-settings/"+host.ID, input)
	if updated.Code != 200 {
		t.Fatal(updated.Body.String())
	}
	refused := request(http.MethodPost, "/api/agent/v1/sessions", viewInput)
	if refused.Code != 409 {
		t.Fatalf("stale host admission: %d %s", refused.Code, refused.Body.String())
	}
	after, err := a.store.ListSessions(context.Background())
	if err != nil || len(after) != len(before) {
		t.Fatalf("refused view mutated sessions: %v", err)
	}
	var actor string
	if err = a.store.DB.QueryRowContext(t.Context(), `SELECT actor_uri FROM agent_actor_bindings LIMIT 1`).Scan(&actor); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("host/view request actor result: %v", err)
	}
	for _, path := range []string{"/api/agents", "/api/agents/install", "/api/agents/old/sync", "/api/agents/old/procedures", "/api/agents/old/reflexes"} {
		w := request(http.MethodPost, path, map[string]any{})
		if w.Code != 410 {
			t.Fatalf("legacy authoring %s: %d", path, w.Code)
		}
	}
}

func TestDefinitionAuthorRefusesAmbiguousDocuments(t *testing.T) {
	a, mux := newTestAPI(t)
	original, err := service.EmbeddedDefinition()
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := a.store.GetAgentDefinitionArtifact(t.Context(), original.Ref.MeshRef())
	if err != nil {
		t.Fatal(err)
	}
	quoted, err := json.Marshal(string(artifact.Data))
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"concrete":` + string(quoted) + `,"concrete":` + string(quoted) + `}`, `{"concrete":` + string(quoted) + `,"role":null}`, `{"concrete":` + string(quoted) + `} {}`} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/agent-definitions/author", strings.NewReader(body)))
		if w.Code != http.StatusBadRequest {
			t.Fatal("ambiguous authoring accepted", w.Code, w.Body.String())
		}
	}
}
