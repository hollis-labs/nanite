// Package tetherregistry is Nanite's client for the Tether registry, used only
// by durable actor enrollment (CW-20261024-0017). It speaks the Tether API as
// shipped in build 49a3a6b2 (PRs #516/#517): idempotent registration on an
// external key, lookup by external id, and deregistration.
//
// The registry credential is a Tether token scoped tether.registry
// (registry.write). It is a SEPARATE credential from the bridge's messages/read
// token and lives in its own 0600 file read at call time. It is never accepted
// from a request, never logged, never returned and never stored in the
// database.
package tetherregistry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// AddrEnv is the Tether daemon address of the delivery bridge (an http(s)
	// URL, or a unix socket path). The registry client falls back to it when
	// RegistryAddrEnv is unset.
	AddrEnv = "NANITE_TETHER_ADDR"
	// RegistryAddrEnv is the address the registry client uses, separate from
	// the bridge's: unix:///abs/path/tetherd.sock (or a bare absolute path), or
	// an http(s) URL. A registry service token authenticates only on Tether's
	// local unix socket; the remote (TCP/https) listener accepts paired device
	// credentials only and answers 401 by design.
	RegistryAddrEnv = "NANITE_TETHER_REGISTRY_ADDR"
	// TokenFileEnv names the 0600 file holding the registry token.
	TokenFileEnv = "NANITE_TETHER_REGISTRY_TOKEN_FILE"
	// Substrate is the registry substrate Nanite registers under.
	Substrate = "nanite"

	maxResponse    = 1 << 20
	defaultTimeout = 15 * time.Second
)

// Typed errors. Callers match with errors.Is / errors.As.
var (
	// ErrNotConfigured: no Tether address is set. Enrollment is optional and
	// Nanite works without Tether.
	ErrNotConfigured = errors.New("tether is not configured: set " + RegistryAddrEnv + " (or " + AddrEnv + ")")
	// ErrAddrInvalid: the configured address is neither an http(s) URL nor an
	// absolute unix socket path, or the socket path is not a socket.
	ErrAddrInvalid = errors.New("tether registry address is invalid: use unix:///absolute/path.sock or an http(s) URL")
	// ErrTokenMissing: no registry token file is configured, or it is absent or empty.
	ErrTokenMissing = errors.New("tether registry token is not configured: set " + TokenFileEnv + " to a 0600 file holding a tether.registry token")
	// ErrTokenInsecure: the token file is readable by group or others.
	ErrTokenInsecure = errors.New("tether registry token file must not be accessible by group or others (chmod 600)")
	// ErrUnreachable is the retryable failure: Tether did not answer, timed
	// out, or answered 5xx. Nothing was committed on the Tether side that the
	// caller can rely on; repeating the call is safe.
	ErrUnreachable = errors.New("tether is unreachable (retryable)")
	// ErrBadResponse: Tether answered 2xx with a body Nanite cannot use.
	ErrBadResponse = errors.New("tether returned an unusable response")
)

// RefusedError is a 4xx answer. Code is the error code from Tether's error
// envelope (for example invalid_request); the message text is not kept, so
// nothing a server echoes can reach a log or a response.
type RefusedError struct {
	Status int
	Code   string
}

func (e *RefusedError) Error() string {
	code := e.Code
	if code == "" {
		code = "no error code"
	}
	return fmt.Sprintf("tether refused the request: HTTP %d (%s)", e.Status, code)
}

// Unauthorized reports a credential problem (401/403).
func (e *RefusedError) Unauthorized() bool { return e.Status == 401 || e.Status == 403 }

// Registrar is what enrollment needs from Tether. The deregister call is the
// one place the retire request shape lives.
type Registrar interface {
	// Ready reports, without any network call, whether Tether is configured:
	// ErrNotConfigured, ErrTokenMissing or ErrTokenInsecure.
	Ready() error
	// Register registers the external key idempotently and returns the URN.
	// created is true on the first call (201) and false on a repeat (200).
	Register(ctx context.Context, externalKey, displayName string) (urn string, created bool, err error)
	// Lookup finds the entry for an external key without creating one.
	Lookup(ctx context.Context, externalKey string) (urn string, found bool, err error)
	// Deregister soft-deletes (deprecates) the URN. An entry Tether no longer
	// has counts as done.
	Deregister(ctx context.Context, urn string) error
}

