package wardentest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// Expiry selects which lifetime fields a fake mint answer carries. The real
// Warden sends all three (Recorded); the others pin the client's fallbacks.
type Expiry int

const (
	// Recorded: expires_at, expires_in and ttl_seconds, as Warden sends them.
	Recorded Expiry = iota
	// ExpiresAtOnly: only expires_at.
	ExpiresAtOnly
	// ExpiresInOnly: only expires_in.
	ExpiresInOnly
	// TTLSecondsOnly: only ttl_seconds.
	TTLSecondsOnly
	// NoTTL: no lifetime at all; the client falls back to its default.
	NoTTL
)

// Fake is the one fake Warden tests share. It answers the token endpoint
// and the grant-request API in the recorded wire shapes (testdata): a mint
// rotates the parent, refusals use the recorded OAuth bodies, and the API's
// errors the recorded {"code","message"} bodies. Grant requests follow
// Warden's api.createRequest and client.Request (filing answers 202 while
// pending, 201 once granted).
type Fake struct {
	*httptest.Server
	t testing.TB

	mu      sync.Mutex
	parent  string
	mints   int
	grants  map[string]map[string]bool // audience -> granted scopes; "*" grants any
	expiry  Expiry
	ttl     int
	now     func() time.Time
	noNext  bool
	tokens  func(n int, audience string) string
	parents func(n int) string
	// fileStatus and fileTier are what a new grant request gets.
	fileStatus, fileTier string
	// lose is how the next mint's answer is lost after Warden rotated the
	// parent: "500" or "drop" (the connection is cut).
	lose      string
	refused   int
	requests  map[string]*grantRequest
	order     []string
	forbidden map[string]bool
	failures  map[string][]failure
	broken    map[string]int // path -> status it answers until mended
	forms     []url.Values
	filed     []map[string]any
	calls     map[string]int
}

type grantRequest struct {
	audience, tier string
	scopes         []string
	status         string
}

type failure struct {
	status int
	body   string
}

// NewFake starts a fake Warden whose current parent credential is parent.
// Warden's own audience is granted for filing and reading requests, as the
// ground tier grants it; Grant adds others.
func NewFake(t testing.TB, parent string) *Fake {
	t.Helper()
	f := &Fake{t: t, parent: parent, ttl: 899, now: time.Now, fileStatus: "pending",
		fileTier: "ground",
		grants:   map[string]map[string]bool{"warden": {"warden.read": true, "warden.request": true}},
		requests: map[string]*grantRequest{}, forbidden: map[string]bool{}, failures: map[string][]failure{}, broken: map[string]int{}, calls: map[string]int{},
		tokens:  func(n int, audience string) string { return fmt.Sprintf("fixture-token-%s-%d", audience, n) },
		parents: func(n int) string { return fmt.Sprintf("fixture-parent-%d", n) },
	}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	return f
}

// Grant lets the identity mint tokens for audience with scopes; with none,
// with any scopes.
func (f *Fake) Grant(audience string, scopes ...string) *Fake {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.grantLocked(audience, scopes)
	return f
}

func (f *Fake) grantLocked(audience string, scopes []string) {
	if f.grants[audience] == nil {
		f.grants[audience] = map[string]bool{}
	}
	if len(scopes) == 0 {
		f.grants[audience]["*"] = true
	}
	for _, s := range scopes {
		f.grants[audience][s] = true
	}
}

// LoseNextAnswer makes the next mint rotate the parent and then lose its
// answer: "500" answers a server error, "drop" cuts the connection.
func (f *Fake) LoseNextAnswer(mode string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lose = mode
}

// Refused is how many token requests presented a parent Warden does not
// hold (answered invalid_grant).
func (f *Fake) Refused() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.refused
}

// Expire sets the lifetime fields mint answers carry and their seconds.
func (f *Fake) Expire(e Expiry, seconds int) *Fake {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.expiry, f.ttl = e, seconds
	return f
}

// Clock sets the fake's clock, for expires_at.
func (f *Fake) Clock(now func() time.Time) *Fake {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = now
	return f
}

// Tokens names the access token of the n-th mint (from 1) for audience.
func (f *Fake) Tokens(name func(n int, audience string) string) *Fake {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokens = name
	return f
}

// Parents names the parent the n-th mint (from 1) rotates to.
func (f *Fake) Parents(name func(n int) string) *Fake {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.parents = name
	return f
}

// OmitNextParent makes mint answers carry no refresh_token.
func (f *Fake) OmitNextParent() *Fake {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.noNext = true
	return f
}

// FileAs sets the status and tier new grant requests get: "pending" (the
// default) or "granted", which grants the scopes at once; tier "ground" by
// default.
func (f *Fake) FileAs(status, tier string) *Fake {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fileStatus, f.fileTier = status, tier
	return f
}

// Decide sets a filed grant request's status; "granted" or "approved"
// grants its scopes.
func (f *Fake) Decide(id, status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.requests[id]
	if r == nil {
		f.t.Errorf("wardentest: no grant request %s", id)
		return
	}
	r.status = status
	if status == "granted" || status == "approved" {
		f.grantLocked(r.audience, r.scopes)
	}
}

