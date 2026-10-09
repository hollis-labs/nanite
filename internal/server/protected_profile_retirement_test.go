package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProtectedProfileRetirementHostAuthentication(t *testing.T) {
	t.Setenv("NANITE_AUTH_USER", "operator")
	t.Setenv("NANITE_AUTH_PASSWORD", "fixture-password")
	t.Setenv("NANITE_AUTH_TOKEN", "fixture-token")
	for _, path := range []string{"/api/agents/fixture/protected-retirement-export", "/api/agents/fixture/protected-retire"} {
		for _, tc := range []struct {
			remote, auth string
			want         int
		}{
			{"127.0.0.1:123", "", http.StatusUnauthorized},
			{"127.0.0.1:123", "Basic b3BlcmF0b3I6d3Jvbmc=", http.StatusUnauthorized},
			{"192.0.2.1:123", "Basic b3BlcmF0b3I6Zml4dHVyZS1wYXNzd29yZA==", http.StatusUnauthorized},
			{"192.0.2.1:123", "Bearer fixture-token", http.StatusOK},
			{"127.0.0.1:123", "Basic b3BlcmF0b3I6Zml4dHVyZS1wYXNzd29yZA==", http.StatusOK},
		} {
			request := httptest.NewRequest(http.MethodPost, path, nil)
			request.RemoteAddr = tc.remote
			request.Header.Set("Authorization", tc.auth)
			w := httptest.NewRecorder()
			agentAuthMiddleware(basicAuthMiddleware(dummyHandler)).ServeHTTP(w, request)
			if w.Code != tc.want {
				t.Fatalf("%s: status%d want%d", path, w.Code, tc.want)
			}
		}
	}
}
