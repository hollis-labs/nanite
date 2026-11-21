package warden

import (
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/libs/security/redact"
	"github.com/hollis-labs/libs/security/secrets/tokenfile"
	"github.com/hollis-labs/nanite/internal/warden/wardentest"
)

// These tests drive the Source against the shared fake, which answers in
// the shapes recorded from the live Warden (wardentest/testdata).

func TestRecordedFixturesAreCleanRecordings(t *testing.T) {
	entries, err := os.ReadDir(wardentest.Dir())
	if err != nil || len(entries) == 0 {
		t.Fatalf("fixtures: %v", err)
	}
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".json")
		f := wardentest.Load(t, name)
		if f.Source != "recorded" {
			t.Fatalf("%s: committed fixtures are recordings (source %q)", name, f.Source)
		}
		wardentest.CheckClean(t, name, f)
	}
}

// The recorded answer carries expires_at, expires_in and ttl_seconds; the
// client prefers expires_at, then expires_in, then ttl_seconds, then a
// 300-second default.
func TestMintParsesTheTokensLife(t *testing.T) {
	sourceNow := time.Unix(1_800_000_000, 0)
	// The fake's clock runs an hour ahead, so expires_at (from its clock)
	// and a lifetime counted from the Source's clock differ.
	wardenNow := sourceNow.Add(time.Hour)
	for _, tc := range []struct {
		name   string
		expiry wardentest.Expiry
		ttl    int
		want   time.Time
	}{
		{"recorded: expires_at wins", wardentest.Recorded, 899, wardenNow.Add(899 * time.Second)},
		{"expires_at only", wardentest.ExpiresAtOnly, 600, wardenNow.Add(600 * time.Second)},
		{"expires_in only", wardentest.ExpiresInOnly, 600, sourceNow.Add(600 * time.Second)},
		{"ttl_seconds only", wardentest.TTLSecondsOnly, 120, sourceNow.Add(120 * time.Second)},
		{"no lifetime: the default", wardentest.NoTTL, 120, sourceNow.Add(300 * time.Second)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t).Expire(tc.expiry, tc.ttl).Clock(func() time.Time { return wardenNow })
			c := &clock{now: sourceNow}
			s := source(t, f.URL, credential(t, "parent-0"), c, nil)
			if _, err := s.Token(t.Context(), "llm-gateway", []string{"llm-gateway.chat"}); err != nil {
				t.Fatal(err)
			}
			e := s.entry("llm-gateway", []string{"llm-gateway.chat"})
			// expires_at has second precision on the wire.
			if got := e.expiresAt.Truncate(time.Second); !got.Equal(tc.want.Truncate(time.Second)) {
				t.Fatalf("expires %v, want %v", got, tc.want)
			}
			if want := sourceNow.Add(e.expiresAt.Sub(sourceNow) * 2 / 3); !e.renewAt.Equal(want) {
				t.Fatalf("renews %v, want %v (two thirds of the life)", e.renewAt, want)
			}
		})
	}
}

// Without expires_at, expires_in is preferred over ttl_seconds when they
// differ (Warden sends them equal; a proxy or an older Warden may not).
func TestExpiresInIsPreferredOverTTLSeconds(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	srv, _ := tokenServer(t, func(w http.ResponseWriter, _ *http.Request, n int) {
		body := wardentest.MintAnswerBody(wardentest.MintFields{Token: "tok", NextParent: "parent-1", Audience: "llm-gateway", Expiry: wardentest.ExpiresInOnly, TTL: 600})
		body["ttl_seconds"] = 60
		writeJSON(w, body)
	})
	s := source(t, srv.URL, credential(t, "parent-0"), &clock{now: now}, nil)
	if _, err := s.Token(t.Context(), "llm-gateway", nil); err != nil {
		t.Fatal(err)
	}
	if got := s.entry("llm-gateway", nil).expiresAt; !got.Equal(now.Add(600 * time.Second)) {
		t.Fatalf("expires %v, want expires_in's 600s", got)
	}
}

func TestAnAnswerWithoutANextParentNeedsReenrollment(t *testing.T) {
	// Warden rotated, but the answer names no next parent: the old one is
	// spent and there is nothing to save. The marker stays.
	f := newFake(t).OmitNextParent()
	file := credential(t, "parent-0")
	s := source(t, f.URL, file, nil, nil)
	if _, err := s.Token(t.Context(), "llm-gateway", nil); !errors.Is(err, ErrReenroll) || !strings.Contains(err.Error(), "no next parent") {
		t.Fatalf("Token = %v, want ErrReenroll for a missing next parent", err)
	}
	if got, _ := tokenfile.Read(file); got != "parent-0" {
		t.Fatalf("parent = %q, want it untouched", got)
	}
	if _, err := os.Stat(file + ".inflight"); err != nil {
		t.Fatalf("no marker: %v", err)
	}
}