// Forbid makes a grant request's lookup answer 403, as for another
// principal's request.
func (f *Fake) Forbid(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forbidden[id] = true
}

// Remove deletes a grant request: its lookups answer the recorded 404.
func (f *Fake) Remove(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.requests, id)
}

// Break makes every request to path ("/v1/token" or "/api/v1/requests")
// answer status with no body, until Mend.
func (f *Fake) Break(path string, status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.broken[path] = status
}

// Mend undoes Break.
func (f *Fake) Mend(path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.broken, path)
}

// FailNext answers the next request to path ("/v1/token" or
// "/api/v1/requests") with status and a JSON body.
func (f *Fake) FailNext(path string, status int, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures[path] = append(f.failures[path], failure{status, body})
}

// Parent is the parent credential Warden accepts now.
func (f *Fake) Parent() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.parent
}

// Mints is how many tokens were minted.
func (f *Fake) Mints() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mints
}

// Calls is how many requests reached path.
func (f *Fake) Calls(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[path]
}

// Hits is how many requests reached the fake, on any path.
func (f *Fake) Hits() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		n += c
	}
	return n
}

// Forms are the token requests' forms, in order.
func (f *Fake) Forms() []url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.forms)
}

// Filed are the grant request bodies, in order.
func (f *Fake) Filed() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.filed)
}

// Requests are the filed grant request IDs, in order.
func (f *Fake) Requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.order)
}

func (f *Fake) serve(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if strings.HasPrefix(path, "/api/v1/requests/") {
		path = "/api/v1/requests"
	}
	f.mu.Lock()
	f.calls[r.URL.Path]++
	if status := f.broken[path]; status != 0 {
		f.mu.Unlock()
		w.WriteHeader(status)
		return
	}
	if q := f.failures[path]; len(q) > 0 {
		f.failures[path] = q[1:]
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(q[0].status)
		_, _ = fmt.Fprint(w, q[0].body)
		return
	}
	f.mu.Unlock()
	switch {
	case r.URL.Path == "/v1/token" && r.Method == http.MethodPost:
		f.token(w, r)
	case r.URL.Path == "/api/v1/requests" && r.Method == http.MethodPost:
		f.file(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/v1/requests/") && r.Method == http.MethodGet:
		f.lookup(w, r, strings.TrimPrefix(r.URL.Path, "/api/v1/requests/"))
	default:
		f.recorded(w, "lookup_missing")
	}
}

// recorded answers with a recorded fixture's status and body.
func (f *Fake) recorded(w http.ResponseWriter, name string) {
	fx := Load(f.t, name)
	w.Header().Set("Content-Type", fx.Response.ContentType)
	w.WriteHeader(fx.Response.Status)
	_, _ = w.Write(fx.Response.Body)
}

func (f *Fake) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil || r.PostForm.Get("grant_type") != "refresh_token" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_request", "error_description": "unreadable token request"})
		return
	}
	f.mu.Lock()
	f.forms = append(f.forms, r.PostForm)
	if r.PostForm.Get("refresh_token") != f.parent {
		f.refused++
		f.mu.Unlock()
		f.recorded(w, "mint_invalid_grant")
		return
	}
	audience := r.PostForm.Get("audience")
	scopes := strings.Fields(r.PostForm.Get("scope"))
	granted := f.grants[audience]
	for _, s := range scopes {
		if !granted[s] && !granted["*"] {
			f.mu.Unlock()
			fx := Load(f.t, "mint_invalid_scope")
			var body map[string]any
			_ = json.Unmarshal(fx.Response.Body, &body)
			body["error_description"] = "no live grant covers audience " + audience + " with those scopes"
			writeJSON(w, fx.Response.Status, body)
			return
		}
	}
	if len(granted) == 0 {
		f.mu.Unlock()
		f.recorded(w, "mint_invalid_scope")
		return
	}
	if lose := f.lose; lose != "" {
		// Warden rotated the parent; the answer never arrives.
		f.lose = ""
		f.parent = "<rotated-then-lost>"
		f.mu.Unlock()
		if lose == "drop" {
			if hj, ok := w.(http.Hijacker); ok {
				if conn, _, err := hj.Hijack(); err == nil {
					_ = conn.Close()
					return
				}
			}
		}
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	f.mints++
	n := f.mints
	token, next := f.tokens(n, audience), f.parents(n)
	f.parent = next
	expiry, ttl, now, noNext := f.expiry, f.ttl, f.now(), f.noNext
	f.mu.Unlock()

	writeJSON(w, http.StatusOK, MintAnswer(f.t, MintFields{Token: token, NextParent: next, Audience: audience, Scopes: scopes,
		Expiry: expiry, TTL: ttl, Now: now, OmitNextParent: noNext}))
}

