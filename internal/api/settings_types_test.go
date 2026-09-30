package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

var userSettingsViewKeys = []string{
	"allow_unsigned_plugins", "auto_repair_pref", "compaction_strategy",
	"context_budget_pct", "context_overflow_recovery", "context_window_tokens",
	"default_agent", "default_model", "default_provider", "developer_mode",
	"embedding_mode", "embedding_model", "embedding_provider", "embedding_status",
	"ext_settings", "provider_fallback_chain", "recover_mode",
	"subagent_approval_required", "subagent_approval_timeout_seconds",
	"subagent_runtime", "summarizer_model", "summarizer_provider", "task_backend",
	"tool_cache_enabled", "tool_call_display_mode", "tool_classifier_mode",
	"tool_classifier_model", "tool_classifier_provider",
	"tool_classifier_timeout_ms", "tool_drawer_retention", "tool_load_preferences",
	"tool_per_turn_cap", "tool_result_cache_ttl_seconds",
	"tool_result_hard_cap_bytes", "tool_result_soft_truncate_bytes",
	"tool_stream_behavior", "utility_model", "utility_provider",
}

// legacySettingsJSON is the response body the settings handlers built before
// UserSettingsView: the row marshaled, decoded into a map, embedding_status
// added, and the map marshaled again.
func legacySettingsJSON(t *testing.T, us *store.UserSettings, status string) []byte {
	t.Helper()
	raw := mustJSON(t, us)
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out["embedding_status"] = status
	return mustJSON(t, out)
}

// populatedUserSettings sets every scalar field to a distinct value and the
// collection fields to non-empty values.
func populatedUserSettings(t *testing.T) store.UserSettings {
	t.Helper()
	var us store.UserSettings
	rv := reflect.ValueOf(&us).Elem()
	for i := 0; i < rv.NumField(); i++ {
		f := rv.Field(i)
		switch f.Kind() {
		case reflect.String:
			f.SetString(fmt.Sprintf("value-%d", i))
		case reflect.Bool:
			f.SetBool(true)
		case reflect.Int:
			f.SetInt(int64(1000 + i))
		case reflect.Float64:
			f.SetFloat(float64(i) + 0.25)
		case reflect.Slice, reflect.Map:
			// set below
		default:
			t.Fatalf("store.UserSettings.%s has kind %s; teach populatedUserSettings about it", rv.Type().Field(i).Name, f.Kind())
		}
	}
	us.ProviderFallbackChain = []string{"anthropic", "openai"}
	us.ExtSettings = map[string]any{
		"display_name": "<Chris & co>",
		"nested":       map[string]any{"b": 2.5, "a": []any{"x", true, nil}},
		"count":        float64(3),
	}
	us.ToolLoadPreferences = map[string]string{"z_tool": "opt-in", "a_tool": "auto"}
	return us
}

func assertSettingsViewMatchesLegacy(t *testing.T, name string, us store.UserSettings) {
	t.Helper()
	got := mustJSON(t, userSettingsToView(&us, "active"))
	want := legacySettingsJSON(t, &us, "active")
	if !bytes.Equal(got, want) {
		t.Errorf("%s: view JSON differs from the legacy map JSON\n got: %s\nwant: %s", name, got, want)
	}
}

