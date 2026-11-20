package warden

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/libs/security/secrets/tokenfile"
)

// fakeWarden is a token endpoint with rotating parents and a grants list,
// plus the request API.
type fakeWarden struct {
	mu        sync.Mutex
	parent    string
	n         int
	ttl       int
	allowed   map[string]bool // audience → granted
	mints     int
	reused    int
	fail      string // "500" or "drop" for the next token request
	requests  int
	reqStatus string
	tokens    map[string]string // access token → audience
}

func newFake(t *testing.T) (*fakeWarden, *httptest.Server) {
	t.Helper()
	f := &fakeWarden{parent: "parent-0", ttl: 900, allowed: map[string]bool{"llm-gateway": true, "warden": true}, reqStatus: "pending", tokens: map[string]string{}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeWarden) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.URL.Path == "/v1/token":
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		_ = r.ParseForm()
		switch f.fail {
		case "500":
			f.fail = ""
			f.parent = "spent-by-lost-answer"
			w.WriteHeader(http.StatusInternalServerError)
			return
		case "drop":
			f.fail = ""
			f.parent = "spent-by-lost-answer"
			hj, _ := w.(http.Hijacker)
			conn, _, _ := hj.Hijack()
			_ = conn.Close()
			return
		}
		if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != f.parent {
			f.reused++
			oauth(w, "invalid_grant")
			return
		}
		aud := r.Form.Get("audience")
		if !f.allowed[aud] {
			oauth(w, "invalid_scope")
			return
		}
		f.mints++
		f.n++
		f.parent = fmt.Sprintf("parent-%d", f.n)
		tok := fmt.Sprintf("tok-%s-%d", aud, f.n)
		f.tokens[tok] = aud
		writeJSON(w, map[string]any{"access_token": tok, "expires_in": f.ttl, "refresh_token": f.parent})
	case r.URL.Path == "/api/v1/requests" && r.Method == http.MethodPost:
		if f.tokens[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")] != "warden" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.requests++
		writeJSON(w, map[string]any{"id": "req-1", "status": "pending", "tier": "elevated"})
	case r.URL.Path == "/api/v1/requests/req-1":
		writeJSON(w, map[string]any{"id": "req-1", "status": f.reqStatus})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func oauth(w http.ResponseWriter, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "error_description": "refused"})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func credential(t *testing.T, value string) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "agent.cred")
	if err := tokenfile.Write(file, value); err != nil {
		t.Fatal(err)
	}
	return file
}

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *clock) add(d time.Duration) { c.mu.Lock(); c.now = c.now.Add(d); c.mu.Unlock() }

