package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

// A CLI session's stop kills the tracked process and reports the canceled turn.
func TestAgentV1StopCLISessionKillsTrackedProcessAndCancelsTurn(t *testing.T) {
	host := t.TempDir()
	a, mux, w := cliSessionIn(t, host, "")
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", w.Code, w.Body.String())
	}
	var created agentV1SessionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	id := created.Session.ID
	var canceled []string
	a.Services.Chat = &agentChatStub{cancelActiveGenerationFn: func(s string) bool { canceled = append(canceled, s); return true }}

	child := exec.Command("sleep", "60")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill() })
	a.Services.ProcessTracker.Track(id, child.Process)
	exited := make(chan struct{})
	go func() { _ = child.Wait(); close(exited) }()

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", agentV1RoutePrefix+"/sessions/"+id+"/stop", nil))
	var out agentV1StopResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || rec.Code != 200 || !out.Stopped || !out.TurnCanceled {
		t.Fatalf("stop = %d %s", rec.Code, rec.Body.String())
	}
	if len(canceled) != 1 || canceled[0] != id {
		t.Fatalf("canceled = %v", canceled)
	}
	select {
	case <-exited:
	case <-time.After(10 * time.Second):
		t.Fatal("the tracked process was still running after stop")
	}
}

// Concurrent stops: exactly one reports stopped=true.
func TestAgentV1ConcurrentStopsReportOneStop(t *testing.T) {
	a, mux := newTestAPI(t)
	allowTestNativeModel(a)
	body, _ := json.Marshal(agentV1CreateSessionRequest{DefinitionRef: a.Services.CognitiveViews.DefaultDefinitionRef})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", agentV1RoutePrefix+"/sessions", bytes.NewReader(body)))
	var created agentV1SessionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil || w.Code != http.StatusCreated {
		t.Fatal(w.Code, w.Body.String())
	}
	var stopped atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest("POST", agentV1RoutePrefix+"/sessions/"+created.Session.ID+"/stop", nil))
			var out agentV1StopResponse
			_ = json.Unmarshal(rec.Body.Bytes(), &out)
			if rec.Code == 200 && out.Stopped {
				stopped.Add(1)
			}
		}()
	}
	wg.Wait()
	if stopped.Load() != 1 {
		t.Fatalf("%d stops reported stopped=true, want 1", stopped.Load())
	}
}

// An ended session cannot be reached again through the retained reboot/recover routes.
func TestEndedSessionRefusesRebootAndRecover(t *testing.T) {
	a, mux := newTestAPI(t)
	allowTestNativeModel(a)
	body, _ := json.Marshal(agentV1CreateSessionRequest{DefinitionRef: a.Services.CognitiveViews.DefaultDefinitionRef})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", agentV1RoutePrefix+"/sessions", bytes.NewReader(body)))
	var created agentV1SessionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil || w.Code != http.StatusCreated {
		t.Fatal(w.Code, w.Body.String())
	}
	id := created.Session.ID
	mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", agentV1RoutePrefix+"/sessions/"+id+"/stop", nil))
	for _, path := range []string{"/api/sessions/" + id + "/agent/reboot", "/api/sessions/" + id + "/recover"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("POST", path, nil))
		if rec.Code != http.StatusConflict {
			t.Fatalf("POST %s = %d: %s", path, rec.Code, rec.Body.String())
		}
	}
	// Retry and agent-message are refused for any defined view, ended or not.
	for _, path := range []string{"/api/sessions/" + id + "/retry", "/api/sessions/" + id + "/agent-message"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("POST", path, strings.NewReader(`{"from_session_id":"x","content":"y"}`)))
		if rec.Code == http.StatusOK || rec.Code == http.StatusAccepted {
			t.Fatalf("POST %s revived an ended session: %d %s", path, rec.Code, rec.Body.String())
		}
	}
}