func TestTheRotatedParentAndTokenAreRedacted(t *testing.T) {
	f := newFake(t)
	scope := redact.NewScope()
	s, err := New(Config{URL: f.URL, CredentialFile: credential(t, "parent-0"), Principal: "agent:test", Redactor: scope})
	if err != nil {
		t.Fatal(err)
	}
	tok, err := s.Token(t.Context(), "llm-gateway", nil)
	if err != nil {
		t.Fatal(err)
	}
	r, err := redact.New(redact.WithScope(scope))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{f.Parent(), tok} {
		if out := r.Text("seen: " + secret); strings.Contains(out, secret) {
			t.Fatalf("not redacted: %q", out)
		}
	}
}

func TestARenewalFailureKeepsTheTokenOnlyUntilItExpires(t *testing.T) {
	c := &clock{now: time.Unix(1_800_000_000, 0)}
	f := newFake(t).Expire(wardentest.Recorded, 90).Clock(c.Now)
	s := source(t, f.URL, credential(t, "parent-0"), c, nil)
	first, err := s.Token(t.Context(), "llm-gateway", nil)
	if err != nil {
		t.Fatal(err)
	}
	refuse := `{"error":"invalid_request","error_description":"temporarily refused"}`
	// Past two thirds of its life the token is renewed; the renewal is
	// refused (unspent), and the token, still alive, is used.
	f.FailNext("/v1/token", http.StatusBadRequest, refuse)
	c.add(61 * time.Second)
	if tok, err := s.Token(t.Context(), "llm-gateway", nil); err != nil || tok != first {
		t.Fatalf("during its life: %q, %v", tok, err)
	}
	// Once it has expired, a failed renewal is an error, never the dead token.
	f.FailNext("/v1/token", http.StatusBadRequest, refuse)
	c.add(30 * time.Second)
	if tok, err := s.Token(t.Context(), "llm-gateway", nil); err == nil || tok != "" {
		t.Fatalf("after expiry: %q, %v", tok, err)
	}
}

// Inside the backoff after a failed renewal, the cached token is used while
// it lives and refused once it has expired, without another request.
func TestTheBackoffWindowHonoursTheTokensExpiry(t *testing.T) {
	c := &clock{now: time.Unix(1_800_000_000, 0)}
	f := newFake(t).Expire(wardentest.Recorded, 90).Clock(c.Now)
	s := source(t, f.URL, credential(t, "parent-0"), c, nil)
	first, err := s.Token(t.Context(), "llm-gateway", nil)
	if err != nil {
		t.Fatal(err)
	}
	refuse := `{"error":"invalid_request","error_description":"temporarily refused"}`
	// Renewal fails at 89.5s; the 1s backoff runs past the 90s expiry.
	f.FailNext("/v1/token", http.StatusBadRequest, refuse)
	c.add(89500 * time.Millisecond)
	if tok, err := s.Token(t.Context(), "llm-gateway", nil); err != nil || tok != first {
		t.Fatalf("renewal failure while alive: %q, %v", tok, err)
	}
	requests := f.Calls("/v1/token")
	c.add(200 * time.Millisecond) // in the backoff, still alive
	if tok, err := s.Token(t.Context(), "llm-gateway", nil); err != nil || tok != first {
		t.Fatalf("in the backoff while alive: %q, %v", tok, err)
	}
	c.add(400 * time.Millisecond) // in the backoff, expired
	if tok, err := s.Token(t.Context(), "llm-gateway", nil); err == nil || tok != "" {
		t.Fatalf("in the backoff after expiry: %q, %v", tok, err)
	}
	if f.Calls("/v1/token") != requests {
		t.Fatal("a token request was made inside the backoff")
	}
}

// A grant request Warden grants at once is followed by a mint, not a wait.
func TestAGrantRequestGrantedAtOnceMints(t *testing.T) {
	f := newFake(t).FileAs("granted", "ground")
	var waited []Waiting
	s := source(t, f.URL, credential(t, "parent-0"), nil, func(w Waiting) { waited = append(waited, w) })
	tok, err := s.Token(t.Context(), "mcp-gateway", []string{"mcp.profile.standard"})
	if err != nil || !strings.HasPrefix(tok, "tok-mcp-gateway-") {
		t.Fatalf("Token = %q, %v", tok, err)
	}
	if len(f.Requests()) != 1 || len(waited) != 0 {
		t.Fatalf("%d requests filed, %d waits", len(f.Requests()), len(waited))
	}
}