func source(t *testing.T, url, file string, c *clock, onWaiting func(Waiting)) *Source {
	t.Helper()
	cfg := Config{URL: url, CredentialFile: file, Principal: "agent:test", OnWaiting: onWaiting}
	if c != nil {
		cfg.Now = c.Now
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestMintRotatesTheParentAndCachesTheToken(t *testing.T) {
	f, srv := newFake(t)
	file := credential(t, "parent-0")
	s := source(t, srv.URL, file, nil, nil)
	tok, err := s.Token(t.Context(), "llm-gateway", []string{"llm-gateway.chat"})
	if err != nil || tok != "tok-llm-gateway-1" {
		t.Fatalf("Token = %q, %v", tok, err)
	}
	if got, err := tokenfile.Read(file); err != nil || got != "parent-1" {
		t.Fatalf("parent after mint = %q, %v", got, err)
	}
	if _, err := os.Stat(file + ".inflight"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("inflight marker left after a successful mint: %v", err)
	}
	again, err := s.Bearer("llm-gateway", []string{"llm-gateway.chat"}).Token(t.Context())
	if err != nil || again != tok || f.mints != 1 {
		t.Fatalf("second Token = %q, %v after %d mints; want the cached token", again, err, f.mints)
	}
}

func TestRenewsAtTwoThirdsOfTheTokensLife(t *testing.T) {
	f, srv := newFake(t)
	f.ttl = 90
	c := &clock{now: time.Unix(1_800_000_000, 0)}
	s := source(t, srv.URL, credential(t, "parent-0"), c, nil)
	first, _ := s.Token(t.Context(), "llm-gateway", nil)
	c.add(59 * time.Second)
	if tok, _ := s.Token(t.Context(), "llm-gateway", nil); tok != first {
		t.Fatalf("renewed before two thirds of the life: %q", tok)
	}
	c.add(2 * time.Second)
	if tok, err := s.Token(t.Context(), "llm-gateway", nil); err != nil || tok == first || f.mints != 2 {
		t.Fatalf("no renewal past two thirds: %q, %v, %d mints", tok, err, f.mints)
	}
}

func TestTwoSourcesSharingAFileNeverReuseAParent(t *testing.T) {
	f, srv := newFake(t)
	file := credential(t, "parent-0")
	a, b := source(t, srv.URL, file, nil, nil), source(t, srv.URL, file, nil, nil)
	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for i := range 40 {
		s := a
		if i%2 == 1 {
			s = b
		}
		wg.Go(func() {
			s.Invalidate("llm-gateway", nil, "")
			if _, err := s.Token(context.Background(), "llm-gateway", nil); err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if f.reused != 0 || f.mints == 0 {
		t.Fatalf("%d reused parents, %d mints", f.reused, f.mints)
	}
}

func TestALostAnswerBlocksAnyRetryWithTheOldParent(t *testing.T) {
	for _, mode := range []string{"500", "drop"} {
		t.Run(mode, func(t *testing.T) {
			f, srv := newFake(t)
			f.fail = mode
			file := credential(t, "parent-0")
			s := source(t, srv.URL, file, nil, nil)
			_, err := s.Token(t.Context(), "llm-gateway", nil)
			if !errors.Is(err, ErrReenroll) {
				t.Fatalf("Token = %v, want ErrReenroll", err)
			}
			if _, err := os.Stat(file + ".inflight"); err != nil {
				t.Fatalf("no inflight marker after a lost answer: %v", err)
			}
			f.mu.Lock()
			before := f.reused + f.mints
			f.mu.Unlock()
			if _, err := s.Token(t.Context(), "llm-gateway", nil); !errors.Is(err, ErrReenroll) {
				t.Fatalf("retry = %v, want ErrReenroll", err)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.reused+f.mints != before || f.reused != 0 {
				t.Fatal("the old parent was presented again after a lost answer")
			}
		})
	}
}

func TestARefusedParentNeedsReenrollment(t *testing.T) {
	_, srv := newFake(t)
	file := credential(t, "parent-unknown")
	s := source(t, srv.URL, file, nil, nil)
	if _, err := s.Token(t.Context(), "llm-gateway", nil); !errors.Is(err, ErrReenroll) {
		t.Fatalf("Token = %v, want ErrReenroll", err)
	}
	if _, err := os.Stat(file + ".inflight"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a refusal left the inflight marker: a refused parent is not spent")
	}
}

func TestAMissingGrantFilesOneRequestAndMintsOnceApproved(t *testing.T) {
	f, srv := newFake(t)
	c := &clock{now: time.Unix(1_800_000_000, 0)}
	var seen []Waiting
	s := source(t, srv.URL, credential(t, "parent-0"), c, func(w Waiting) { seen = append(seen, w) })
	_, err := s.Token(t.Context(), "mcp-gateway", []string{"mcp.profile.standard"})
	var missing *MissingGrantError
	if !errors.As(err, &missing) || missing.Waiting.RequestID != "req-1" || missing.Waiting.Status != "pending" {
		t.Fatalf("Token = %v, want a MissingGrantError for req-1", err)
	}
	if len(seen) != 1 || seen[0].Audience != "mcp-gateway" || seen[0].Tier != "elevated" {
		t.Fatalf("OnWaiting saw %+v", seen)
	}
	// Within the recheck interval nothing is filed or looked up again.
	if _, err := s.Token(t.Context(), "mcp-gateway", []string{"mcp.profile.standard"}); !errors.As(err, &missing) || f.requests != 1 {
		t.Fatalf("second Token = %v after %d requests", err, f.requests)
	}
	f.mu.Lock()
	f.allowed["mcp-gateway"], f.reqStatus = true, "approved"
	f.mu.Unlock()
	c.add(31 * time.Second)
	tok, err := s.Token(t.Context(), "mcp-gateway", []string{"mcp.profile.standard"})
	if err != nil || !strings.HasPrefix(tok, "tok-mcp-gateway-") || f.requests != 1 {
		t.Fatalf("after approval Token = %q, %v (%d requests)", tok, err, f.requests)
	}
}

func TestErrorsNeverCarryATokenOrParent(t *testing.T) {
	f, srv := newFake(t)
	file := credential(t, "parent-0")
	s := source(t, srv.URL, file, nil, nil)
	tok, err := s.Token(t.Context(), "llm-gateway", nil)
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.fail = "500"
	f.mu.Unlock()
	s.Invalidate("llm-gateway", nil, "")
	_, err = s.Token(t.Context(), "llm-gateway", nil)
	if err == nil {
		t.Fatal("no error after a lost answer")
	}
	for _, secret := range []string{tok, "parent-0", "parent-1"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error %q carries a secret", err)
		}
	}
	if strings.Contains(err.Error(), srv.URL) {
		t.Fatalf("error %q carries the URL", err)
	}
}

func TestValidateURL(t *testing.T) {
	for raw, ok := range map[string]bool{
		"https://warden.example.test": true, "http://127.0.0.1:18212": true, "http://localhost:1": true,
		"http://warden.example.test": false, "https://u:p@warden.example.test": false, "ftp://x": false, "": false,
	} {
		if err := ValidateURL(raw); (err == nil) != ok {
			t.Errorf("ValidateURL(%q) = %v", raw, err)
		}
	}
}

// tokenServer answers /v1/token with handle and counts the requests.
func tokenServer(t *testing.T, handle func(w http.ResponseWriter, r *http.Request, n int)) (*httptest.Server, func() int) {
	t.Helper()
	var mu sync.Mutex
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		_ = r.ParseForm()
		mu.Lock()
		n++
		k := n
		mu.Unlock()
		handle(w, r, k)
	}))
	t.Cleanup(srv.Close)
	return srv, func() int { mu.Lock(); defer mu.Unlock(); return n }
}

func ok(w http.ResponseWriter, n int) {
	writeJSON(w, map[string]any{"access_token": fmt.Sprintf("tok-%d", n), "expires_in": 900, "refresh_token": fmt.Sprintf("parent-%d", n)})
}

func noMarker(t *testing.T, file string) {
	t.Helper()
	if _, err := os.Stat(file + ".inflight"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("inflight marker present: %v", err)
	}
}

func TestACancelledCallerDoesNotStrandTheMint(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	srv, _ := tokenServer(t, func(w http.ResponseWriter, _ *http.Request, n int) {
		close(started)
		<-release
		ok(w, n)
	})
	file := credential(t, "parent-0")
	s := source(t, srv.URL, file, nil, nil)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { _, err := s.Token(ctx, "llm-gateway", nil); done <- err }()
	<-started
	cancel()
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("Token after the caller cancelled = %v; the mint should finish", err)
	}
	noMarker(t, file)
	if got, _ := tokenfile.Read(file); got != "parent-1" {
		t.Fatalf("parent = %q, want the rotated parent saved", got)
	}
	if tok, err := s.Token(t.Context(), "llm-gateway", nil); err != nil || tok != "tok-1" {
		t.Fatalf("next Token = %q, %v", tok, err)
	}
}

func TestARequestThatNeverLeftClearsTheMarker(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close() // nothing listens: the dial fails
	file := credential(t, "parent-0")
	s := source(t, url, file, nil, nil)
	_, err := s.Token(t.Context(), "llm-gateway", nil)
	if err == nil || errors.Is(err, ErrReenroll) {
		t.Fatalf("Token = %v, want a plain connection error", err)
	}
	noMarker(t, file)
	if got, _ := tokenfile.Read(file); got != "parent-0" {
		t.Fatalf("parent = %q", got)
	}
}

func TestRedirectsAreNotFollowed(t *testing.T) {
	var followed sync.Mutex
	hit := false
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		followed.Lock()
		hit = true
		followed.Unlock()
	}))
	t.Cleanup(target.Close)
	srv, _ := tokenServer(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		http.Redirect(w, r, target.URL+"/v1/token", http.StatusTemporaryRedirect)
	})
	file := credential(t, "parent-0")
	s := source(t, srv.URL, file, nil, nil)
	if _, err := s.Token(t.Context(), "llm-gateway", nil); !errors.Is(err, ErrReenroll) {
		t.Fatalf("Token = %v, want ErrReenroll (a redirect is not a refusal)", err)
	}
	followed.Lock()
	defer followed.Unlock()
	if hit {
		t.Fatal("the redirect was followed: the parent was resent")
	}
}

