package provider

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/hollis-labs/libs/llm/core/llmcontracts"
	"github.com/hollis-labs/libs/security/redact"
	"github.com/hollis-labs/libs/security/secrets"
	"github.com/hollis-labs/nanite/internal/provider/anthropic"
	"github.com/hollis-labs/nanite/internal/provider/openai"
	"github.com/hollis-labs/nanite/internal/provider/station"
)

// Wires a named provider can speak.
const (
	// WireAnthropic is the Anthropic Messages API.
	WireAnthropic = "anthropic"
	// WireOpenAI is the OpenAI API, choosing Chat Completions or Responses
	// per request, as the built-in openai provider does.
	WireOpenAI = "openai"
	// WireOpenAIChat is Chat Completions only: any OpenAI-compatible server.
	WireOpenAIChat = "openai-chat"
	// WireOpenAIResponses is the Responses API only.
	WireOpenAIResponses = "openai-responses"
)

// Auth schemes for a named provider.
const (
	// AuthDefault is the wire's own scheme: x-api-key for Anthropic, a
	// bearer token for OpenAI.
	AuthDefault = ""
	// AuthBearer sends the key as a bearer token on the Anthropic wire.
	AuthBearer = "bearer"
	// AuthNone sends no real key, for a local server without auth.
	AuthNone = "none"
	// AuthWarden sends a short-lived Warden token for Audience and Scopes
	// as a bearer token on every request, minted from the agent's parent
	// credential (identity.yaml).
	AuthWarden = "warden"
)

// Built-in provider names. They are configured by the host's Gateway and
// cannot be redefined.
const (
	BuiltinAnthropic = "anthropic"
	BuiltinOpenAI    = "openai"
)

// Spec describes a named provider: an endpoint, the wire it speaks and how
// it is authenticated. A Spec carries a reference to its key, never the key.
type Spec struct {
	Name string `yaml:"-" json:"name"`
	// Wire is one of the Wire constants.
	Wire string `yaml:"wire" json:"wire"`
	// BaseURL is the endpoint root: without /v1 for the Anthropic wire, with
	// /v1 for the OpenAI wires (http://127.0.0.1:8080/v1).
	BaseURL string `yaml:"base_url" json:"base_url"`
	// KeyRef is a secrets reference (keychain://..., env://..., file://...).
	// Empty uses keychain://nanite/provider/<name>.
	KeyRef string `yaml:"key_ref,omitempty" json:"key_ref,omitempty"`
	// KeyEnv names an environment variable holding the key. It is read only
	// when NANITE_PROVIDER_KEY_ENV=1, like the built-in providers' keys.
	KeyEnv string `yaml:"key_env,omitempty" json:"key_env,omitempty"`
	// Auth is one of the Auth constants.
	Auth string `yaml:"auth,omitempty" json:"auth,omitempty"`
	// Headers are sent on every request. Credentials do not belong here.
	Headers map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
	// Audience and Scopes are the Warden token's audience (the gateway's app
	// name, such as llm-gateway) and scopes, with auth warden.
	Audience string   `yaml:"audience,omitempty" json:"audience,omitempty"`
	Scopes   []string `yaml:"scopes,omitempty" json:"scopes,omitempty"`
	// DisableAnthropicBeta and DisableCacheControl turn off the Anthropic
	// wire's beta header and cache_control markers, for a gateway or
	// compatible server that rejects them.
	DisableAnthropicBeta bool `yaml:"disable_anthropic_beta,omitempty" json:"disable_anthropic_beta,omitempty"`
	DisableCacheControl  bool `yaml:"disable_cache_control,omitempty" json:"disable_cache_control,omitempty"`
}