// Each recorded refusal: what it does to the marker and the parent.
func TestRecordedErrorCodes(t *testing.T) {
	t.Run("invalid_grant: re-enroll, parent unspent", func(t *testing.T) {
		f := newFake(t)
		file := credential(t, "not-the-parent")
		_, err := source(t, f.URL, file, nil, nil).Token(t.Context(), "llm-gateway", nil)
		if !errors.Is(err, ErrReenroll) || !strings.Contains(err.Error(), "invalid_grant") {
			t.Fatalf("Token = %v", err)
		}
		noMarker(t, file)
	})
	t.Run("invalid_scope: a grant request is filed", func(t *testing.T) {
		f := newFake(t).Grant("queue", "queue.read")
		file := credential(t, "parent-0")
		_, err := source(t, f.URL, file, nil, nil).Token(t.Context(), "queue", []string{"queue.write"})
		var missing *MissingGrantError
		if !errors.As(err, &missing) || missing.Waiting.RequestID != "req-1" || len(f.Requests()) != 1 {
			t.Fatalf("Token = %v, requests %v", err, f.Requests())
		}
		noMarker(t, file)
		if got, _ := tokenfile.Read(file); got != f.Parent() {
			t.Fatalf("parent %q out of step with Warden's %q", got, f.Parent())
		}
	})
	t.Run("401 on the API: a refusal, not a gone request", func(t *testing.T) {
		f := newFake(t)
		s := source(t, f.URL, credential(t, "parent-0"), nil, nil)
		f.FailNext("/api/v1/requests", http.StatusUnauthorized, string(wardentest.Load(t, "lookup_unauthorized").Response.Body))
		_, err := s.Request(t.Context(), "req-1")
		var r *refusal
		if !errors.As(err, &r) || r.status != http.StatusUnauthorized || r.code != "unauthorized" || errors.Is(err, ErrRequestGone) {
			t.Fatalf("Request = %v", err)
		}
	})
	t.Run("429 on the token endpoint: the answer may be lost", func(t *testing.T) {
		// Warden itself never answers 429; one in front of it (a proxy)
		// cannot prove the parent was not presented, so the marker stays.
		f := newFake(t)
		file := credential(t, "parent-0")
		f.FailNext("/v1/token", http.StatusTooManyRequests, `{"code":"rate_limited","message":"slow down"}`)
		if _, err := source(t, f.URL, file, nil, nil).Token(t.Context(), "llm-gateway", nil); !errors.Is(err, ErrReenroll) {
			t.Fatalf("Token = %v", err)
		}
		if _, err := os.Stat(file + ".inflight"); err != nil {
			t.Fatal("a 429 without an OAuth code cleared the marker")
		}
	})
	t.Run("429 on the API: a plain refusal", func(t *testing.T) {
		f := newFake(t)
		s := source(t, f.URL, credential(t, "parent-0"), nil, nil)
		f.FailNext("/api/v1/requests", http.StatusTooManyRequests, `{"code":"rate_limited","message":"slow down"}`)
		_, err := s.Request(t.Context(), "req-1")
		var r *refusal
		if !errors.As(err, &r) || r.code != "rate_limited" || errors.Is(err, ErrRequestGone) || errors.Is(err, ErrReenroll) {
			t.Fatalf("Request = %v", err)
		}
	})
}

func TestGoneRequests(t *testing.T) {
	f := newFake(t)
	s := source(t, f.URL, credential(t, "parent-0"), nil, nil)
	// 404, recorded: no such request.
	if _, err := s.Request(t.Context(), "req-missing"); !errors.Is(err, ErrRequestGone) || !strings.Contains(err.Error(), "404") {
		t.Fatalf("missing = %v", err)
	}
	// 403: another principal's request.
	if _, err := s.Token(t.Context(), "mcp-gateway", nil); err == nil {
		t.Fatal("a missing grant minted")
	}
	f.Forbid("req-1")
	if _, err := s.Request(t.Context(), "req-1"); !errors.Is(err, ErrRequestGone) || !strings.Contains(err.Error(), "403") {
		t.Fatalf("forbidden = %v", err)
	}
	// A request that exists is not gone.
	g := newFake(t)
	s = source(t, g.URL, credential(t, "parent-0"), nil, nil)
	if _, err := s.Token(t.Context(), "mcp-gateway", nil); err == nil {
		t.Fatal("a missing grant minted")
	}
	if w, err := s.Request(t.Context(), "req-1"); err != nil || w.Status != "pending" || w.Tier != "elevated" {
		t.Fatalf("pending request = %+v, %v", w, err)
	}
}