func TestOnlyAnOAuthRefusalCountsAsUnspent(t *testing.T) {
	srv, _ := tokenServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("blocked by a proxy"))
	})
	file := credential(t, "parent-0")
	s := source(t, srv.URL, file, nil, nil)
	if _, err := s.Token(t.Context(), "llm-gateway", nil); !errors.Is(err, ErrReenroll) {
		t.Fatalf("Token = %v, want ErrReenroll", err)
	}
	if _, err := os.Stat(file + ".inflight"); err != nil {
		t.Fatal("a 4xx without an OAuth error cleared the marker")
	}
}

func TestARotatedParentIsSavedEvenWithoutAnAccessToken(t *testing.T) {
	srv, _ := tokenServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		writeJSON(w, map[string]any{"refresh_token": "parent-1"})
	})
	file := credential(t, "parent-0")
	s := source(t, srv.URL, file, nil, nil)
	if _, err := s.Token(t.Context(), "llm-gateway", nil); err == nil || errors.Is(err, ErrReenroll) {
		t.Fatalf("Token = %v, want a plain error", err)
	}
	if got, _ := tokenfile.Read(file); got != "parent-1" {
		t.Fatalf("parent = %q, want the rotated parent saved", got)
	}
	noMarker(t, file)
}

func TestAMarkerForAnotherParentIsSettled(t *testing.T) {
	srv, hits := tokenServer(t, func(w http.ResponseWriter, _ *http.Request, n int) { ok(w, n) })
	file := credential(t, "parent-new")
	// A request presenting parent-old was in flight; the file has since
	// been rotated, so that request finished.
	if err := tokenfile.Write(file+".inflight", "sha256:"+parentHash("parent-old")); err != nil {
		t.Fatal(err)
	}
	s := source(t, srv.URL, file, nil, nil)
	if _, err := s.Token(t.Context(), "llm-gateway", nil); err != nil {
		t.Fatalf("Token = %v", err)
	}
	// One for the parent the file still holds is not.
	file2 := credential(t, "parent-same")
	if err := tokenfile.Write(file2+".inflight", "sha256:"+parentHash("parent-same")); err != nil {
		t.Fatal(err)
	}
	before := hits()
	if _, err := source(t, srv.URL, file2, nil, nil).Token(t.Context(), "llm-gateway", nil); !errors.Is(err, ErrReenroll) || hits() != before {
		t.Fatalf("Token = %v after %d requests; want ErrReenroll without a request", err, hits()-before)
	}
}

