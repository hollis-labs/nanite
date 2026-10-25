// Package tetherregistrytest is a FAKE Tether registry for tests: an
// httptest server implementing the slice of the Tether API that enrollment
// uses (idempotent register, lookup, deregister). No test may talk to a real
// Tether; the token it accepts is an obviously fake string.
package tetherregistrytest

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/tetherregistry"
)

// Token is the only bearer the fake accepts. It is a fixture, not a credential.
const Token = "fake-registry-token-not-a-secret" // #nosec G101

// Entry is one registry row.
type Entry struct {
	URN, Substrate, ExternalID, DisplayName, Status string
}

// Fake is the fake Tether.
type Fake struct {
	Server *httptest.Server
	// Addr is what a client is pointed at: the server URL, or unix://<socket>.
	Addr string

	mu       sync.Mutex
	entries  map[string]*Entry // by URN
	byKey    map[string]string // external key -> URN
	seq      int
	failMode string // "", "drop", "slow", "status"
	failCode int
	failBody string
	delay    time.Duration

	Registers, Lookups, Deregisters int
	// Log is "METHOD escaped-path" per request, for path assertions.
	Log []string
	// LastAuth and LastHost are the last Authorization and Host headers seen.
	LastAuth, LastHost string
}

// New starts a fake Tether and registers its cleanup.
func New(t *testing.T) *Fake {
	t.Helper()
	f := &Fake{entries: map[string]*Entry{}, byKey: map[string]string{}}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	f.Addr = f.Server.URL
	t.Cleanup(f.Server.Close)
	return f
}

// NewUnix starts a fake Tether on a real unix socket (the local listener where
// the registry service token works) and returns the socket path. The socket
// lives in a short temp dir because unix socket paths are length-limited.
func NewUnix(t *testing.T) (*Fake, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "trs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "tetherd.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	f := &Fake{entries: map[string]*Entry{}, byKey: map[string]string{}, Addr: "unix://" + sock}
	f.Server = httptest.NewUnstartedServer(http.HandlerFunc(f.serve))
	f.Server.Listener = ln
	f.Server.Start()
	t.Cleanup(f.Server.Close)
	return f, sock
}

// Registrar returns a real tetherregistry.Client pointed at the fake, with the
// token file at tokenFile.
func (f *Fake) Registrar(tokenFile string) *tetherregistry.Client {
	return &tetherregistry.Client{
		Config: func() tetherregistry.Config {
			return tetherregistry.Config{Addr: f.Addr, TokenFile: tokenFile}
		},
		HTTP: &http.Client{Timeout: 2 * time.Second},
	}
}

// TokenFile writes the fake token to a 0600 file and returns its path.
func TokenFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "registry-token")
	if err := os.WriteFile(p, []byte(Token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// FailWith makes every request answer status with Tether's error envelope.
func (f *Fake) FailWith(status int, code string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failMode, f.failCode = "status", status
	f.failBody = fmt.Sprintf(`{"error":{"code":%q,"message":"fake tether says no"}}`, code)
}

// Drop makes the fake close every connection without answering (Tether down).
func (f *Fake) Drop() { f.set("drop") }

// Slow makes every request wait d before answering (client timeout).
func (f *Fake) Slow(d time.Duration) {
	f.mu.Lock()
	f.failMode, f.delay = "slow", d
	f.mu.Unlock()
}

// Heal clears any failure mode.
func (f *Fake) Heal() { f.set("") }

func (f *Fake) set(mode string) {
	f.mu.Lock()
	f.failMode = mode
	f.mu.Unlock()
}

// Entries returns a snapshot of the registry.
func (f *Fake) Entries() []Entry {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Entry
	for _, e := range f.entries {
		out = append(out, *e)
	}
	return out
}

// Counts returns the register, lookup and deregister call counts.
func (f *Fake) Counts() (register, lookup, deregister int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Registers, f.Lookups, f.Deregisters
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": msg}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (f *Fake) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.Log = append(f.Log, r.Method+" "+r.URL.EscapedPath())
	f.LastAuth = r.Header.Get("Authorization")
	f.LastHost = r.Host
	mode, code, body, delay := f.failMode, f.failCode, f.failBody, f.delay
	f.mu.Unlock()

	switch mode {
	case "drop":
		if hj, ok := w.(http.Hijacker); ok {
			if conn, _, err := hj.Hijack(); err == nil {
				_ = conn.Close()
			}
		}
		return
	case "slow":
		time.Sleep(delay)
	case "status":
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+Token {
		writeErr(w, http.StatusUnauthorized, "unauthorized", "bad token")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	path := r.URL.EscapedPath()
	switch {
	case path == "/registry/agents" && r.Method == http.MethodPost:
		f.register(w, r)
	case path == "/registry/agents" && r.Method == http.MethodGet:
		f.lookup(w, r)
	case strings.HasPrefix(path, "/registry/agents/") && r.Method == http.MethodDelete:
		f.deregister(w, strings.TrimPrefix(path, "/registry/agents/"))
	default:
		writeErr(w, http.StatusNotFound, "not_found", "no route")
	}
}

func (f *Fake) register(w http.ResponseWriter, r *http.Request) {
	f.Registers++
	var in struct {
		Substrate   string `json:"substrate"`
		ExternalID  string `json:"external_id"`
		ExternalKey string `json:"external_key"`
		DisplayName string `json:"display_name"`
		URN         string `json:"urn"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || in.URN != "" {
		writeErr(w, http.StatusBadRequest, "invalid_request", "bad body")
		return
	}
	sub, ext := in.Substrate, in.ExternalID
	if in.ExternalKey != "" {
		ks, ke, _ := strings.Cut(in.ExternalKey, "/")
		if (sub != "" && sub != ks) || (ext != "" && ext != ke) {
			writeErr(w, http.StatusBadRequest, "invalid_request", "conflicting key")
			return
		}
		sub, ext = ks, ke
	}
	if sub == "" || ext == "" {
		writeErr(w, http.StatusBadRequest, "invalid_request", "half a key")
		return
	}
	key := sub + "/" + ext
	if urn, ok := f.byKey[key]; ok {
		writeJSON(w, http.StatusOK, map[string]string{"urn": urn, "status": f.entries[urn].Status})
		return
	}
	f.seq++
	urn := fmt.Sprintf("msg://agent/agent-mux/agt_fake%06d", f.seq)
	f.entries[urn] = &Entry{URN: urn, Substrate: sub, ExternalID: ext, DisplayName: in.DisplayName, Status: "active"}
	f.byKey[key] = urn
	writeJSON(w, http.StatusCreated, map[string]string{"urn": urn, "status": "active"})
}

func (f *Fake) lookup(w http.ResponseWriter, r *http.Request) {
	f.Lookups++
	q := r.URL.Query()
	urn, ok := f.byKey[q.Get("substrate")+"/"+q.Get("external_id")]
	if !ok {
		writeErr(w, http.StatusNotFound, "not_found", "no such entry")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"agent": map[string]string{"urn": urn, "status": f.entries[urn].Status}})
}

func (f *Fake) deregister(w http.ResponseWriter, escaped string) {
	f.Deregisters++
	urn, err := url.PathUnescape(escaped)
	e, ok := f.entries[urn]
	if err != nil || !ok {
		writeErr(w, http.StatusNotFound, "not_found", "no such entry")
		return
	}
	e.Status = "deprecated"
	writeJSON(w, http.StatusOK, map[string]string{"urn": urn, "status": "deprecated"})
}
