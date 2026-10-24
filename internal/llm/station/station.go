// Package station holds what the native API adapters need to talk to the
// Station LLM gateway: its provider settings (base URL, extra headers,
// per-provider flags), the per-request X-Station-Session id, and the gateway's
// typed error codes.
//
// The gateway owns budget and routing. Nothing here decides which alias a
// session may use or how much it may spend; it only carries the session id the
// gateway asks for and explains the refusals it sends back.
package station

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// SessionHeader is the request header the gateway attributes a call to.
const SessionHeader = "X-Station-Session"

// Settings keys in a station provider row's settings JSON.
const (
	SettingBaseURL              = "base_url"
	SettingHeaders              = "headers"
	SettingDisableAnthropicBeta = "disable_anthropic_beta"
	SettingDisableCacheControl  = "disable_cache_control"
	SettingAuth                 = "auth"
)

// Auth schemes for the Anthropic wire.
const (
	AuthDefault = ""       // the adapter's own scheme (x-api-key)
	AuthBearer  = "bearer" // Authorization: Bearer <key>
)

// Settings is the gateway part of a station provider row's settings JSON.
// key_ref, the one other setting a provider row carries, is read by
// internal/providerkeys and is not repeated here.
type Settings struct {
	// BaseURL is the gateway root. The Anthropic wire takes it without /v1
	// (https://host/api/llm-gateway); the OpenAI wire takes it with /v1.
	BaseURL string
	// Headers are sent on every request. They are stored in the row in clear,
	// so they must not carry a credential; Authorization and the key headers
	// are refused.
	Headers map[string]string
	// DisableAnthropicBeta stops the Anthropic adapter sending the
	// anthropic-beta header (and the interleaved-thinking request that needs it).
	DisableAnthropicBeta bool
	// DisableCacheControl stops the Anthropic adapter planting cache_control
	// markers.
	DisableCacheControl bool
	// Auth selects how the Anthropic adapter presents the key: "" for the
	// adapter default (x-api-key) or "bearer".
	Auth string
}

// rawSettings is the wire shape of Settings.
type rawSettings struct {
	BaseURL              string            `json:"base_url"`
	Headers              map[string]string `json:"headers"`
	DisableAnthropicBeta bool              `json:"disable_anthropic_beta"`
	DisableCacheControl  bool              `json:"disable_cache_control"`
	Auth                 string            `json:"auth"`
}

// forbiddenHeaders may not be set from settings: a credential does not belong
// in a settings blob, and the session header is set per request.
var forbiddenHeaders = map[string]bool{
	"authorization":       true,
	"x-api-key":           true,
	"proxy-authorization": true,
	"cookie":              true,
	"x-station-session":   true,
	"anthropic-version":   true,
	"host":                true,
	"content-length":      true,
	"content-type":        true,
}

// ParseSettings reads and validates the gateway settings from a provider row's
// settings JSON. A base_url is required.
func ParseSettings(settings string) (Settings, error) {
	var raw rawSettings
	if strings.TrimSpace(settings) != "" {
		if err := json.Unmarshal([]byte(settings), &raw); err != nil {
			return Settings{}, errors.New("station provider settings are not a valid JSON object")
		}
	}
	s := Settings{
		BaseURL:              strings.TrimSpace(raw.BaseURL),
		Headers:              raw.Headers,
		DisableAnthropicBeta: raw.DisableAnthropicBeta,
		DisableCacheControl:  raw.DisableCacheControl,
		Auth:                 strings.ToLower(strings.TrimSpace(raw.Auth)),
	}
	if err := s.Validate(); err != nil {
		return Settings{}, err
	}
	return s, nil
}

// ValidateSettings checks the gateway settings of a station provider row.
func ValidateSettings(settings string) error {
	_, err := ParseSettings(settings)
	return err
}