// Config is the environment-derived configuration, read per call.
type Config struct {
	Addr      string
	TokenFile string
}

// ConfigFromEnv reads the address and token file path now.
func ConfigFromEnv() Config {
	addr := strings.TrimSpace(os.Getenv(RegistryAddrEnv))
	if addr == "" {
		addr = strings.TrimSpace(os.Getenv(AddrEnv))
	}
	return Config{Addr: addr, TokenFile: strings.TrimSpace(os.Getenv(TokenFileEnv))}
}

// Client implements Registrar over HTTP. A nil Config func reads the
// environment on every call, so rotating the token file needs no restart.
type Client struct {
	// Config, when set, supplies the configuration; default ConfigFromEnv.
	Config func() Config
	// HTTP, when set, is used instead of the default client (tests).
	HTTP *http.Client
}

var _ Registrar = (*Client)(nil)

// ReadToken reads the registry token from path. It refuses a file with any
// group or world permission bit, and an empty file. The value is returned only
// to the caller that builds the Authorization header.
func ReadToken(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", ErrTokenMissing
	}
	fi, err := os.Stat(path)
	if err != nil {
		return "", ErrTokenMissing
	}
	if !fi.Mode().IsRegular() {
		return "", ErrTokenMissing
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return "", ErrTokenInsecure
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", ErrTokenMissing
	}
	tok := strings.TrimSpace(string(b))
	if tok == "" {
		return "", ErrTokenMissing
	}
	return tok, nil
}

// Ready implements Registrar.
func (c *Client) Ready() error {
	_, _, _, err := c.prepare()
	return err
}

func (c *Client) config() Config {
	if c.Config != nil {
		return c.Config()
	}
	return ConfigFromEnv()
}

// prepare resolves the base URL, HTTP client and token, in the order the typed
// errors are documented: address, then token.
func (c *Client) prepare() (base string, hc *http.Client, token string, err error) {
	cfg := c.config()
	if cfg.Addr == "" {
		return "", nil, "", ErrNotConfigured
	}
	token, err = ReadToken(cfg.TokenFile)
	if err != nil {
		return "", nil, "", err
	}
	base, hc, err = c.endpoint(cfg.Addr)
	return base, hc, token, err
}

func (c *Client) endpoint(addr string) (string, *http.Client, error) {
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		u, err := url.Parse(addr)
		if err != nil || u.Host == "" {
			return "", nil, ErrNotConfigured
		}
		hc := c.HTTP
		if hc == nil {
			hc = &http.Client{Timeout: defaultTimeout}
		}
		return strings.TrimRight(addr, "/"), hc, nil
	}
	// A unix socket: unix:///abs/path or a bare absolute path. Requests go over
	// the socket with Host "localhost". The path is checked at call time.
	sock := strings.TrimPrefix(addr, "unix://")
	if strings.Contains(addr, "://") && !strings.HasPrefix(addr, "unix://") {
		return "", nil, ErrAddrInvalid
	}
	if !filepath.IsAbs(sock) || filepath.Clean(sock) != sock {
		return "", nil, ErrAddrInvalid
	}
	fi, err := os.Stat(sock)
	if err != nil {
		// Tether not running (or wrong path): retryable, and the path is not secret.
		return "", nil, fmt.Errorf("%w: socket %s does not exist", ErrUnreachable, sock)
	}
	if fi.Mode()&os.ModeSocket == 0 {
		return "", nil, ErrAddrInvalid
	}
	hc := &http.Client{Timeout: defaultTimeout}
	if c.HTTP != nil {
		hc.Timeout = c.HTTP.Timeout
	}
	hc.Transport = &http.Transport{
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		}}
	return "http://localhost", hc, nil
}

// SplitKey splits an external key '<substrate>/<external_id>' at the FIRST
// slash, as Tether does.
func SplitKey(key string) (substrate, externalID string, err error) {
	substrate, externalID, ok := strings.Cut(key, "/")
	if !ok || substrate == "" || externalID == "" {
		return "", "", fmt.Errorf("external key %q must be <substrate>/<external_id>", key)
	}
	return substrate, externalID, nil
}

