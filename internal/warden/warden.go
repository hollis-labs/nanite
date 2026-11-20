// Package warden gets and renews short-lived Warden access tokens from an
// agent's parent credential, for the providers and MCP servers configured
// with Warden auth.
//
// A parent credential rotates on every use: each token request returns the
// next parent, and presenting a spent one revokes the whole credential family.
// So every mint holds an exclusive lock on <file>.lock, marks <file>.inflight
// (with the hash of the parent it presents) before the request and writes
// the next parent atomically before the token is used. If the answer to a
// mint is lost (a timeout, a cut connection, a server error or a reply that
// cannot be read), the old parent may already be spent: the marker stays, and
// every later mint fails with ErrReenroll instead of presenting it again,
// unless the file now holds a different parent (the rotation was saved). A
// request that provably never left (the connection was never made) clears
// the marker. Re-enrolling (a new parent credential in the file) and removing
// the marker recovers.
//
// The Warden Go client (and so the warden CLI) takes the same <file>.lock but
// does not honor <file>.inflight: after a lost answer, using the credential
// file with it can present a spent parent and revoke the family. Do not share
// a credential file with it.
//
// A mint runs to completion even when the caller gives up: it is detached
// from the caller's context and bounded by its own timeout, so a cancelled
// turn never strands a request half-done.
//
// Tokens are cached per audience and scope set and renewed at two thirds of
// their life. A token never appears in an error, a log line or a URL.
package warden

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hollis-labs/libs/security/redact"
	"github.com/hollis-labs/libs/security/secrets/tokenfile"
)

// ErrReenroll means the parent credential can no longer be used safely: a
// mint's answer was lost, the rotated parent could not be saved, or Warden
// refused the credential. The owner (or whoever enrolled the agent) must
// place a new parent credential and remove the <file>.inflight marker.
var ErrReenroll = errors.New("warden: the parent credential needs re-enrollment")

// ErrRequestGone means Warden no longer shows a grant request to this agent
// (not found, or not its own): it will never be decided for it.
var ErrRequestGone = errors.New("warden: the grant request is gone")

// AuthWaitingActivity is the activity kind a host publishes for a Waiting
// (see Config.OnWaiting).
const AuthWaitingActivity = "nanite.auth.waiting"

// Waiting reports a grant request Nanite filed because the agent has no grant
// for an audience and scope set. Nothing in it is secret.
type Waiting struct {
	RequestID string   `json:"request_id"`
	Status    string   `json:"status"`
	Tier      string   `json:"tier,omitempty"`
	Audience  string   `json:"audience"`
	Scopes    []string `json:"scopes,omitempty"`
	// Conversation is the conversation whose request needed the grant
	// (WithConversation), "" when none was set.
	Conversation string `json:"conversation,omitempty"`
	// For names what needed the grant: a provider or an MCP server
	// (Source.BearerFor), "" when unnamed.
	For string `json:"for,omitempty"`
}

type conversationKey struct{}

type forKey struct{}

// WithConversation tags ctx with the conversation a token is needed for, so
// a grant request and its Waiting name it. An outer tag wins: a subtask's
// requests stay with the conversation that started it.
func WithConversation(ctx context.Context, id string) context.Context {
	if id == "" || conversationFrom(ctx) != "" {
		return ctx
	}
	return context.WithValue(ctx, conversationKey{}, id)
}

func conversationFrom(ctx context.Context) string {
	id, _ := ctx.Value(conversationKey{}).(string)
	return id
}

func forFrom(ctx context.Context) string {
	label, _ := ctx.Value(forKey{}).(string)
	return label
}

// MissingGrantError is returned while a grant request is pending. The turn
// that needed the token fails with it; a later request mints once the
// request is approved.
type MissingGrantError struct{ Waiting Waiting }

func (e *MissingGrantError) Error() string {
	return fmt.Sprintf("warden: no grant for audience %s yet; request %s is %s", e.Waiting.Audience, e.Waiting.RequestID, e.Waiting.Status)
}

