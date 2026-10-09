package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAgentBindRequiresTokenOffLoopback(t *testing.T) {
	for _, bind := range []string{"0.0.0.0", "::", "192.0.2.1"} {
		if err := validateAgentBind(bind, ""); err == nil {
			t.Errorf("%s admitted without token", bind)
		}
		if err := validateAgentBind(bind, "test-token"); err != nil {
			t.Errorf("%s rejected with token: %v", bind, err)
		}
	}
	for _, bind := range []string{"127.0.0.1", "::1"} {
		if err := validateAgentBind(bind, ""); err != nil {
			t.Error(err)
		}
	}
}
func TestAgentAuthBoundary(t *testing.T) {
	for _, test := range []struct {
		name, path, remote, token, authorization string
		want                                     int
	}{
		{"local view", "/api/agent/v1/sessions", "127.0.0.1:123", "", "", 200},
		{"remote view requires token", "/api/agent/v1/sessions", "192.0.2.1:123", "", "", 401},
		{"configured token required", "/api/agent/v1/sessions", "127.0.0.1:123", "test-token", "", 401},
		{"wrong token", "/api/agent/v1/sessions", "127.0.0.1:123", "test-token", "Bearer wrong", 401},
		{"remote authenticated", "/api/agent/v1/sessions", "192.0.2.1:123", "test-token", "Bearer test-token", 200},
		{"tools loopback still requires token", "/api/tools/call", "127.0.0.1:123", "", "", 401},
		{"tools authenticated", "/api/tools/call", "127.0.0.1:123", "test-token", "Bearer test-token", 200},
		{"remote product records authenticated", "/api/sessions", "192.0.2.1:123", "test-token", "", 401},
		{"health remains public", "/api/health", "192.0.2.1:123", "test-token", "", 200},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("NANITE_AUTH_TOKEN", test.token)
			t.Setenv("NANITE_AUTH_USER", "operator")
			t.Setenv("NANITE_AUTH_PASSWORD", "basic-secret")
			var downstream http.Handler = dummyHandler
			// Check that a valid bearer does not additionally require Basic Auth.
			if test.authorization == "Bearer test-token" {
				downstream = basicAuthMiddleware(dummyHandler)
			}
			handler := agentAuthMiddleware(downstream)
			req := httptest.NewRequest(http.MethodGet, test.path, nil)
			req.RemoteAddr = test.remote
			req.Header.Set("Authorization", test.authorization)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if w.Code != test.want {
				t.Fatalf("status=%d want=%d body=%s", w.Code, test.want, w.Body.String())
			}
		})
	}
}

func TestLogicalGeneralChatProvisionAuthBoundary(t *testing.T) {
	t.Setenv("NANITE_AUTH_USER", "operator")
	t.Setenv("NANITE_AUTH_PASSWORD", "fixture-password")
	t.Setenv("NANITE_AUTH_TOKEN", "fixture-token")
	for _, test := range []struct {
		name, remote, authorization string
		want                        int
	}{
		{"local missing auth", "127.0.0.1:123", "", http.StatusUnauthorized},
		{"local wrong auth", "127.0.0.1:123", "Basic b3BlcmF0b3I6d3Jvbmc=", http.StatusUnauthorized},
		{"remote basic is insufficient", "192.0.2.1:123", "Basic b3BlcmF0b3I6Zml4dHVyZS1wYXNzd29yZA==", http.StatusUnauthorized},
		{"remote bearer", "192.0.2.1:123", "Bearer fixture-token", http.StatusOK},
		{"local basic", "127.0.0.1:123", "Basic b3BlcmF0b3I6Zml4dHVyZS1wYXNzd29yZA==", http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := agentAuthMiddleware(basicAuthMiddleware(dummyHandler))
			request := httptest.NewRequest(http.MethodPost, "/api/agents/provision/general-chat", nil)
			request.RemoteAddr = test.remote
			request.Header.Set("Authorization", test.authorization)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status %d want %d", response.Code, test.want)
			}
		})
	}
}