func TestUserSettingsViewJSON(t *testing.T) {
	full := populatedUserSettings(t)
	assertKeys(t, "UserSettingsView", mustJSON(t, userSettingsToView(&full, "active")), userSettingsViewKeys)
	assertSettingsViewMatchesLegacy(t, "populated", full)
	assertSettingsViewMatchesLegacy(t, "zero", store.UserSettings{})

	// ext_settings and tool_load_preferences drop out when empty, nil or
	// not; a nil provider_fallback_chain stays null.
	empty := store.UserSettings{ExtSettings: map[string]any{}, ToolLoadPreferences: map[string]string{}}
	assertSettingsViewMatchesLegacy(t, "empty maps", empty)
	raw := string(mustJSON(t, userSettingsToView(&empty, "disabled")))
	if strings.Contains(raw, "ext_settings") || strings.Contains(raw, "tool_load_preferences") {
		t.Errorf("empty maps not omitted: %s", raw)
	}
	if !strings.Contains(raw, `"provider_fallback_chain":null`) {
		t.Errorf("nil provider_fallback_chain not null: %s", raw)
	}

	// The legacy path carried ints through float64. Up to 2^53 that is
	// exact, so the view matches it byte for byte.
	big := full
	big.ToolResultHardCapBytes = 1<<53 - 1
	big.ContextWindowTokens = 1 << 53
	assertSettingsViewMatchesLegacy(t, "ints at 2^53", big)
}

// failingSettingsStore fails every settings read.
type failingSettingsStore struct{}

func (failingSettingsStore) GetUserSettings(context.Context) (*store.UserSettings, error) {
	return nil, errors.New("db down")
}

func (failingSettingsStore) UpdateUserSettings(context.Context, *store.UserSettings) error {
	return errors.New("db down")
}

func newFailingSettingsMux() *http.ServeMux {
	a := &API{Services: &service.Container{Settings: service.NewUserSettingsService(failingSettingsStore{})}}
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/settings", a.handleUpdateSettings)
	mux.HandleFunc("PUT /api/tools/load-preferences", a.handleUpdateToolLoadPreferences)
	return mux
}

