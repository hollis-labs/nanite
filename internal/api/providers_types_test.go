package api

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

var providerConfigViewKeys = []string{"id", "name", "provider_type", "is_enabled", "settings", "created_at", "updated_at"}

var modelViewKeys = []string{
	"id", "provider_id", "model_id", "display_name", "context_window", "max_output",
	"supports_tools", "supports_vision", "is_enabled", "pricing", "sort_order", "provider_type",
}

func TestProviderConfigViewJSON(t *testing.T) {
	var p store.ProviderConfig
	populate(t, &p)
	rows := []store.ProviderConfig{p}
	assertKeys(t, "ProviderConfigView", mustJSON(t, providerConfigToView(&p)), providerConfigViewKeys)
	assertSameJSON(t, "populated", providerConfigsToView(rows), rows)
	assertSameJSON(t, "zero", providerConfigToView(&store.ProviderConfig{}), store.ProviderConfig{})
	assertSameJSON(t, "empty", providerConfigsToView([]store.ProviderConfig{}), []store.ProviderConfig{})
}

func TestModelViewJSON(t *testing.T) {
	var m store.Model
	populate(t, &m)
	rows := []store.Model{m}
	assertKeys(t, "ModelView", mustJSON(t, modelsToView(rows)[0]), modelViewKeys)
	assertSameJSON(t, "populated", modelsToView(rows), rows)
	assertSameJSON(t, "zero", modelsToView([]store.Model{{}}), []store.Model{{}})
	assertSameJSON(t, "empty", modelsToView([]store.Model{}), []store.Model{})
}

// TestProviderStatusResponsesMatchOldEncoding pins the three provider
// status/key responses against the shapes the handlers built before: the
// local struct embedding store.ProviderConfig, and two map[string]any (the
// api-key one plus its key_source addition).
func TestProviderStatusResponsesMatchOldEncoding(t *testing.T) {
	var p store.ProviderConfig
	populate(t, &p)

	type providerStatusBefore struct {
		store.ProviderConfig
		HasAPIKey  bool `json:"has_api_key"`
		Registered bool `json:"registered"`
	}
	for _, flags := range [][2]bool{{true, false}, {false, true}} {
		before := []providerStatusBefore{{ProviderConfig: p, HasAPIKey: flags[0], Registered: flags[1]}}
		after := []ProviderStatusView{{ProviderConfigView: providerConfigToView(&p), HasAPIKey: flags[0], Registered: flags[1]}}
		assertSameJSON(t, "provider statuses", after, before)

		detailBefore := map[string]any{"provider": &p, "has_api_key": flags[0], "registered": flags[1]}
		detailAfter := ProviderStatusDetailView{Provider: providerConfigToView(&p), HasAPIKey: flags[0], Registered: flags[1]}
		assertSameJSON(t, "provider status detail", detailAfter, detailBefore)

		// CW-20260930-0101 added key_source, an approved wire addition; the
		// two keys the map carried are otherwise unchanged.
		for _, source := range []string{"keychain", "environment", ""} {
			keyBefore := map[string]any{"provider_id": p.ID, "has_key": flags[0], "key_source": source}
			keyAfter := ProviderAPIKeyResponse{ProviderID: p.ID, HasKey: flags[0], KeySource: source}
			assertSameJSON(t, "provider api key", keyAfter, keyBefore)
		}
	}
	assertKeys(t, "ProviderAPIKeyResponse", mustJSON(t, ProviderAPIKeyResponse{}), []string{"has_key", "key_source", "provider_id"})
}

// TestUpdateProviderRequestDecodesLikeStoreUpdate pins that PUT bodies
// decode through the API-owned request into the same store.ProviderUpdate
// they produced when handlers decoded into the store type directly.
func TestUpdateProviderRequestDecodesLikeStoreUpdate(t *testing.T) {
	bodies := []string{
		`{}`,
		`{"is_enabled":false}`,
		`{"is_enabled":true,"settings":"{\"cli_path\":\"/bin/claude\"}"}`,
		`{"settings":""}`,
		`{"settings":null,"is_enabled":null}`,
		`{"unknown":1,"is_enabled":true}`,
	}
	for _, body := range bodies {
		var direct store.ProviderUpdate
		directErr := json.NewDecoder(strings.NewReader(body)).Decode(&direct)
		var req UpdateProviderRequest
		reqErr := json.NewDecoder(strings.NewReader(body)).Decode(&req)
		if (directErr == nil) != (reqErr == nil) {
			t.Fatalf("%s: decode errors differ: direct %v, request %v", body, directErr, reqErr)
		}
		if got := req.toStore(); !reflect.DeepEqual(got, direct) {
			t.Errorf("%s: request decodes to %+v, direct decode gave %+v", body, got, direct)
		}
	}
}