var missingGrantText = regexp.MustCompile(`warden: no grant for audience (\S+) yet; request (\S+) is (\S+)`)

// AsMissingGrant finds a MissingGrantError in err, or its text where a
// provider or the engine carried the failure as a string.
func AsMissingGrant(err error) (Waiting, bool) {
	if err == nil {
		return Waiting{}, false
	}
	var missing *MissingGrantError
	if errors.As(err, &missing) {
		return missing.Waiting, true
	}
	m := missingGrantText.FindStringSubmatch(err.Error())
	if m == nil {
		return Waiting{}, false
	}
	return Waiting{Audience: m[1], RequestID: m[2], Status: m[3]}, true
}

// Config configures a Source.
type Config struct {
	// URL is Warden's base URL (https; http only for a loopback address).
	URL string
	// CredentialFile is the 0600 parent credential file.
	CredentialFile string
	// Principal is the agent's principal, for logs and the grant request.
	Principal string
	// Purpose explains a grant request to the approver.
	Purpose string
	// HTTP is the client for Warden; nil uses one with a 30s timeout.
	HTTP *http.Client
	// Redactor learns every token minted, so a token echoed anywhere is
	// scrubbed. Nil keeps a private scope.
	Redactor *redact.Scope
	// Logger reports mints and requests, never their secrets.
	Logger *slog.Logger
	// OnWaiting sees each grant request filed or found still pending.
	OnWaiting func(Waiting)
	// OnGranted sees a conversation that waited on a grant request get its
	// token (Waiting.Conversation; Status "granted").
	OnGranted func(Waiting)
	// Now is the clock; nil uses time.Now.
	Now func() time.Time
}

// Source mints and caches tokens for one agent.
type Source struct {
	cfg          Config
	mu           sync.Mutex
	tokens       map[string]*entry
	waiterMu     sync.Mutex
	waiterStore  WaiterStore
	savedWaiters map[string]savedWaiterEntry
}

type entry struct {
	mu           sync.Mutex // one mint at a time per key
	token        string
	renewAt      time.Time
	expiresAt    time.Time
	request      string // a pending grant request's ID
	requestSince time.Time
	// waiters is the conversations a token request failed for while a
	// grant request was pending, with that request's ID.
	waiters map[string]waiter
	checked time.Time
	// After a failed mint, nothing is tried again before retryAt; the wait
	// doubles from minBackoff to maxBackoff.
	failed  error
	retryAt time.Time
	backoff time.Duration
}

const (
	// mintTimeout bounds one mint, lock wait included.
	mintTimeout = 30 * time.Second
	minBackoff  = time.Second
	maxBackoff  = 30 * time.Second
)

// New returns a Source. It reads nothing until the first token is needed.
func New(cfg Config) (*Source, error) {
	if err := ValidateURL(cfg.URL); err != nil {
		return nil, err
	}
	if cfg.CredentialFile == "" {
		return nil, errors.New("warden: a credential file is required")
	}
	if cfg.HTTP == nil {
		cfg.HTTP = &http.Client{Timeout: mintTimeout}
	}
	// A redirect would resend the request, parent credential included, to
	// wherever Warden's answer points: never follow one.
	client := *cfg.HTTP
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	cfg.HTTP = &client
	if cfg.Redactor == nil {
		cfg.Redactor = redact.NewScope()
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Source{cfg: cfg, tokens: map[string]*entry{}}, nil
}

// ValidateURL accepts an https URL, or http for a loopback host.
func ValidateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" {
		return errors.New("warden: url must be an http(s) URL with a host and no credentials or query")
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if h := u.Hostname(); h == "localhost" || h == "127.0.0.1" || h == "::1" {
			return nil
		}
	}
	return errors.New("warden: url must be https (http only for a loopback address)")
}

func key(audience string, scopes []string) string {
	s := slices.Clone(scopes)
	slices.Sort(s)
	return audience + "\x00" + strings.Join(slices.Compact(s), " ")
}