func doJSON(mux http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func assertErrorBody(t *testing.T, w *httptest.ResponseRecorder, code int, msg string) {
	t.Helper()
	if w.Code != code {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, code, w.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body["error"] != msg {
		t.Fatalf("error = %q, want %q", body["error"], msg)
	}
}

func TestSettings_PutLoadFailureBeforeDecode(t *testing.T) {
	w := doJSON(newFailingSettingsMux(), "PUT", "/api/settings", `not json`)
	assertErrorBody(t, w, http.StatusInternalServerError, "failed to load current settings")
}

func TestSettings_PutFirstFieldErrorWins(t *testing.T) {
	_, mux := newSettingsTestAPI(t)
	// Both values are invalid. Fields are checked in handler order, not
	// body order, so tool_stream_behavior's message is the one returned.
	w := doJSON(mux, "PUT", "/api/settings", `{"embedding_mode":"bogus","tool_stream_behavior":"bogus"}`)
	assertErrorBody(t, w, http.StatusBadRequest, "tool_stream_behavior must be one of: streaming, persist, hidden")

	// A decode error on an earlier field beats an enum error on a later one.
	w = doJSON(mux, "PUT", "/api/settings", `{"tool_drawer_retention":7,"tool_stream_behavior":42}`)
	assertErrorBody(t, w, http.StatusBadRequest, "invalid value for field 'tool_stream_behavior'")
}

func TestSettings_PutResponseMatchesGet(t *testing.T) {
	_, mux := newSettingsTestAPI(t)
	put := doJSON(mux, "PUT", "/api/settings", `{"ext_settings":{"display_name":"x"},"tool_drawer_retention":15}`)
	if put.Code != http.StatusOK {
		t.Fatalf("PUT status = %d: %s", put.Code, put.Body.String())
	}
	get := doJSON(mux, "GET", "/api/settings", "")
	if get.Code != http.StatusOK {
		t.Fatalf("GET status = %d: %s", get.Code, get.Body.String())
	}
	if put.Body.String() != get.Body.String() {
		t.Fatalf("PUT and GET bodies differ\n put: %s\n get: %s", put.Body.String(), get.Body.String())
	}
	// No tool load preferences are set, so that key drops out.
	var want []string
	for _, k := range userSettingsViewKeys {
		if k != "tool_load_preferences" {
			want = append(want, k)
		}
	}
	assertKeys(t, "GET /api/settings", get.Body.Bytes(), want)
}

func TestToolLoadPreferences_BadValueBeforeLoad(t *testing.T) {
	w := doJSON(newFailingSettingsMux(), "PUT", "/api/tools/load-preferences", `{"t":"sometimes"}`)
	assertErrorBody(t, w, http.StatusBadRequest, `invalid load_type for tool t: must be "auto", "opt-in", or "" (remove)`)
}

func TestToolLoadPreferences_EmptyDeletes(t *testing.T) {
	_, mux := newSettingsTestAPI(t)
	if w := doJSON(mux, "PUT", "/api/tools/load-preferences", `{"a":"opt-in","b":"auto"}`); w.Code != http.StatusOK {
		t.Fatalf("seed PUT status = %d: %s", w.Code, w.Body.String())
	}
	w := doJSON(mux, "PUT", "/api/tools/load-preferences", `{"a":""}`)
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"b":"auto"}` {
		t.Fatalf("PUT = %d %s, want 200 {\"b\":\"auto\"}", w.Code, w.Body.String())
	}
	w = doJSON(mux, "GET", "/api/tools/load-preferences", "")
	if strings.TrimSpace(w.Body.String()) != `{"b":"auto"}` {
		t.Fatalf("GET = %s, want {\"b\":\"auto\"}", w.Body.String())
	}
	w = doJSON(mux, "PUT", "/api/tools/load-preferences", `{"b":""}`)
	if strings.TrimSpace(w.Body.String()) != `{}` {
		t.Fatalf("PUT removing the last override = %s, want {}", w.Body.String())
	}
}

func TestHandleGrantAgentTool_Precedence(t *testing.T) {
	a, mux := newTestAPI(t)
	agent := createTestAgentForGrant(t, mux, "grant-precedence-agent", nil)
	toolID, err := a.Services.Store.UpsertKnownTool(context.Background(), "zz_precedence_tool", "builtin", "available", "")
	if err != nil {
		t.Fatalf("UpsertKnownTool: %v", err)
	}

	// Unknown agent beats a bad body.
	w := doJSON(mux, "POST", "/api/agents/no-such-agent/tools", `not json`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown agent + bad body: status = %d, want 404; body: %s", w.Code, w.Body.String())
	}
	// Bad body beats a missing tool_id.
	w = doJSON(mux, "POST", "/api/agents/"+agent.ID+"/tools", `not json`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "invalid JSON: ") {
		t.Fatalf("bad body: %d %s, want 400 invalid JSON", w.Code, w.Body.String())
	}
	w = doJSON(mux, "POST", "/api/agents/"+agent.ID+"/tools", `{}`)
	assertErrorBody(t, w, http.StatusBadRequest, "tool_id is required")
	w = doJSON(mux, "POST", "/api/agents/"+agent.ID+"/tools", `{"tool_id":"no-such-tool"}`)
	assertErrorBody(t, w, http.StatusNotFound, "known tool not found")

	// A grant with no granted_via is recorded as explicit.
	w = doJSON(mux, "POST", "/api/agents/"+agent.ID+"/tools", `{"tool_id":"`+toolID+`"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("grant: %d %s", w.Code, w.Body.String())
	}
	var via string
	if err := a.Services.Store.DB.QueryRow(`SELECT granted_via FROM agent_tools WHERE agent_id = ? AND tool_id = ?`, agent.ID, toolID).Scan(&via); err != nil {
		t.Fatalf("read grant: %v", err)
	}
	if via != "explicit" {
		t.Fatalf("granted_via = %q, want explicit", via)
	}

	// Revoke: unknown agent is 404, then the grant goes.
	w = doJSON(mux, "DELETE", "/api/agents/no-such-agent/tools/"+toolID, "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("revoke unknown agent: %d %s", w.Code, w.Body.String())
	}
	w = doJSON(mux, "DELETE", "/api/agents/"+agent.ID+"/tools/"+toolID, "")
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"status":"revoked"}` {
		t.Fatalf("revoke: %d %s", w.Code, w.Body.String())
	}
}
