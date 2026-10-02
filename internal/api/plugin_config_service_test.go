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

func TestPluginConfigHTTP_MergeAndDefaults(t *testing.T) {
	a, mux := newTestAPI(t)
	req := httptest.NewRequest(http.MethodGet, "/api/plugin-config/missing", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || rr.Body.String() != `{"plugin_id":"missing","schema":[],"settings":{}}`+"\n" {
		t.Fatalf("defaults: %d %s", rr.Code, rr.Body.String())
	}
	if err := a.Services.Store.UpsertPluginSchema(t.Context(), "probe", []store.ConfigField{{Key: "label", Type: "string", Label: "Label", Default: "default", Required: true, Options: []string{"first", "second"}, Component: "picker"}}); err != nil {
		t.Fatal(err)
	}
	if err := a.Services.Store.UpsertPluginSettings(t.Context(), "probe", map[string]any{"keep": "retained", "label": "old"}); err != nil {
		t.Fatal(err)
	}
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/plugin-config/probe", bytes.NewBufferString(`{"label":"new"}`)))
	if rr.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rr.Code, rr.Body.String())
	}
	var got PluginSettingsView
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.PluginID != "probe" || got.Settings["keep"] != "retained" || got.Settings["label"] != "new" || got.Schema[0].Component != "picker" || !got.Schema[0].Required {
		t.Fatalf("response: %+v", got)
	}
	stored, err := a.Services.Store.GetPluginSettings(t.Context(), "probe")
	if err != nil || stored.Settings["label"] != "new" {
		t.Fatalf("stored: %+v %v", stored, err)
	}
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/plugin-config", nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"plugin_id":"probe"`) {
		t.Fatalf("list: %d %s", rr.Code, rr.Body.String())
	}
}

func TestPluginSettingsViewJSON(t *testing.T) {
	row := &store.PluginSettings{PluginID: "probe", Settings: map[string]any{"token": "********"}, Schema: []store.ConfigField{{Key: "token", Type: "secret", Label: "Token", Description: "credential", Default: "hint", Required: true, Options: []string{"option"}, Component: "input"}}, Icon: "icon", UpdatedAt: "stamp"}
	got, err := json.Marshal(pluginSettingsView(row))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"plugin_id":"probe","settings":{"token":"********"},"schema":[{"key":"token","type":"secret","label":"Token","description":"credential","default":"hint","required":true,"options":["option"],"component":"input"}],"icon":"icon","updated_at":"stamp"}`
	if string(got) != want {
		t.Fatalf("wire: %s", got)
	}
	row.Schema = nil
	got, err = json.Marshal(pluginSettingsView(row))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `"schema":null`) {
		t.Fatal(string(got))
	}
	row.Schema = []store.ConfigField{}
	got, err = json.Marshal(pluginSettingsView(row))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `"schema":[]`) {
		t.Fatal(string(got))
	}
}