func (s *Source) entry(audience string, scopes []string) *entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key(audience, scopes)
	e := s.tokens[k]
	if e == nil {
		e = &entry{}
		s.tokens[k] = e
	}
	return e
}

// Bearer is a token source for one audience and scope set.
type Bearer struct {
	s        *Source
	audience string
	scopes   []string
	label    string
}

// Bearer returns the token source for audience and scopes.
func (s *Source) Bearer(audience string, scopes []string) *Bearer {
	return s.BearerFor(audience, scopes, "")
}

// BearerFor is Bearer for a named user of the token ("provider gateway",
// "MCP server tools"), which a grant request and its Waiting name.
func (s *Source) BearerFor(audience string, scopes []string, label string) *Bearer {
	return &Bearer{s: s, audience: audience, scopes: slices.Clone(scopes), label: label}
}

// Token returns a valid token, minting one when the cached token is past two
// thirds of its life.
func (b *Bearer) Token(ctx context.Context) (string, error) {
	if b.label != "" && forFrom(ctx) == "" {
		ctx = context.WithValue(ctx, forKey{}, b.label)
	}
	return b.s.Token(ctx, b.audience, b.scopes)
}

// Invalidate drops the cached token if it is still token, after a resource
// server refused token; a newer token minted meanwhile is kept.
func (b *Bearer) Invalidate(token string) { b.s.Invalidate(b.audience, b.scopes, token) }

// Invalidate drops the cached token for audience and scopes if it is token;
// an empty token drops whatever is cached.
func (s *Source) Invalidate(audience string, scopes []string, token string) {
	e := s.entry(audience, scopes)
	e.mu.Lock()
	if token == "" || e.token == token {
		e.token, e.renewAt = "", time.Time{}
	}
	e.mu.Unlock()
}

// requestRecheck is how often a pending grant request is looked up again.
const requestRecheck = 30 * time.Second

// Token returns a valid token for audience and scopes.
func (s *Source) Token(ctx context.Context, audience string, scopes []string) (string, error) {
	// The Warden-audience request can be nested under another entry's lock.
	// Sweep only after the outer token operation has released its entry.
	if audience != wardenAudience {
		defer s.sweepWaiters()
	}
	e := s.entry(audience, scopes)
	e.mu.Lock()
	defer e.mu.Unlock()
	changed := pruneLiveWaiters(e, s.cfg.Now())
	previousRequest := e.request
	tok, err := s.token(ctx, e, audience, scopes)
	if previousRequest != e.request {
		changed = true
		e.requestSince = time.Time{}
		if e.request != "" {
			e.requestSince = s.cfg.Now()
		}
	}
	conversation := conversationFrom(ctx)
	var missing *MissingGrantError
	var granted *Waiting
	switch {
	case err == nil && conversation != "" && e.waiters[conversation].RequestID != "":
		id := e.waiters[conversation].RequestID
		delete(e.waiters, conversation)
		changed = true
		granted = &Waiting{RequestID: id, Status: "granted", Audience: audience, Scopes: slices.Clone(scopes), Conversation: conversation, For: forFrom(ctx)}
	case errors.As(err, &missing) && conversation != "":
		if e.waiters == nil {
			e.waiters = map[string]waiter{}
		}
		if e.waiters[conversation].RequestID != missing.Waiting.RequestID {
			e.waiters[conversation] = waiter{RequestID: missing.Waiting.RequestID, Since: s.cfg.Now()}
			changed = true
		}
	}
	if changed {
		s.saveWaiters(key(audience, scopes), e)
	}
	if granted != nil && s.cfg.OnGranted != nil {
		s.cfg.OnGranted(*granted)
	}
	return tok, err
}

