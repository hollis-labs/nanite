package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProviderManageAndPluginConfigMalformedJSONUsesSharedDecode(t *testing.T) {
	a := &API{}
	tests := []struct {
		name    string
		method  string
		path    string
		handler http.HandlerFunc
	}{
		{
			name:    "update provider",
			method:  http.MethodPut,
			path:    "/api/providers/anthropic-001",
			handler: a.handleUpdateProvider,
		},
		{
			name:    "set provider api key",
			method:  http.MethodPost,
			path:    "/api/providers/anthropic-001/api-key",
			handler: a.handleSetProviderAPIKey,
		},
		{
			name:    "update plugin config",
			method:  http.MethodPut,
			path:    "/api/plugin-config/demo",
			handler: a.handleUpdatePluginConfig,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, bytes.NewBufferString(`{`))
			req.SetPathValue("id", "demo")
			w := httptest.NewRecorder()

			tt.handler(w, req)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
			}
			var body map[string]string
			if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
				t.Fatalf("decode error response: %v", err)
			}
			got := body["error"]
			if !strings.HasPrefix(got, "invalid JSON: ") {
				t.Fatalf("error = %q, want invalid JSON prefix", got)
			}
			if !strings.Contains(got, "unexpected EOF") {
				t.Fatalf("error = %q, want decoder error detail", got)
			}
		})
	}
}
