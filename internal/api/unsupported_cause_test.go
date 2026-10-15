package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

// A 422 must say why and what to do; the body is the error text.
func TestAgentV1CreateSession422NamesCauseAndRemedy(t *testing.T) {
	a, mux := newTestAPI(t)
	allowTestNativeModel(a)
	pin := a.Services.CognitiveViews.DefaultDefinitionRef
	host := func(slug string, enabled bool, runtime string) store.AgentHostSettings {
		t.Helper()
		settings := store.NativeHostSettings{Version: "1", Runtime: runtime, Provider: "fixture", Model: "fixture-model"}
		h, err := a.store.CreateAgentHostSettings(t.Context(), store.AgentHostSettings{Slug: slug, Title: slug, DefinitionRef: pin.MeshRef(), Settings: settings, Enabled: enabled, Source: "explicit-private-test-authoring"})
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	create := func(h store.AgentHostSettings, sel *service.ModelSelection) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(agentV1CreateSessionRequest{DefinitionRef: pin, HostSettings: &service.HostSettingsRef{ID: h.ID, Revision: h.Revision}, ModelSelection: sel})
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("POST", agentV1RoutePrefix+"/sessions", bytes.NewReader(raw)))
		return w
	}
	for name, tc := range map[string]struct {
		h    store.AgentHostSettings
		sel  *service.ModelSelection
		want []string
		code string
	}{
		"disabled host settings": {host("cause-disabled", false, "api"), nil, []string{"disabled", "enable"}, "host_disabled"},
		"runtime is not api":     {host("cause-cli", true, "cli"), nil, []string{`runtime "cli"`, `runtime "api"`}, "runtime_not_api"},
		"selection mismatch":     {host("cause-mismatch", true, "api"), &service.ModelSelection{Provider: "other", Model: "other-model"}, []string{"other/other-model", "fixture/fixture-model"}, "model_selection_mismatch"},
	} {
		t.Run(name, func(t *testing.T) {
			w := create(tc.h, tc.sel)
			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d %s", w.Code, w.Body.String())
			}
			var body struct {
				Error struct {
					Code    string `json:"code"`
					Cause   string `json:"cause"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Error.Code != "unsupported_selection" || body.Error.Cause != tc.code {
				t.Fatalf("code=%q cause=%q, want unsupported_selection/%s", body.Error.Code, body.Error.Cause, tc.code)
			}
			for _, want := range tc.want {
				if !strings.Contains(body.Error.Message, want) {
					t.Fatalf("message %q lacks %q", body.Error.Message, want)
				}
			}
		})
	}
}

func TestLogicalProvision422ReturnsTheCause(t *testing.T) {
	a, mux := newTestAPI(t)
	verified, err := service.EmbeddedDefinition()
	if err != nil {
		t.Fatal(err)
	}
	a.Services.CognitiveViews.Models = service.ModelAuthorizerFunc(func(context.Context, service.DefinitionRef, *service.ModelSelection) (service.ModelSelection, error) {
		return service.ModelSelection{}, fmt.Errorf("%w: provider %q is not a registered native provider", service.ErrUnsupportedModel, "nowhere")
	})
	raw, _ := json.Marshal(service.ProvisionGeneralChatRequest{Name: "General chat", Slug: "cause-chat", DefinitionRef: verified.Ref})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/agents/provision/general-chat", bytes.NewReader(raw)))
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "not a registered native provider") || strings.Contains(w.Body.String(), "provisioning refused") {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
}

// The body `agent setup` emits must be admitted by the real create path.
func TestSetupEmittedSessionBodyIsAdmitted(t *testing.T) {
	a, mux := newTestAPI(t)
	allowTestNativeModel(a)
	if _, err := a.store.UpsertKnownTool(t.Context(), "dev_read", "builtin", "available", "read"); err != nil {
		t.Fatal(err)
	}
	def := []byte("---\nschema_version: \"2\"\ndefinition_id: def:setup-admit\nrevision: \"1\"\nname: setup-admit\ndescription: Fixture.\nbehavior:\n  purpose: Complete the requested work.\nrequirements: {}\nharness_profile:\n  context: {}\n  permissions:\n    profile: default\ncontinuity:\n  mode: ephemeral\n---\nFixture.\n")
	res, err := a.store.SetupAgent(t.Context(), store.AgentSetup{Definition: def, Provider: "fixture", Model: "fixture-model", Tools: []string{"dev_read"}, GrantedBy: "cli:agent-setup", Reason: "test", Directories: []string{t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	ref := service.DefinitionRef{DefinitionID: res.DefinitionRef.ID, Revision: res.DefinitionRef.Revision, SemanticDigest: res.DefinitionRef.Digest}
	raw, _ := json.Marshal(map[string]any{"definition_ref": ref, "host_settings": service.HostSettingsRef{ID: res.HostSettings.ID, Revision: res.HostSettings.Revision}})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", agentV1RoutePrefix+"/sessions", bytes.NewReader(raw)))
	if w.Code != http.StatusCreated {
		t.Fatalf("setup's session body was refused: %d %s", w.Code, w.Body.String())
	}
}