func (s *Source) token(ctx context.Context, e *entry, audience string, scopes []string) (string, error) {
	now := s.cfg.Now()
	if e.token != "" && now.Before(e.renewAt) {
		return e.token, nil
	}
	if e.failed != nil && now.Before(e.retryAt) {
		if e.token != "" && now.Before(e.expiresAt) {
			return e.token, nil
		}
		return "", e.failed
	}
	if e.request != "" {
		w, done, err := s.pending(ctx, e, audience, scopes)
		if err != nil || !done {
			if err == nil {
				w = s.waiting(ctx, w)
				err = &MissingGrantError{Waiting: w}
			}
			return "", err
		}
	}
	t, err := s.mint(ctx, audience, scopes)
	var refused *refusal
	if errors.As(err, &refused) && refused.code == "invalid_scope" && audience != wardenAudience {
		w, rerr := s.file(ctx, audience, scopes)
		if rerr != nil {
			return "", fmt.Errorf("warden: no grant for audience %s, and the grant request failed: %w", audience, rerr)
		}
		if w.Status == "approved" || w.Status == "granted" {
			t, err = s.mint(ctx, audience, scopes)
		} else {
			e.request, e.checked = w.RequestID, s.cfg.Now()
			w = s.waiting(ctx, w)
			return "", &MissingGrantError{Waiting: w}
		}
	}
	if err != nil {
		var missing *MissingGrantError
		if !errors.As(err, &missing) {
			e.backoff = min(max(e.backoff*2, minBackoff), maxBackoff)
			e.failed, e.retryAt = err, now.Add(e.backoff)
		}
		// A cached token still inside its life is better than none.
		if e.token != "" && now.Before(e.expiresAt) {
			s.cfg.Logger.Warn("warden: renewal failed; using the current token", "audience", audience, "error", err.Error())
			return e.token, nil
		}
		return "", err
	}
	e.request, e.failed, e.backoff = "", nil, 0
	e.token, e.expiresAt = t.token, t.expiresAt
	e.renewAt = now.Add(t.expiresAt.Sub(now) * 2 / 3)
	return e.token, nil
}

// pending looks up a filed grant request, at most every requestRecheck. done
// means it was approved and a mint may follow.
func (s *Source) pending(ctx context.Context, e *entry, audience string, scopes []string) (Waiting, bool, error) {
	w := Waiting{RequestID: e.request, Status: "pending", Audience: audience, Scopes: slices.Clone(scopes)}
	if s.cfg.Now().Sub(e.checked) < requestRecheck {
		return w, false, nil
	}
	e.checked = s.cfg.Now()
	got, err := s.lookup(ctx, e.request)
	if err != nil {
		s.cfg.Logger.Warn("warden: grant request lookup failed", "request", e.request, "error", err.Error())
		return w, false, nil
	}
	got.Audience, got.Scopes = audience, slices.Clone(scopes)
	switch got.Status {
	case "approved", "granted":
		e.request = ""
		return got, true, nil
	case "pending", "":
		return got, false, nil
	default: // denied, expired, withdrawn: file afresh on the next attempt
		e.request = ""
		return got, false, fmt.Errorf("warden: grant request %s for audience %s was %s", got.RequestID, audience, got.Status)
	}
}

// waiting tags w with ctx's conversation and user and reports it. It is
// called for every token request that finds a grant missing, so OnWaiting
// sees a pending request again on each attempt and should deduplicate.
func (s *Source) waiting(ctx context.Context, w Waiting) Waiting {
	w.Conversation, w.For = conversationFrom(ctx), forFrom(ctx)
	s.cfg.Logger.Info("warden: waiting for a grant", "request", w.RequestID, "status", w.Status, "audience", w.Audience)
	if s.cfg.OnWaiting != nil {
		s.cfg.OnWaiting(w)
	}
	return w
}

// Request looks up a grant request's current status ("pending", "approved",
// "denied", ...). A watcher uses it to learn the decision.
func (s *Source) Request(ctx context.Context, id string) (Waiting, error) {
	return s.lookup(ctx, id)
}