// bearer reports whether a request carries a token minted for Warden.
func (f *Fake) bearer(r *http.Request) bool {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || tok == "" {
		return false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for n := 1; n <= f.mints; n++ {
		if f.tokens(n, "warden") == tok {
			return true
		}
	}
	return false
}

func (f *Fake) file(w http.ResponseWriter, r *http.Request) {
	if !f.bearer(r) {
		f.recorded(w, "lookup_unauthorized")
		return
	}
	var in map[string]any
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"code": "invalid_request", "message": "invalid JSON body"})
		return
	}
	audience, _ := in["audience"].(string)
	var scopes []string
	if list, ok := in["scopes"].([]any); ok {
		for _, s := range list {
			if v, ok := s.(string); ok {
				scopes = append(scopes, v)
			}
		}
	}
	f.mu.Lock()
	id := fmt.Sprintf("req-%d", len(f.order)+1)
	status := f.fileStatus
	f.requests[id] = &grantRequest{audience: audience, scopes: scopes, status: status, tier: f.fileTier}
	f.order = append(f.order, id)
	f.filed = append(f.filed, in)
	if status == "granted" || status == "approved" {
		f.grantLocked(audience, scopes)
	}
	body := f.requestBody(id)
	f.mu.Unlock()
	code := http.StatusCreated
	if status == "pending" {
		code = http.StatusAccepted
	}
	writeJSON(w, code, body)
}

func (f *Fake) lookup(w http.ResponseWriter, r *http.Request, id string) {
	if !f.bearer(r) {
		f.recorded(w, "lookup_unauthorized")
		return
	}
	f.mu.Lock()
	_, known := f.requests[id]
	forbidden := f.forbidden[id]
	var body map[string]any
	if known {
		body = f.requestBody(id)
	}
	f.mu.Unlock()
	switch {
	case forbidden:
		writeJSON(w, http.StatusForbidden, map[string]any{"code": "forbidden", "message": "not your request"})
	case !known:
		f.recorded(w, "lookup_missing")
	default:
		writeJSON(w, http.StatusOK, body)
	}
}

// requestBody is a grant request in Warden's client.Request shape. The
// caller holds f.mu.
func (f *Fake) requestBody(id string) map[string]any {
	r := f.requests[id]
	now := f.now().UTC()
	return map[string]any{"id": id, "action": "grant", "principal": Principal, "audience": r.audience, "scopes": r.scopes,
		"tier": r.tier, "status": r.status, "created_at": now.Format(time.RFC3339), "expires_at": now.Add(7 * 24 * time.Hour).Format(time.RFC3339)}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// MintFields fill a mint answer.
type MintFields struct {
	Token, NextParent, Audience string
	Scopes                      []string
	Expiry                      Expiry
	TTL                         int
	Now                         time.Time
	OmitNextParent              bool
}

// MintAnswer is a successful token answer in the recorded shape (mint_ok),
// with the given values. A test with its own handler answers with it, so
// it never guesses the wire format.
func MintAnswer(t testing.TB, m MintFields) map[string]any {
	t.Helper()
	body, err := mintAnswer(m)
	if err != nil {
		t.Fatalf("wardentest: %v", err)
	}
	return body
}

// MintAnswerBody is MintAnswer for a handler with no test at hand; it
// panics if the recorded fixture cannot be read.
func MintAnswerBody(m MintFields) map[string]any {
	body, err := mintAnswer(m)
	if err != nil {
		panic("wardentest: " + err.Error())
	}
	return body
}

func mintAnswer(m MintFields) (map[string]any, error) {
	raw, err := os.ReadFile(filepath.Join(Dir(), "mint_ok.json")) // #nosec G304 -- A fixture in this package's testdata.
	if err != nil {
		return nil, err
	}
	var fx Fixture
	if err := json.Unmarshal(raw, &fx); err != nil {
		return nil, err
	}
	var body map[string]any
	if err := json.Unmarshal(fx.Response.Body, &body); err != nil {
		return nil, err
	}
	if m.Now.IsZero() {
		m.Now = time.Now()
	}
	if m.TTL == 0 {
		m.TTL = 899
	}
	sorted := slices.Clone(m.Scopes)
	slices.Sort(sorted)
	body["access_token"], body["token"], body["refresh_token"] = m.Token, m.Token, m.NextParent
	body["aud"], body["scope"] = m.Audience, strings.Join(sorted, " ")
	body["expires_at"] = m.Now.Add(time.Duration(m.TTL) * time.Second).UTC().Format(time.RFC3339)
	body["expires_in"], body["ttl_seconds"] = m.TTL, m.TTL
	switch m.Expiry {
	case ExpiresAtOnly:
		delete(body, "expires_in")
		delete(body, "ttl_seconds")
	case ExpiresInOnly:
		delete(body, "expires_at")
		delete(body, "ttl_seconds")
	case TTLSecondsOnly:
		delete(body, "expires_at")
		delete(body, "expires_in")
	case NoTTL:
		delete(body, "expires_at")
		delete(body, "expires_in")
		delete(body, "ttl_seconds")
	case Recorded:
	}
	if m.OmitNextParent {
		delete(body, "refresh_token")
	}
	return body, nil
}
