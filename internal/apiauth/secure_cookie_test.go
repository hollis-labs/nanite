package apiauth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The Secure attribute follows the operator's setting alone: Nanite sees plain
// HTTP behind a proxy and must not infer HTTPS from request headers.
func TestSessionCookieSecureFollowsOperatorSetting(t *testing.T) {
	for _, tc := range []struct {
		name   string
		secure bool
	}{{"default off", false}, {"operator on", true}} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			(&Handlers{SecureCookies: tc.secure}).setCookie(rec, "v", time.Now().Add(time.Hour))
			cookies := rec.Result().Cookies()
			if len(cookies) != 1 || cookies[0].Name != CookieName {
				t.Fatalf("cookies = %+v", cookies)
			}
			if cookies[0].Secure != tc.secure {
				t.Fatalf("Secure = %v, want %v", cookies[0].Secure, tc.secure)
			}
			if !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
				t.Fatalf("weakened cookie: %+v", cookies[0])
			}
		})
	}
}