func TestGrantRequestsDoNotNamePrincipal(t *testing.T) {
	var body map[string]any
	f, srv := newFake(t)
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/requests" && r.Method == http.MethodPost {
			r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
			_ = json.NewDecoder(r.Body).Decode(&body)
		}
		f.serve(w, r)
	})
	s := source(t, srv.URL, credential(t, "parent-0"), nil, nil)
	_, _ = s.Token(t.Context(), "mcp-gateway", nil)
	if body == nil || body["audience"] != "mcp-gateway" {
		t.Fatalf("no grant request: %v", body)
	}
	if _, ok := body["principal"]; ok {
		t.Fatalf("the grant request names a principal: %v", body)
	}
}

func TestFailedRenewalsBackOff(t *testing.T) {
	srv, hits := tokenServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) { oauth(w, "invalid_request") })
	c := &clock{now: time.Unix(1_800_000_000, 0)}
	s := source(t, srv.URL, credential(t, "parent-0"), c, nil)
	for range 3 {
		if _, err := s.Token(t.Context(), "llm-gateway", nil); err == nil {
			t.Fatal("no error")
		}
	}
	if hits() != 1 {
		t.Fatalf("%d requests within the backoff, want 1", hits())
	}
	c.add(1100 * time.Millisecond)
	_, _ = s.Token(t.Context(), "llm-gateway", nil)
	c.add(1100 * time.Millisecond) // the wait has doubled to 2s
	_, _ = s.Token(t.Context(), "llm-gateway", nil)
	if hits() != 2 {
		t.Fatalf("%d requests, want 2 (the second wait is longer)", hits())
	}
}

func TestInvalidateDropsOnlyTheTokenSent(t *testing.T) {
	_, srv := newFake(t)
	s := source(t, srv.URL, credential(t, "parent-0"), nil, nil)
	b := s.Bearer("llm-gateway", nil)
	first, _ := b.Token(t.Context())
	b.Invalidate("some-older-token")
	if again, _ := b.Token(t.Context()); again != first {
		t.Fatal("invalidating an older token dropped the current one")
	}
	b.Invalidate(first)
	if again, _ := b.Token(t.Context()); again == first {
		t.Fatal("the refused token was kept")
	}
}

type opaqueTransport struct{}

func (opaqueTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("opaque transport failure")
}

// A transport that does not report connections cannot prove a request never
// left, so its failures keep the lost-answer marker.
func TestUnknownTransportFailureKeepsTheMarker(t *testing.T) {
	if tracesConnections(&http.Client{Transport: opaqueTransport{}}) {
		t.Fatal("an unknown transport was trusted to report connections")
	}
	if !tracesConnections(nil) || !tracesConnections(&http.Client{}) || !tracesConnections(&http.Client{Transport: &http.Transport{}}) {
		t.Fatal("the standard transport was not trusted")
	}
}