type profileBody struct {
	URN    string `json:"urn"`
	Status string `json:"status"`
}

func (c *Client) do(ctx context.Context, method, path string, body any) (int, []byte, error) {
	base, hc, token, err := c.prepare()
	if err != nil {
		return 0, nil, err
	}
	var rdr io.Reader
	if body != nil {
		b, merr := json.Marshal(body)
		if merr != nil {
			return 0, nil, merr
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, rdr)
	if err != nil {
		return 0, nil, fmt.Errorf("tether registry request: %w", ErrUnreachable)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := hc.Do(req)
	if err != nil {
		// The transport error can carry the URL; report the class, not the text.
		if ctx.Err() != nil {
			return 0, nil, fmt.Errorf("%w: %v", ErrUnreachable, ctx.Err())
		}
		return 0, nil, ErrUnreachable
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return 0, nil, ErrUnreachable
	}
	switch {
	case resp.StatusCode >= 500:
		return resp.StatusCode, data, fmt.Errorf("%w: HTTP %d", ErrUnreachable, resp.StatusCode)
	case resp.StatusCode >= 400:
		var env struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(data, &env)
		return resp.StatusCode, data, &RefusedError{Status: resp.StatusCode, Code: sanitizeCode(env.Error.Code)}
	}
	return resp.StatusCode, data, nil
}

// sanitizeCode keeps only a short identifier-shaped code.
func sanitizeCode(s string) string {
	if len(s) > 64 {
		return ""
	}
	for _, r := range s {
		if !(r == '_' || r == '-' || r == '.' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return ""
		}
	}
	return s
}

// Register implements Registrar: POST /registry/agents with substrate and
// external_id. 201 creates, 200 is a repeat returning the same URN, 400 is a
// malformed or conflicting key.
func (c *Client) Register(ctx context.Context, externalKey, displayName string) (string, bool, error) {
	substrate, externalID, err := SplitKey(externalKey)
	if err != nil {
		return "", false, err
	}
	status, data, err := c.do(ctx, http.MethodPost, "/registry/agents", map[string]string{
		"substrate": substrate, "external_id": externalID, "display_name": displayName})
	if err != nil {
		return "", false, err
	}
	var p profileBody
	if json.Unmarshal(data, &p) != nil || strings.TrimSpace(p.URN) == "" || (status != 200 && status != 201) {
		return "", false, ErrBadResponse
	}
	return p.URN, status == 201, nil
}

// Lookup implements Registrar: GET /registry/agents?substrate=&external_id=.
// A 404 is "not found", not an error.
func (c *Client) Lookup(ctx context.Context, externalKey string) (string, bool, error) {
	substrate, externalID, err := SplitKey(externalKey)
	if err != nil {
		return "", false, err
	}
	q := url.Values{"substrate": {substrate}, "external_id": {externalID}}
	status, data, err := c.do(ctx, http.MethodGet, "/registry/agents?"+q.Encode(), nil)
	var ref *RefusedError
	if errors.As(err, &ref) && ref.Status == http.StatusNotFound {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	var env struct {
		Agent profileBody `json:"agent"`
	}
	if status != 200 || json.Unmarshal(data, &env) != nil {
		return "", false, ErrBadResponse
	}
	if strings.TrimSpace(env.Agent.URN) == "" {
		return "", false, nil
	}
	return env.Agent.URN, true, nil
}

// Deregister implements Registrar. THE RETIRE CALL, in one place:
// DELETE /registry/agents/{urn} (URN path-escaped), which Tether answers 200
// with the profile at status "deprecated". Verified against Tether's source on
// main (internal/api/registry.go handleRegistryDeregister) and its MCP tool
// tether_registry_deregister; NOT exercised against a live Tether by Nanite.
// A 404 means the entry is already gone and counts as done.
func (c *Client) Deregister(ctx context.Context, urn string) error {
	if strings.TrimSpace(urn) == "" {
		return errors.New("deregister: URN is required")
	}
	_, _, err := c.do(ctx, http.MethodDelete, "/registry/agents/"+url.PathEscape(urn), nil)
	var ref *RefusedError
	if errors.As(err, &ref) && ref.Status == http.StatusNotFound {
		return nil
	}
	return err
}