// Decided tells the source a grant request has been decided. On approval
// ("approved" or "granted") the next token request looks it up at once
// instead of waiting out the recheck interval; on any other decision the
// request is forgotten ("denied", "expired" or "withdrawn"), so the next
// token request files a new one. An empty or unknown status is not a
// decision and changes nothing.
func (s *Source) Decided(id, status string) {
	var approved bool
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "approved", "granted":
		approved = true
	case "denied", "expired", "withdrawn":
	default:
		return
	}
	s.mu.Lock()
	entries := make(map[string]*entry, len(s.tokens))
	for k, e := range s.tokens {
		entries[k] = e
	}
	s.mu.Unlock()
	for k, e := range entries {
		e.mu.Lock()
		changed := pruneLiveWaiters(e, s.cfg.Now())
		if e.request == id {
			if approved {
				e.checked = time.Time{}
			} else {
				e.request = ""
				e.requestSince = time.Time{}
				changed = true
			}
		}
		if !approved {
			for c, req := range e.waiters {
				if req.RequestID == id {
					delete(e.waiters, c)
					changed = true
				}
			}
		}
		if changed {
			s.saveWaiters(k, e)
		}
		e.mu.Unlock()
	}
}

type minted struct {
	token     string
	expiresAt time.Time
}

// refusal is Warden refusing a request with an OAuth or API error code. The
// parent credential is not spent by a refusal: Warden checks the request
// before it rotates the parent, in one transaction.
type refusal struct {
	status int
	code   string
}

func (r *refusal) Error() string {
	return fmt.Sprintf("warden refused the request: %s (status %d)", r.code, r.status)
}

// oauthCodes are the OAuth error codes Warden decides before it rotates the
// parent, in the same transaction: a refusal carrying one left the parent
// unspent. Any other failed answer may have spent it.
var oauthCodes = map[string]bool{
	"invalid_request": true, "invalid_grant": true, "invalid_scope": true, "invalid_target": true,
	"invalid_client": true, "unauthorized_client": true, "unsupported_grant_type": true, "access_denied": true,
}

func parentHash(parent string) string {
	sum := sha256.Sum256([]byte(parent))
	return hex.EncodeToString(sum[:])
}

