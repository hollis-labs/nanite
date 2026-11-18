package station

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestGatewaySettings(t *testing.T) {
	for _, tc := range []struct {
		name      string
		settings  Settings
		wantError bool
	}{
		{"bearer", Settings{BaseURL: "https://example.com/api", Auth: AuthBearer, Headers: map[string]string{"X-Client": "example"}}, false},
		{"missing", Settings{}, true},
		{"scheme", Settings{BaseURL: "ftp://example.com"}, true},
		{"userinfo", Settings{BaseURL: "https://example@example.com"}, true},
		{"auth", Settings{BaseURL: "https://example.com", Auth: "other"}, true},
		{"header name", Settings{BaseURL: "https://example.com", Headers: map[string]string{"Invalid Key": "value"}}, true},
		{"credential header", Settings{BaseURL: "https://example.com", Headers: map[string]string{"authorization": "sample"}}, true},
		{"header value", Settings{BaseURL: "https://example.com", Headers: map[string]string{"X-Client": "example\nvalue"}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.settings.Validate(); (err != nil) != tc.wantError {
				t.Fatalf("Validate() = %v", err)
			}
		})
	}
}

func TestSessionID(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"a-_.:09", "a-_.:09"},
		{"._:example/session", "example-session"},
		{"._:", ""},
		{strings.Repeat("a", 129), strings.Repeat("a", 128)},
	} {
		ctx := WithSessionID(t.Context(), tc.input)
		if got, ok := SessionID(ctx); got != tc.want || ok != (tc.want != "") || (ok && !ValidSessionID(got)) {
			t.Errorf("SessionID(%q) = %q, %t, want %q", tc.input, got, ok, tc.want)
		}
	}
}

func TestGatewayClassify(t *testing.T) {
	cause := errors.New("wire error")
	for _, tc := range []struct {
		status     int
		code, body string
		want       error
	}{
		{http.StatusBadRequest, "session_required", "", ErrSessionRequired},
		{http.StatusBadRequest, "", "INVALID_SESSION", ErrInvalidSession},
		{0, "model_not_available", "", ErrModelNotAvailable},
		{http.StatusOK, "invalid_session", "", nil},
		{http.StatusBadGateway, "unknown", "", nil},
	} {
		err := Classify(tc.status, tc.code, tc.body, cause)
		if !errors.Is(err, tc.want) || (err != nil && !strings.Contains(err.Error(), cause.Error())) {
			t.Errorf("Classify(%d, %q, %q) = %v, want %v", tc.status, tc.code, tc.body, err, tc.want)
		}
	}
}