// Validate reports the first problem with s. It never echoes a header value.
func (s Settings) Validate() error {
	if s.BaseURL == "" {
		return fmt.Errorf("station provider settings need %q (the gateway URL)", SettingBaseURL)
	}
	u, err := url.Parse(s.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("station provider %q must be an http(s) URL", SettingBaseURL)
	}
	if u.User != nil {
		return fmt.Errorf("station provider %q must not carry credentials", SettingBaseURL)
	}
	for name := range s.Headers {
		if name == "" || strings.TrimSpace(name) != name || strings.ContainsAny(name, " \t:\r\n") {
			return fmt.Errorf("station provider header name %q is not valid", name)
		}
		if forbiddenHeaders[strings.ToLower(name)] {
			return fmt.Errorf("station provider header %q cannot be set in settings", name)
		}
	}
	for name, v := range s.Headers {
		if strings.ContainsAny(v, "\r\n") {
			return fmt.Errorf("station provider header %q has an invalid value", name)
		}
	}
	switch s.Auth {
	case AuthDefault, AuthBearer:
	default:
		return fmt.Errorf("station provider %q must be empty or %q", SettingAuth, AuthBearer)
	}
	return nil
}

// sessionIDRe is the gateway's session id grammar: 1-128 characters from
// [A-Za-z0-9._:-], starting alphanumeric.
var sessionIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

// ValidSessionID reports whether id is acceptable to the gateway as is.
func ValidSessionID(id string) bool { return sessionIDRe.MatchString(id) }

// NormalizeSessionID maps a Nanite session id onto the gateway grammar: other
// characters become '-', leading non-alphanumerics are trimmed and the result
// is cut to 128. It returns "" when nothing usable is left. The mapping is
// deterministic, so every turn and retry of one session sends the same value.
func NormalizeSessionID(id string) string {
	if ValidSessionID(id) {
		return id
	}
	b := make([]byte, 0, len(id))
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '.', c == '_', c == ':', c == '-':
			b = append(b, c)
		default:
			b = append(b, '-')
		}
	}
	out := strings.TrimLeft(string(b), "._:-")
	if len(out) > 128 {
		out = out[:128]
	}
	return out
}

type sessionKey struct{}

// WithSessionID returns ctx carrying the Nanite session id the adapters send
// as X-Station-Session. ChatRequest has no field for it, so it travels in the
// context like the reasoning config. An id that normalizes to nothing leaves
// ctx unchanged.
func WithSessionID(ctx context.Context, id string) context.Context {
	n := NormalizeSessionID(id)
	if n == "" {
		return ctx
	}
	return context.WithValue(ctx, sessionKey{}, n)
}

// SessionID returns the session id carried by ctx.
func SessionID(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(sessionKey{}).(string)
	return id, ok && id != ""
}

// Typed gateway refusals. Each carries the remedy; the original error text is
// appended by Classify.
var (
	ErrSessionRequired   = errors.New("station gateway: the request carried no session id (X-Station-Session); the call did not come from a Nanite session")
	ErrInvalidSession    = errors.New("station gateway: the session id was rejected (X-Station-Session must be 1-128 characters of [A-Za-z0-9._:-], starting alphanumeric)")
	ErrModelNotAvailable = errors.New("station gateway: model not available on this wire; this alias is served on a different endpoint (wrong wire for this alias: chat/completions, responses and messages are separate aliases)")
)

// Classify maps a gateway error onto a typed refusal. code is the error code
// the SDK parsed (may be empty); body is the raw response JSON. It returns nil
// when the error is not one of the gateway's own, so callers fall through to
// their normal translation.
func Classify(status int, code, body string, cause error) error {
	if status != 0 && status < http.StatusBadRequest {
		return nil
	}
	hay := strings.ToLower(code + " " + body)
	var sentinel error
	switch {
	case strings.Contains(hay, "session_required"):
		sentinel = ErrSessionRequired
	case strings.Contains(hay, "invalid_session"):
		sentinel = ErrInvalidSession
	case strings.Contains(hay, "model_not_available"):
		sentinel = ErrModelNotAvailable
	default:
		return nil
	}
	if cause == nil {
		return sentinel
	}
	return fmt.Errorf("%w: %s", sentinel, cause.Error())
}