var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// Validate reports the first problem with s. It never echoes a header value.
func (s Spec) Validate() error {
	if !namePattern.MatchString(s.Name) {
		return fmt.Errorf("provider name %q must be lowercase letters, digits, - or _", s.Name)
	}
	if s.Name == BuiltinAnthropic || s.Name == BuiltinOpenAI {
		return fmt.Errorf("provider name %q is built in and cannot be redefined", s.Name)
	}
	switch s.Wire {
	case WireAnthropic, WireOpenAI, WireOpenAIChat, WireOpenAIResponses:
	default:
		return fmt.Errorf("provider %s: wire %q must be one of anthropic, openai, openai-chat, openai-responses", s.Name, s.Wire)
	}
	switch s.Auth {
	case AuthWarden:
		if s.KeyRef != "" || s.KeyEnv != "" {
			return fmt.Errorf("provider %s: auth warden takes no key_ref or key_env", s.Name)
		}
		if err := ValidateAudience(s.Audience, s.Scopes); err != nil {
			return fmt.Errorf("provider %s: %w", s.Name, err)
		}
	case AuthDefault, AuthNone:
	case AuthBearer:
		if s.Wire != WireAnthropic {
			return fmt.Errorf("provider %s: auth bearer applies to the anthropic wire; the openai wires always use a bearer token", s.Name)
		}
	default:
		return fmt.Errorf("provider %s: auth %q must be empty, bearer, none or warden", s.Name, s.Auth)
	}
	if s.Auth != AuthWarden && (s.Audience != "" || len(s.Scopes) > 0) {
		return fmt.Errorf("provider %s: audience and scopes apply to auth warden", s.Name)
	}
	if s.Auth == AuthNone && (s.KeyRef != "" || s.KeyEnv != "") {
		return fmt.Errorf("provider %s: auth none takes no key_ref or key_env", s.Name)
	}
	if u, err := url.Parse(s.BaseURL); err != nil || u.Host == "" {
		return fmt.Errorf("provider %s: base_url must be an http(s) URL", s.Name)
	}
	if err := s.settings().Validate(); err != nil {
		return fmt.Errorf("provider %s: %w", s.Name, err)
	}
	// A credential belongs behind key_ref, where it is resolved and redacted,
	// never in a header value under some other name.
	for name := range s.Headers {
		lower := strings.ToLower(name)
		for _, word := range []string{"key", "token", "secret", "auth", "password", "credential"} {
			if strings.Contains(lower, word) {
				return fmt.Errorf("provider %s: header %q looks like a credential; use key_ref", s.Name, name)
			}
		}
	}
	return nil
}

func (s Spec) settings() station.Settings {
	auth := station.AuthDefault
	if s.Auth == AuthBearer {
		auth = station.AuthBearer
	}
	return station.Settings{BaseURL: s.BaseURL, Headers: maps.Clone(s.Headers), Auth: auth,
		DisableAnthropicBeta: s.DisableAnthropicBeta, DisableCacheControl: s.DisableCacheControl}
}

var tokenPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$`)

// ValidateAudience checks a Warden audience and scope set.
func ValidateAudience(audience string, scopes []string) error {
	if !tokenPattern.MatchString(audience) {
		return fmt.Errorf("audience %q must be an app name (letters, digits, . _ : / -)", audience)
	}
	for _, scope := range scopes {
		if !tokenPattern.MatchString(scope) {
			return fmt.Errorf("scope %q must be letters, digits, . _ : / -", scope)
		}
	}
	return nil
}

// keylessPlaceholder is the key sent to a server configured with auth none.
// The SDKs require a non-empty key; such servers ignore it.
const keylessPlaceholder = "nanite-no-key"

// NewNamed builds the adapter for a named provider. cfg supplies the secrets
// resolver, redaction scope and panic guard; its Provider and Gateway are
// ignored.
func NewNamed(spec Spec, cfg Config) (llmcontracts.Provider, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	if cfg.Redactor == nil {
		cfg.Redactor = redact.NewScope()
	}
	resolver := cfg.Secrets
	if resolver == nil {
		resolver = secrets.New()
	}
	load := func(ctx context.Context) (string, error) {
		ref := spec.KeyRef
		if ref == "" {
			ref = "keychain://nanite/provider/" + spec.Name
		}
		resolved, err := resolver.Resolve(ctx, ref)
		if err == nil {
			value := resolved.Secret.Reveal()
			cfg.Redactor.Add("provider key", value)
			return value, nil
		}
		if spec.KeyEnv != "" && os.Getenv("NANITE_PROVIDER_KEY_ENV") == "1" {
			if value := os.Getenv(spec.KeyEnv); value != "" {
				cfg.Redactor.Add(spec.KeyEnv, value)
				return value, nil
			}
		}
		return "", fmt.Errorf("resolve %s provider key: %w", spec.Name, errors.Join(err))
	}
	settings := spec.settings()
	keyless := spec.Auth == AuthNone
	if spec.Auth == AuthWarden {
		if cfg.Warden == nil {
			return nil, fmt.Errorf("provider %s: auth warden needs identity.yaml with a warden section", spec.Name)
		}
		settings.Bearer = cfg.Warden.Bearer(spec.Audience, spec.Scopes)
		keyless = true
	}
	switch spec.Wire {
	case WireAnthropic:
		client := anthropic.New()
		client.SetPanicGuard(cfg.Panics)
		client.SetGateway(settings)
		if keyless {
			client.SetAPIKey(keylessPlaceholder)
		} else {
			client.SetKeyFunc(load)
		}
		return client, nil
	default:
		client := openai.New(keylessPlaceholder, nil)
		client.SetPanicGuard(cfg.Panics)
		client.SetGateway(settings)
		switch spec.Wire {
		case WireOpenAIChat:
			client.SetAPI(openai.APIChat)
		case WireOpenAIResponses:
			client.SetAPI(openai.APIResponses)
		}
		if !keyless {
			client.SetKeyFunc(load)
		}
		return client, nil
	}
}
