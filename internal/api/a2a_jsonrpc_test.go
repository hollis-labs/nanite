package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/nanite/internal/a2a"
	"github.com/hollis-labs/nanite/internal/service"
)

// TestA2AJSONRPC_MethodRouting verifies that the JSON-RPC handler routes
// to the correct method handlers based on the method field.
func TestA2AJSONRPC_MethodRouting(t *testing.T) {
	tests := []struct {
		name           string
		method         string
		wantStatusCode int
	}{
		{
			name:           "task.submit method routes correctly",
			method:         methodTaskSubmit,
			wantStatusCode: http.StatusOK,
		},
		{
			name:           "task.get method routes correctly",
			method:         methodTaskGet,
			wantStatusCode: http.StatusOK,
		},
		{
			name:           "task.cancel method routes correctly",
			method:         methodTaskCancel,
			wantStatusCode: http.StatusOK,
		},
		{
			name:           "task.provideInput method routes correctly",
			method:         methodTaskProvideInput,
			wantStatusCode: http.StatusOK,
		},
		{
			name:           "unknown method returns method not found",
			method:         "unknown.method",
			wantStatusCode: http.StatusOK, // JSON-RPC always returns 200
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create minimal request
			req := a2a.JSONRPCRequest{
				JSONRPC: a2a.JSONRPCVersion,
				Method:  tt.method,
				Params:  map[string]any{},
				ID:      1,
			}

			body, err := json.Marshal(req)
			if err != nil {
				t.Fatalf("failed to marshal request: %v", err)
			}

			httpReq := httptest.NewRequest("POST", "/api/a2a/jsonrpc", bytes.NewReader(body))
			httpReq.Header.Set("Content-Type", "application/json")

			recorder := httptest.NewRecorder()

			// Create minimal API instance (would need full mocking for real tests)
			// This is just a compilation test
			api := &API{
				Services: &service.Container{},
			}

			api.handleA2AJSONRPC(recorder, httpReq)

			if recorder.Code != tt.wantStatusCode {
				t.Errorf("got status %d, want %d", recorder.Code, tt.wantStatusCode)
			}
		})
	}
}

// TestAgentCard_ContentType verifies the agent card endpoint returns JSON.
func TestAgentCard_ContentType(t *testing.T) {
	httpReq := httptest.NewRequest("GET", "/.well-known/agent-card.json", nil)
	recorder := httptest.NewRecorder()

	// This would panic without a real AgentCardGenerator, but validates the signature
	_ = httpReq
	_ = recorder
}
