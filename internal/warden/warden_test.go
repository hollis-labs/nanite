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
	"github.com/hollis-labs/nanite/internal/warden/wardentest"
)

// newFake is the shared fake Warden (wardentest) with this file's names:
// parents parent-N, tokens tok-<audience>-N, grant requests filed pending
// at the elevated tier, and llm-gateway granted.
func newFake(t *testing.T) *wardentest.Fake {
	t.Helper()
	return wardentest.NewFake(t, "parent-0").Grant("llm-gateway").Grant("warden").
		Tokens(func(n int, aud string) string { return fmt.Sprintf("tok-%s-%d", aud, n) }).
		Parents(func(n int) string { return fmt.Sprintf("parent-%d", n) }).
		FileAs("pending", "elevated").Expire(wardentest.Recorded, 900)
}

// oauth refuses in the recorded OAuth error shape.
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
	f := newFake(t)
	srv := f.Server
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
	if err != nil || again != tok || f.Mints() != 1 {
		t.Fatalf("second Token = %q, %v after %d mints; want the cached token", again, err, f.Mints())
	}
}

func TestRenewsAtTwoThirdsOfTheTokensLife(t *testing.T) {
	f := newFake(t)
	srv := f.Server
	c := &clock{now: time.Unix(1_800_000_000, 0)}
	f.Expire(wardentest.Recorded, 90).Clock(c.Now)
	s := source(t, srv.URL, credential(t, "parent-0"), c, nil)
	first, _ := s.Token(t.Context(), "llm-gateway", nil)
	c.add(59 * time.Second)
	if tok, _ := s.Token(t.Context(), "llm-gateway", nil); tok != first {
		t.Fatalf("renewed before two thirds of the life: %q", tok)
	}
	c.add(2 * time.Second)
	if tok, err := s.Token(t.Context(), "llm-gateway", nil); err != nil || tok == first || f.Mints() != 2 {
		t.Fatalf("no renewal past two thirds: %q, %v, %d mints", tok, err, f.Mints())
	}
}

func TestTwoSourcesSharingAFileNeverReuseAParent(t *testing.T) {
	f := newFake(t)
	srv := f.Server
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
	if f.Refused() != 0 || f.Mints() == 0 {
		t.Fatalf("%d reused parents, %d mints", f.Refused(), f.Mints())
	}
}

func TestALostAnswerBlocksAnyRetryWithTheOldParent(t *testing.T) {
	for _, mode := range []string{"500", "drop"} {
		t.Run(mode, func(t *testing.T) {
			f := newFake(t)
			srv := f.Server
			f.LoseNextAnswer(mode)
			file := credential(t, "parent-0")
			s := source(t, srv.URL, file, nil, nil)
			_, err := s.Token(t.Context(), "llm-gateway", nil)
			if !errors.Is(err, ErrReenroll) {
				t.Fatalf("Token = %v, want ErrReenroll", err)
			}
			if _, err := os.Stat(file + ".inflight"); err != nil {
				t.Fatalf("no inflight marker after a lost answer: %v", err)
			}
			before := f.Refused() + f.Mints()
			if _, err := s.Token(t.Context(), "llm-gateway", nil); !errors.Is(err, ErrReenroll) {
				t.Fatalf("retry = %v, want ErrReenroll", err)
			}
			if f.Refused()+f.Mints() != before || f.Refused() != 0 {
				t.Fatal("the old parent was presented again after a lost answer")
			}
		})
	}
}

func TestARefusedParentNeedsReenrollment(t *testing.T) {
	srv := newFake(t).Server
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
	f := newFake(t)
	srv := f.Server
	c := &clock{now: time.Unix(1_800_000_000, 0)}
	f.Clock(c.Now)
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
	if _, err := s.Token(t.Context(), "mcp-gateway", []string{"mcp.profile.standard"}); !errors.As(err, &missing) || len(f.Requests()) != 1 {
		t.Fatalf("second Token = %v after %d requests", err, len(f.Requests()))
	}
	f.Decide("req-1", "approved")
	c.add(31 * time.Second)
	tok, err := s.Token(t.Context(), "mcp-gateway", []string{"mcp.profile.standard"})
	if err != nil || !strings.HasPrefix(tok, "tok-mcp-gateway-") || len(f.Requests()) != 1 {
		t.Fatalf("after approval Token = %q, %v (%d requests)", tok, err, len(f.Requests()))
	}
}

func TestErrorsNeverCarryATokenOrParent(t *testing.T) {
	f := newFake(t)
	srv := f.Server
	file := credential(t, "parent-0")
	s := source(t, srv.URL, file, nil, nil)
	tok, err := s.Token(t.Context(), "llm-gateway", nil)
	if err != nil {
		t.Fatal(err)
	}
	f.LoseNextAnswer("500")
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

// ok answers a mint in the recorded shape: token tok-N, next parent
// parent-N.
func ok(w http.ResponseWriter, n int) {
	writeJSON(w, wardentest.MintAnswerBody(wardentest.MintFields{Token: fmt.Sprintf("tok-%d", n), NextParent: fmt.Sprintf("parent-%d", n), Audience: "llm-gateway", TTL: 900}))
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
	f := newFake(t)
	s := source(t, f.URL, credential(t, "parent-0"), nil, nil)
	_, _ = s.Token(t.Context(), "mcp-gateway", nil)
	filed := f.Filed()
	if len(filed) != 1 || filed[0]["audience"] != "mcp-gateway" {
		t.Fatalf("no grant request: %v", filed)
	}
	if _, ok := filed[0]["principal"]; ok {
		t.Fatalf("the grant request names a principal: %v", filed[0])
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
	srv := newFake(t).Server
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
// left, so its failures keep the lost-answer marker: the mint itself is
// driven through one, and the next attempt refuses without presenting the
// parent again. (A standard transport that never connected clears it:
// TestARequestThatNeverLeftClearsTheMarker.)
func TestUnknownTransportFailureKeepsTheMarker(t *testing.T) {
	f := newFake(t)
	file := credential(t, "parent-0")
	s, err := New(Config{URL: f.URL, CredentialFile: file, Principal: "agent:test", HTTP: &http.Client{Transport: opaqueTransport{}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Token(t.Context(), "llm-gateway", nil); !errors.Is(err, ErrReenroll) {
		t.Fatalf("Token = %v, want ErrReenroll", err)
	}
	marker, err := tokenfile.Read(file + ".inflight")
	if err != nil || marker != "sha256:"+parentHash("parent-0") {
		t.Fatalf("marker %q, %v", marker, err)
	}
	if got, _ := tokenfile.Read(file); got != "parent-0" {
		t.Fatalf("parent = %q", got)
	}
	// A later attempt, even through a working transport, refuses before
	// presenting the parent.
	again, err := New(Config{URL: f.URL, CredentialFile: file, Principal: "agent:test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := again.Token(t.Context(), "llm-gateway", nil); !errors.Is(err, ErrReenroll) || f.Calls("/v1/token") != 0 {
		t.Fatalf("retry = %v after %d token requests", err, f.Calls("/v1/token"))
	}
	if tracesConnections(&http.Client{Transport: opaqueTransport{}}) || !tracesConnections(nil) || !tracesConnections(&http.Client{Transport: &http.Transport{}}) {
		t.Fatal("transport trust is wrong")
	}
}
