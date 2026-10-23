package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

func cliSessionIn(t *testing.T, hostDir, workRoot string) (*testAPI, http.Handler, *httptest.ResponseRecorder) {
	t.Helper()
	a, mux := newTestAPI(t)
	embedded, err := service.EmbeddedDefinition()
	if err != nil {
		t.Fatal(err)
	}
	host, err := a.store.CreateAgentHostSettings(t.Context(), store.AgentHostSettings{Slug: "cli-root", Title: "CLI root", Source: "test", DefinitionRef: embedded.Ref.MeshRef(), Enabled: true,
		Settings: store.NativeHostSettings{Version: "1", Runtime: "cli", Provider: "claude", Model: "haiku", Directories: []string{hostDir}}})
	if err != nil {
		t.Fatal(err)
	}
	req := map[string]any{"definition_ref": service.DefinitionRefFromMesh(embedded.Ref.MeshRef()), "host_settings": service.HostSettingsRef{ID: host.ID, Revision: host.Revision}}
	if workRoot != "" {
		req["work_root"] = workRoot
	}
	body, _ := json.Marshal(req)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/agent/v1/sessions", bytes.NewReader(body)))
	return a, mux, w
}

func TestAgentV1WorkRootValidation(t *testing.T) {
	host := t.TempDir()
	sub := filepath.Join(host, "sub")
	if err := os.Mkdir(sub, 0o750); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(host, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		root  string
		want  int
		cause string
	}{
		"inside host directory": {sub, http.StatusCreated, ""},
		"host directory itself": {host, http.StatusCreated, ""},
		"relative":              {"sub", http.StatusUnprocessableEntity, service.CauseWorkRootInvalid},
		"outside host dirs":     {t.TempDir(), http.StatusUnprocessableEntity, service.CauseWorkRootInvalid},
		"missing":               {filepath.Join(host, "nope"), http.StatusUnprocessableEntity, service.CauseWorkRootInvalid},
		"a file":                {file, http.StatusUnprocessableEntity, service.CauseWorkRootInvalid},
		"dotdot escape":         {filepath.Join(sub, "..", ".."), http.StatusUnprocessableEntity, service.CauseWorkRootInvalid},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, w := cliSessionIn(t, host, tc.root)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
			if tc.cause != "" {
				if _, cause := cliErrorBody(t, w); cause != tc.cause {
					t.Fatalf("cause = %q, want %q: %s", cause, tc.cause, w.Body.String())
				}
			}
		})
	}
}

func TestAgentV1WorkRootRefusedForNonCLIHost(t *testing.T) {
	a, mux := newTestAPI(t)
	allowTestNativeModel(a)
	body, _ := json.Marshal(map[string]any{"definition_ref": a.Services.CognitiveViews.DefaultDefinitionRef, "work_root": t.TempDir()})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/agent/v1/sessions", bytes.NewReader(body)))
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if _, cause := cliErrorBody(t, w); cause != service.CauseWorkRootUnsupported {
		t.Fatalf("cause = %q: %s", cause, w.Body.String())
	}
}

func TestAgentV1StopIsIdempotentAndEndsTheSession(t *testing.T) {
	a, mux := newTestAPI(t)
	allowTestNativeModel(a)
	body, _ := json.Marshal(agentV1CreateSessionRequest{DefinitionRef: a.Services.CognitiveViews.DefaultDefinitionRef})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", agentV1RoutePrefix+"/sessions", bytes.NewReader(body)))
	if w.Code != http.StatusCreated {
		t.Fatal(w.Code, w.Body.String())
	}
	var created agentV1SessionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	id := created.Session.ID
	stop := func() (int, agentV1StopResponse) {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("POST", agentV1RoutePrefix+"/sessions/"+id+"/stop", nil))
		var out agentV1StopResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	if code, out := stop(); code != 200 || !out.Stopped || out.State != "ended" {
		t.Fatalf("first stop = %d %+v", code, out)
	}
	if code, out := stop(); code != 200 || out.Stopped || out.State != "ended" {
		t.Fatalf("second stop = %d %+v", code, out)
	}
	sess, err := a.Services.Sessions.Get(t.Context(), id)
	if err != nil || sess.Status != "archived" {
		t.Fatalf("session = %+v, %v", sess, err)
	}
	turn, _ := json.Marshal(map[string]any{"content": []map[string]string{{"kind": "text", "text": "hi"}}, "delivery": "at_idle"})
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", agentV1RoutePrefix+"/sessions/"+id+"/turns", bytes.NewReader(turn)))
	if w.Code != http.StatusConflict {
		t.Fatalf("turn after stop = %d: %s", w.Code, w.Body.String())
	}
	if code, _ := cliErrorBody(t, w); code != "invalid_state" {
		t.Fatalf("code = %q: %s", code, w.Body.String())
	}
}

func TestAgentV1StopUnknownSessionIs404(t *testing.T) {
	_, mux := newTestAPI(t)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", agentV1RoutePrefix+"/sessions/nope/stop", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
}
