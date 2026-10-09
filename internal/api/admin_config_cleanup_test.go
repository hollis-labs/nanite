package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/providercatalog"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/zalando/go-keyring"
)

func TestListProviders_AllowlistAppliesAfterCatalogMerge(t *testing.T) {
	isolateProviderKeys(t)
	a, mux := newTestAPIWithSeededProviders(t)
	cat := providercatalog.New()
	cat.Add(providercatalog.Entry{Name: "anthropic", DisplayName: "Hidden catalog", RowID: "anthropic-001"})
	cat.Add(providercatalog.Entry{Name: "openai", DisplayName: "Allowed catalog", RowID: "openai-001"})
	cat.Add(providercatalog.Entry{Name: "pty-old", DisplayName: "Retired shell provider", RowID: "legacy"})
	a.Services.ProviderCatalog = cat
	for _, allow := range []string{" OPENAI , ", "does-not-exist"} {
		t.Setenv("NANITE_VISIBLE_PROVIDERS", allow)
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/providers", nil))
		if response.Code != http.StatusOK {
			t.Fatal(response.Body.String())
		}
		var rows []store.ProviderConfig
		if err := json.Unmarshal(response.Body.Bytes(), &rows); err != nil {
			t.Fatal(err)
		}
		if allow == "does-not-exist" {
			if len(rows) != 0 {
				t.Fatal("unmatched allowlist reintroduced catalog rows")
			}
			continue
		}
		if len(rows) != 1 || rows[0].ProviderType != "openai" || rows[0].Name != "Allowed catalog" {
			t.Fatalf("merged filter/catalog precedence: %+v", rows)
		}
	}
	t.Setenv("NANITE_VISIBLE_PROVIDERS", "")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/providers", nil))
	if strings.Contains(response.Body.String(), "pty-old") {
		t.Fatal("retired provider survived catalog merge")
	}
	// Presentation filtering never deletes or disables provider configuration.
	row, err := a.Services.ProviderConfig.Get(context.Background(), "anthropic-001")
	if err != nil || row == nil {
		t.Fatal("hidden provider config lost")
	}
}

func TestProviderCredentialUnavailable_SafeActionableHTTPError(t *testing.T) {
	for _, key := range []string{"private-key-must-not-escape", ""} {
		t.Run(map[bool]string{true: "clear", false: "save"}[key == ""], func(t *testing.T) {
			isolateProviderKeys(t)
			a, mux := newTestAPIWithSeededProviders(t)
			if code, body := postAPIKey(t, mux, "anthropic-001", "retained-key"); code != http.StatusOK {
				t.Fatalf("seed: %d %v", code, body)
			}
			before, _ := a.Services.Providers.Get("anthropic")
			keyring.MockInitWithError(errors.New("backend private-key-must-not-escape"))
			code, body := postAPIKey(t, mux, "anthropic-001", key)
			if code != http.StatusServiceUnavailable || body["code"] != "credential_store_unavailable" || body["environment_variable"] != "ANTHROPIC_API_KEY" || body["restart_required"] != true {
				t.Fatalf("status=%d body=%v", code, body)
			}
			if strings.Contains(body["error"].(string), "private-key-must-not-escape") || !strings.Contains(body["error"].(string), "restart Nanite") {
				t.Fatal("credential escaped or recovery guidance missing")
			}
			after, _ := a.Services.Providers.Get("anthropic")
			if before != after {
				t.Fatal("failed credential update replaced live adapter")
			}
		})
	}
}

func TestValidateReflex_ReportsMatchWithoutExecutingAction(t *testing.T) {
	a, mux := newTestAPI(t)
	ctx := context.Background()
	agent := &store.AgentProfile{Name: "Retained", Slug: "retained-reflex", Status: "active", Source: "api"}
	if err := a.Services.Agents.Create(ctx, agent); err != nil {
		t.Fatal(err)
	}
	for _, spec := range []struct {
		kind, trigger      string
		supported, matched bool
	}{
		{"predicate", `{"kind":"tool_calls_window","window":2,"op":"=","value":0}`, true, true},
		{"predicate", `{"kind":"tool_calls_window","window":2,"op":"=","value":9}`, true, false},
		{"event", `{"name":"user_message"}`, false, false},
		{"predicate", `{"kind":"AND","clauses":[]}`, false, false},
	} {
		body, _ := json.Marshal(map[string]any{"trigger_kind": spec.kind, "trigger_spec": spec.trigger, "action_kind": "halt_session", "action_spec": `{}`, "state": map[string]any{"messages": []any{map[string]any{"tool_calls": 0}, map[string]any{"tool_calls": 0}}}})
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/reflexes/validate", bytes.NewReader(body)))
		if response.Code != http.StatusOK {
			t.Fatal(response.Body.String())
		}
		got := decodeObject(t, response.Body.Bytes())
		if got["valid"] != true || got["matched"] != spec.matched || got["fired"] != spec.matched || got["evaluation_supported"] != spec.supported || got["action_executed"] != false {
			t.Fatalf("truthful preview: %v", got)
		}
	}
	retained, err := a.Services.Agents.Get(ctx, agent.ID)
	if err != nil || retained.Status != "active" {
		t.Fatal("validate executed halt or mutated profile")
	}
}
