package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

func cliSession(t *testing.T, settings store.NativeHostSettings) *httptest.ResponseRecorder {
	t.Helper()
	a, mux := newTestAPI(t)
	embedded, err := service.EmbeddedDefinition()
	if err != nil {
		t.Fatal(err)
	}
	host, err := a.store.CreateAgentHostSettings(t.Context(), store.AgentHostSettings{Slug: "cli-wire", Title: "CLI wire", Source: "test", DefinitionRef: embedded.Ref.MeshRef(), Enabled: true, Settings: settings})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"definition_ref": service.DefinitionRefFromMesh(embedded.Ref.MeshRef()), "host_settings": service.HostSettingsRef{ID: host.ID, Revision: host.Revision}})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/agent/v1/sessions", bytes.NewReader(body)))
	return w
}

func cliErrorBody(t *testing.T, w *httptest.ResponseRecorder) (code, cause string) {
	t.Helper()
	var out struct {
		Error struct{ Code, Cause string }
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	return out.Error.Code, out.Error.Cause
}

// A CLI host that cannot run is refused with the reason in the body, not an
// opaque 422.
func TestAgentV1CLISessionRefusalNamesItsCause(t *testing.T) {
	for name, tc := range map[string]struct {
		settings store.NativeHostSettings
		cause    string
	}{
		"no directory":     {store.NativeHostSettings{Version: "1", Runtime: "cli", Provider: "claude", Model: "haiku"}, service.CauseHostDirectoriesMissing},
		"unknown provider": {store.NativeHostSettings{Version: "1", Runtime: "cli", Provider: "no-such-cli", Directories: []string{"/tmp"}}, service.CauseProviderNotLaunchable},
		"mcp servers":      {store.NativeHostSettings{Version: "1", Runtime: "cli", Provider: "claude", Directories: []string{"/tmp"}, MCPServers: []string{"x"}}, service.CauseCLIHostUnsupportedField},
	} {
		t.Run(name, func(t *testing.T) {
			w := cliSession(t, tc.settings)
			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
			}
			if code, cause := cliErrorBody(t, w); code != "unsupported_selection" || cause != tc.cause {
				t.Fatalf("code=%q cause=%q, want unsupported_selection/%s (body %s)", code, cause, tc.cause, w.Body.String())
			}
		})
	}
}

func TestAgentV1CLISessionAdmitsAValidHost(t *testing.T) {
	w := cliSession(t, store.NativeHostSettings{Version: "1", Runtime: "cli", Provider: "claude", Model: "haiku", Directories: []string{t.TempDir()}})
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
}

func TestAgentV1CapabilitiesAdvertiseTheCLIRuntime(t *testing.T) {
	_, mux := newTestAPI(t)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/agent/v1/capabilities", nil))
	var caps agentV1CapabilitiesResponse
	if err := json.NewDecoder(w.Body).Decode(&caps); err != nil {
		t.Fatal(err)
	}
	var cli *runtimeKindOption
	for i := range caps.RuntimeKinds {
		if caps.RuntimeKinds[i].Value == "cli" {
			cli = &caps.RuntimeKinds[i]
		}
	}
	want := []string{"claude", "codex", "opencode", "agy"}
	if cli == nil || !reflect.DeepEqual(cli.Providers, want) {
		t.Fatalf("runtime_kinds = %+v, want cli with providers %v", caps.RuntimeKinds, want)
	}
}
