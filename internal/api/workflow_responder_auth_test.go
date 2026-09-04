package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBasicWorkflowResponderAuthenticatorFailsClosedAndReturnsVerifiedPrincipal(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/workflows/runs/run/callbacks/step", nil)
	request.SetBasicAuth("responder", "secret")
	for _, test := range []struct {
		name         string
		user         string
		password     string
		wantOK       bool
		wantIdentity string
	}{
		{name: "exact", user: "responder", password: "secret", wantOK: true, wantIdentity: "responder"},
		{name: "wrong user", user: "other", password: "secret"},
		{name: "wrong password", user: "responder", password: "other"},
		{name: "empty configuration", user: "", password: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			identity, ok := NewBasicWorkflowResponderAuthenticator(test.user, test.password)(request)
			if ok != test.wantOK || identity != test.wantIdentity {
				t.Fatalf("authenticate = %q, %v; want %q, %v", identity, ok, test.wantIdentity, test.wantOK)
			}
		})
	}
}