// mint redeems the parent credential for a token, under the file lock. It
// runs detached from ctx (see the package doc) with its own timeout.
func (s *Source) mint(ctx context.Context, audience string, scopes []string) (minted, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), mintTimeout)
	defer cancel()
	file := s.cfg.CredentialFile
	unlock, err := lockFile(ctx, file+".lock")
	if err != nil {
		return minted{}, fmt.Errorf("warden: lock the parent credential: %w", err)
	}
	defer unlock()
	parent, err := tokenfile.Read(file)
	if err != nil {
		return minted{}, fmt.Errorf("warden: read the parent credential: %w", err)
	}
	if marker, err := tokenfile.Read(file + ".inflight"); err == nil {
		presented, ok := strings.CutPrefix(marker, "sha256:")
		if !ok || presented == parentHash(parent) {
			return minted{}, fmt.Errorf("%w: an earlier token request's answer was lost (%s.inflight exists)", ErrReenroll, file)
		}
		// The file holds another parent than the one the lost request
		// presented: its rotation was saved, so the request is settled.
		if err := os.Remove(file + ".inflight"); err != nil {
			return minted{}, fmt.Errorf("warden: clear the token request marker: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return minted{}, fmt.Errorf("%w: the token request marker cannot be read: %w", ErrReenroll, err)
	}
	if err := tokenfile.Write(file+".inflight", "sha256:"+parentHash(parent)); err != nil {
		return minted{}, fmt.Errorf("warden: mark the token request: %w", err)
	}
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {parent}, "audience": {audience}}
	if len(scopes) > 0 {
		form.Set("scope", strings.Join(scopes, " "))
	}
	var answer struct {
		AccessToken  string    `json:"access_token"`
		ExpiresAt    time.Time `json:"expires_at"`
		ExpiresIn    int       `json:"expires_in"`
		TTL          int       `json:"ttl_seconds"`
		RefreshToken string    `json:"refresh_token"`
	}
	sent := false
	status, err := s.send(ctx, http.MethodPost, "/v1/token", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()), "", &answer, &sent)
	if err != nil {
		var r *refusal
		switch {
		case !sent:
			// The connection was never made: Warden never saw the parent.
			_ = os.Remove(file + ".inflight")
			return minted{}, err
		case errors.As(err, &r) && status >= 400 && status < 500 && oauthCodes[r.code]:
			// A refusal is decided before the parent rotates: it is unspent.
			_ = os.Remove(file + ".inflight")
			if r.code == "invalid_grant" {
				return minted{}, fmt.Errorf("%w: warden refused the parent credential (%s)", ErrReenroll, r.code)
			}
			return minted{}, err
		}
		// No answer, a server error, a redirect or an unreadable answer: the
		// parent may be spent. Leave the marker.
		return minted{}, fmt.Errorf("%w: the token request's answer was lost: %w", ErrReenroll, err)
	}
	if answer.RefreshToken == "" {
		return minted{}, fmt.Errorf("%w: the token answer carries no next parent credential", ErrReenroll)
	}
	s.cfg.Redactor.Add("warden parent credential", answer.RefreshToken)
	if err := tokenfile.Write(file, answer.RefreshToken); err != nil {
		return minted{}, fmt.Errorf("%w: the rotated parent credential could not be saved: %w", ErrReenroll, err)
	}
	if err := os.Remove(file + ".inflight"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return minted{}, fmt.Errorf("warden: clear the token request marker: %w", err)
	}
	if answer.AccessToken == "" {
		return minted{}, errors.New("warden: the token answer carries no access token (the rotated parent credential was saved)")
	}
	s.cfg.Redactor.Add("warden token", answer.AccessToken)
	expires := answer.ExpiresAt
	if expires.IsZero() {
		ttl := answer.ExpiresIn
		if ttl == 0 {
			ttl = answer.TTL
		}
		if ttl <= 0 {
			ttl = 300
		}
		expires = s.cfg.Now().Add(time.Duration(ttl) * time.Second)
	}
	s.cfg.Logger.Debug("warden: minted a token", "audience", audience, "expires_at", expires.UTC().Format(time.RFC3339))
	return minted{token: answer.AccessToken, expiresAt: expires}, nil
}

// requestScopes are what the agent needs to file and read its own grant
// requests; Warden grants them to every enrolled principal at the ground
// tier.
var requestScopes = []string{"warden.request", "warden.read"}

// wardenAudience is Warden's own audience. A missing grant for it is not
// requested (that would need the grant it lacks).
const wardenAudience = "warden"

// file files a grant request for audience and scopes, with a token for
// Warden itself.
func (s *Source) file(ctx context.Context, audience string, scopes []string) (Waiting, error) {
	bearer, err := s.Token(ctx, wardenAudience, requestScopes)
	if err != nil {
		return Waiting{}, err
	}
	body, err := json.Marshal(map[string]any{"audience": audience, "scopes": scopes, "purpose": s.purpose(ctx)})
	if err != nil {
		return Waiting{}, err
	}
	var r struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Tier   string `json:"tier"`
	}
	if _, err := s.post(ctx, "/api/v1/requests", "application/json", strings.NewReader(string(body)), bearer, &r); err != nil {
		return Waiting{}, err
	}
	if r.ID == "" {
		return Waiting{}, errors.New("warden: the grant request answer has no id")
	}
	return Waiting{RequestID: r.ID, Status: r.Status, Tier: r.Tier, Audience: audience, Scopes: slices.Clone(scopes)}, nil
}

