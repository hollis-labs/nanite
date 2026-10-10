package api

import (
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

func TestRetiredValidateReflexCannotExecuteHistoricalAction(t *testing.T) {
	a, mux := newTestAPI(t)
	retiredAPIHistoricalProfile(t, a, "reflex-preview-retained", "user")
	const query = `SELECT * FROM agent_profiles ORDER BY id`
	before := retiredAPISnapshot(t, a, query)
	for _, body := range []string{`{"trigger_kind":"predicate","trigger_spec":"{\"kind\":\"tool_calls_window\",\"window\":2,\"op\":\"=\",\"value\":0}","action_kind":"halt_session","action_spec":"{}"}`, `not json`} {
		requireRetiredAPI(t, retiredAPIRequest(t, mux, "POST", "/api/reflexes/validate", body))
	}
	retiredAPIHistoryUnchanged(t, a, query, before)
}
