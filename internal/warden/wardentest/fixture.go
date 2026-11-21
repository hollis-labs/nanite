// Package wardentest holds recorded Warden exchanges and the one fake Warden
// tests share. The fake answers from the recordings' wire shapes, so every
// test meets the contract the real Warden speaks rather than a guess at it.
package wardentest

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
)

// Fixture is one recorded Warden exchange, with every secret replaced: the
// presented parent credential, the minted token, the next parent, and any
// bearer token. Request headers are never kept.
type Fixture struct {
	Source   string   `json:"source"`
	Request  Request  `json:"request"`
	Response Response `json:"response"`
}

type Request struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	// Form is a form-encoded body's fields, sorted by name.
	Form map[string]string `json:"form,omitempty"`
}

type Response struct {
	Status      int             `json:"status"`
	ContentType string          `json:"content_type,omitempty"`
	Body        json.RawMessage `json:"body,omitempty"`
}

// Placeholders that stand in for secrets in a fixture.
const (
	PresentedParent = "<parent>"
	AccessToken     = "<access-token>"
	NextParent      = "<next-parent>"
)

// Dir is where the recorded fixtures live.
func Dir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "testdata")
}

// Load reads a recorded fixture by name.
func Load(t testing.TB, name string) Fixture {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(Dir(), name+".json")) // #nosec G304 -- A fixture in this package's testdata.
	if err != nil {
		t.Fatal(err)
	}
	var f Fixture
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return f
}

// Save writes a fixture, indented.
func Save(name string, f Fixture) error {
	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(Dir(), 0o750); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(Dir(), name+".json"), append(raw, '\n'), 0o600)
}

// scrubbed are the answer fields replaced in a fixture: the secrets, and the
// identifiers of the real identity and grant (not secret, but not the
// repository's to publish).
var scrubbed = map[string]string{
	"access_token": AccessToken, "token": AccessToken, "refresh_token": NextParent, "id_token": "<id-token>",
	"sub": Principal, "grant": "grt_fixture", "jti": "00000000-0000-4000-8000-000000000000",
}

// Principal is the identity fixtures are recorded as.
const Principal = "agent:nanite-fixture"

// jwtLike matches a JWT, whatever field carries it.
var jwtLike = regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`)

// scrubBody replaces the secrets in a Warden answer with placeholders and
// keeps everything else (field names, types, expiry values, error codes).
func scrubBody(raw []byte) json.RawMessage {
	var doc map[string]any
	if json.Unmarshal(raw, &doc) != nil {
		return json.RawMessage(jwtLike.ReplaceAllString(string(raw), "<jwt>"))
	}
	for key, placeholder := range scrubbed {
		if v, ok := doc[key].(string); ok && v != "" {
			doc[key] = placeholder
		}
	}
	out, _ := json.Marshal(doc)
	return json.RawMessage(jwtLike.ReplaceAll(out, []byte("<jwt>")))
}

// RecordingProxy forwards each request to upstream unchanged and keeps a
// scrubbed copy of the exchange. Tests point a warden.Source at its URL: the
// Source keeps its standard transport, so its lost-answer accounting is the
// same as against Warden itself, and it still receives (and saves) the real
// rotated parent. A failure reaching upstream answers 502, which the Source
// treats as a lost answer.
type RecordingProxy struct {
	*httptest.Server
	upstream string

	mu    sync.Mutex
	taken []Fixture
}

// NewRecordingProxy starts a proxy to upstream (Warden's base URL).
func NewRecordingProxy(t testing.TB, upstream string) *RecordingProxy {
	t.Helper()
	p := &RecordingProxy{upstream: strings.TrimRight(upstream, "/")}
	p.Server = httptest.NewServer(http.HandlerFunc(p.forward))
	t.Cleanup(p.Close)
	return p
}

func (p *RecordingProxy) forward(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "proxy: read request", http.StatusBadGateway)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, p.upstream+r.URL.RequestURI(), bytes.NewReader(body)) //nolint:gosec // G704: the test-configured Warden URL
	if err != nil {
		http.Error(w, "proxy: build request", http.StatusBadGateway)
		return
	}
	for _, name := range []string{"Content-Type", "Accept", "Authorization"} {
		if v := r.Header.Get(name); v != "" {
			req.Header.Set(name, v)
		}
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req) //nolint:gosec // G704: the configured Warden URL
	if err != nil {
		http.Error(w, "proxy: upstream unreachable", http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, "proxy: read upstream answer", http.StatusBadGateway)
		return
	}
	f := Fixture{Source: "recorded", Request: Request{Method: r.Method, Path: r.URL.Path},
		Response: Response{Status: resp.StatusCode, ContentType: resp.Header.Get("Content-Type"), Body: scrubBody(raw)}}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		if form, err := url.ParseQuery(string(body)); err == nil {
			f.Request.Form = map[string]string{}
			for k := range form {
				v := form.Get(k)
				if k == "refresh_token" {
					v = PresentedParent
				}
				f.Request.Form[k] = v
			}
		}
	}
	p.mu.Lock()
	p.taken = append(p.taken, f)
	p.mu.Unlock()
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(raw)
}

// Take returns the exchanges kept so far and clears them.
func (p *RecordingProxy) Take() []Fixture {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.taken
	p.taken = nil
	return out
}

// forbidden are strings no committed fixture may carry.
var forbidden = regexp.MustCompile(`(?i)https?://|\b[a-z0-9-]+\.(?:cloud|com|net|org|io|dev|ai|internal|local)\b|\b(?:\d{1,3}\.){3}\d{1,3}\b|eyJ[a-z0-9_-]{10,}|\bbearer\s+\S`)

// CheckClean fails when a fixture carries a hostname, an address, a JWT or
// a bearer token. It runs on each recording and over the committed files.
func CheckClean(t testing.TB, name string, f Fixture) {
	t.Helper()
	parts := []string{f.Source, f.Request.Method, f.Request.Path, f.Response.ContentType, string(f.Response.Body)}
	keys := make([]string, 0, len(f.Request.Form))
	for k := range f.Request.Form {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		parts = append(parts, k, f.Request.Form[k])
	}
	for _, s := range parts {
		if m := forbidden.FindString(s); m != "" {
			t.Fatalf("%s carries %q", name, m)
		}
	}
	if v, ok := f.Request.Form["refresh_token"]; ok && v != PresentedParent {
		t.Fatalf("%s carries a presented parent", name)
	}
}