func (s *Source) lookup(ctx context.Context, id string) (Waiting, error) {
	bearer, err := s.Token(ctx, wardenAudience, requestScopes)
	if err != nil {
		return Waiting{}, err
	}
	var r struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Tier   string `json:"tier"`
	}
	if status, err := s.do(ctx, http.MethodGet, "/api/v1/requests/"+url.PathEscape(id), "", nil, bearer, &r); err != nil {
		if status == http.StatusNotFound || status == http.StatusForbidden {
			return Waiting{}, fmt.Errorf("%w (status %d)", ErrRequestGone, status)
		}
		return Waiting{}, err
	}
	return Waiting{RequestID: id, Status: r.Status, Tier: r.Tier}, nil
}

// purpose explains a grant request to the approver: the configured purpose
// or a default, plus what needed it and for which conversation.
func (s *Source) purpose(ctx context.Context) string {
	what := "a configured provider or MCP server"
	if label := forFrom(ctx); label != "" {
		what = label
	}
	text := s.cfg.Purpose
	if text == "" {
		text = "Nanite agent " + s.cfg.Principal + " needs this grant for " + what
	} else {
		text += " (needed for " + what + ")"
	}
	if id := conversationFrom(ctx); id != "" {
		text += "; conversation " + id
	}
	return text
}

func (s *Source) post(ctx context.Context, path, ctype string, body io.Reader, bearer string, out any) (int, error) {
	return s.send(ctx, http.MethodPost, path, ctype, body, bearer, out, nil)
}

func (s *Source) do(ctx context.Context, method, path, ctype string, body io.Reader, bearer string, out any) (int, error) {
	return s.send(ctx, method, path, ctype, body, bearer, out, nil)
}

// send sends one request. Its errors never carry the URL, the body or a
// token. With sent set, it reports whether a connection was made for the
// request (so it may have reached the server); false means it provably
// never left.
func (s *Source) send(ctx context.Context, method, path, ctype string, body io.Reader, bearer string, out any, sent *bool) (int, error) {
	var connected atomic.Bool
	if sent != nil {
		ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{GotConn: func(httptrace.GotConnInfo) { connected.Store(true) }})
		// Only the standard transport is known to report GotConn before
		// writing a request; with any other, assume the request may have
		// left, so a failure keeps the lost-answer marker.
		reports := tracesConnections(s.cfg.HTTP)
		defer func() { *sent = connected.Load() || !reports }()
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(s.cfg.URL, "/")+path, body)
	if err != nil {
		return 0, errors.New("warden: invalid request")
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	req.Header.Set("Accept", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := s.cfg.HTTP.Do(req) //nolint:gosec // G704: the configured Warden URL
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // never echo the URL
		}
		return 0, fmt.Errorf("warden: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resp.StatusCode, fmt.Errorf("warden: read the answer: %w", err)
	}
	connected.Store(true)
	if resp.StatusCode/100 != 2 {
		r := &refusal{status: resp.StatusCode, code: errorCode(raw)}
		if r.code == "" {
			r.code = strconv.Itoa(resp.StatusCode)
		}
		if resp.StatusCode >= 500 {
			return resp.StatusCode, fmt.Errorf("warden: server error (status %d)", resp.StatusCode)
		}
		return resp.StatusCode, r
	}
	if out != nil && json.Unmarshal(raw, out) != nil {
		return resp.StatusCode, errors.New("warden: unreadable answer")
	}
	return resp.StatusCode, nil
}

// errorCode reads an OAuth error ({"error":"code"}) or the Substrate shape
// ({"error":{"code":...}}).
func errorCode(raw []byte) string {
	var e struct {
		Error any    `json:"error"`
		Code  string `json:"code"`
	}
	if json.Unmarshal(raw, &e) != nil {
		return ""
	}
	switch v := e.Error.(type) {
	case string:
		return v
	case map[string]any:
		if c, ok := v["code"].(string); ok {
			return c
		}
	}
	return e.Code
}

// tracesConnections reports whether the client's transport is the standard
// one, which calls httptrace GotConn before it writes a request.
func tracesConnections(c *http.Client) bool {
	if c == nil {
		return true
	}
	switch c.Transport.(type) {
	case nil, *http.Transport:
		return true
	}
	return false
}
