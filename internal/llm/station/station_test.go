package station

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestParseSettings(t *testing.T) {
	good := `{"base_url":"https://gw.example/api/llm-gateway","headers":{"X-Tag":"a"},"disable_anthropic_beta":true,"disable_cache_control":true,"auth":"Bearer","key_ref":"file:///k"}`
	s, err := ParseSettings(good)
	if err != nil {
		t.Fatal(err)
	}
	if s.BaseURL != "https://gw.example/api/llm-gateway" || s.Headers["X-Tag"] != "a" || !s.DisableAnthropicBeta || !s.DisableCacheControl || s.Auth != AuthBearer {
		t.Errorf("parsed = %+v", s)
	}
	for _, bad := range []string{
		``, `{}`, `[`, `{"base_url":"gw.example"}`, `{"base_url":"https://u:p@gw"}`,
		`{"base_url":"https://gw","headers":{"authorization":"x"}}`,
		`{"base_url":"https://gw","headers":{"X-Station-Session":"x"}}`,
		`{"base_url":"https://gw","headers":{"Bad Name":"x"}}`,
		`{"base_url":"https://gw","headers":{"X-Tag":"a\nb"}}`,
		`{"base_url":"https://gw","auth":"basic"}`,
	} {
		if _, err := ParseSettings(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if _, err := ParseSettings(`{"base_url":"https://gw","headers":{"Authorization":"Bearer sekret-value"}}`); err != nil && strings.Contains(err.Error(), "sekret-value") {
		t.Errorf("error echoes a header value: %v", err)
	}
}

func TestSessionID(t *testing.T) {
	for _, ok := range []string{"a", "ses_01.2:3-4", strings.Repeat("a", 128)} {
		if !ValidSessionID(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "_a", "a b", "a/b", strings.Repeat("a", 129)} {
		if ValidSessionID(bad) {
			t.Errorf("%q should be invalid", bad)
		}
		if n := NormalizeSessionID(bad); n != "" && !ValidSessionID(n) {
			t.Errorf("Normalize(%q) = %q is still invalid", bad, n)
		}
	}
	if got := NormalizeSessionID("__a b/c"); got != "a-b-c" {
		t.Errorf("Normalize = %q", got)
	}
	if got := NormalizeSessionID("a b"); got != NormalizeSessionID("a b") {
		t.Error("Normalize is not deterministic")
	}
	ctx := WithSessionID(context.Background(), "ses-1")
	if id, ok := SessionID(ctx); !ok || id != "ses-1" {
		t.Errorf("SessionID = %q, %v", id, ok)
	}
	if _, ok := SessionID(WithSessionID(context.Background(), "___")); ok {
		t.Error("an id with nothing usable must leave ctx unchanged")
	}
}

func TestClassify(t *testing.T) {
	cause := errors.New("400 boom")
	for body, want := range map[string]error{
		`{"error":{"code":"session_required"}}`:                   ErrSessionRequired,
		`{"error":{"code":"invalid_session"}}`:                    ErrInvalidSession,
		`{"type":"error","error":{"type":"model_not_available"}}`: ErrModelNotAvailable,
	} {
		if got := Classify(400, "", body, cause); !errors.Is(got, want) || !strings.Contains(got.Error(), "400 boom") {
			t.Errorf("Classify(%s) = %v", body, got)
		}
	}
	if Classify(400, "invalid_request_error", `{"error":{}}`, cause) != nil {
		t.Error("a non-gateway error must fall through")
	}
	if Classify(200, "session_required", "", cause) != nil {
		t.Error("a success status is never a refusal")
	}
}
