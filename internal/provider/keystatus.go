package provider

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/hollis-labs/libs/security/secrets"
)

// KeyStatus says whether a provider's key can be found and where from. It never holds the key.
type KeyStatus struct {
	Provider string `json:"provider"`
	Found    bool   `json:"found"`
	// Source is where the key came from: the key reference ("keychain://…", "file://…", "env://…")
	// or "env:<NAME>" for the NANITE_PROVIDER_KEY_ENV fallback; "none" for a provider that needs
	// no key; "" when not found.
	Source string `json:"source,omitempty"`
	// Tried are the references looked at, in order.
	Tried []string `json:"tried"`
	Error string   `json:"error,omitempty"`
}

// ResolveKey finds a built-in provider's key as a request would: the gateway key reference, then
// keychain://nanite/provider/<provider>, then (with NANITE_PROVIDER_KEY_ENV=1) ANTHROPIC_API_KEY or
// OPENAI_API_KEY. It returns the key and where it came from.
func ResolveKey(ctx context.Context, provider string, gateway Gateway, resolver *secrets.Resolver) (string, KeyStatus, error) {
	if resolver == nil {
		resolver = secrets.New()
	}
	st := KeyStatus{Provider: provider, Tried: []string{}}
	var refs []string
	if gateway.KeyRef != "" {
		refs = append(refs, gateway.KeyRef)
	}
	refs = append(refs, "keychain://nanite/provider/"+provider)
	var failures []error
	for _, ref := range refs {
		st.Tried = append(st.Tried, ref)
		resolved, err := resolver.Resolve(ctx, ref)
		if err == nil {
			st.Found, st.Source = true, ref
			return resolved.Secret.Reveal(), st, nil
		}
		failures = append(failures, err)
		if ctx.Err() != nil {
			return "", st, ctx.Err()
		}
	}
	if os.Getenv("NANITE_PROVIDER_KEY_ENV") == "1" {
		name := "ANTHROPIC_API_KEY"
		if provider == "openai" {
			name = "OPENAI_API_KEY"
		}
		st.Tried = append(st.Tried, "env:"+name)
		if value := os.Getenv(name); value != "" {
			st.Found, st.Source = true, "env:"+name
			return value, st, nil
		}
	}
	err := fmt.Errorf("resolve %s provider key: %w", provider, errors.Join(failures...))
	st.Error = err.Error()
	return "", st, err
}

// NamedKeyStatus is ResolveKey for a providers.yaml provider: its key_ref (else
// keychain://nanite/provider/<name>), then its key_env with NANITE_PROVIDER_KEY_ENV=1. A provider
// with auth none or warden needs no key.
func NamedKeyStatus(ctx context.Context, spec Spec, resolver *secrets.Resolver) KeyStatus {
	st := KeyStatus{Provider: spec.Name, Tried: []string{}}
	if spec.Auth == AuthNone || spec.Auth == AuthWarden {
		st.Found, st.Source = true, spec.Auth
		return st
	}
	if resolver == nil {
		resolver = secrets.New()
	}
	ref := spec.KeyRef
	if ref == "" {
		ref = "keychain://nanite/provider/" + spec.Name
	}
	st.Tried = append(st.Tried, ref)
	_, err := resolver.Resolve(ctx, ref)
	if err == nil {
		st.Found, st.Source = true, ref
		return st
	}
	if spec.KeyEnv != "" && os.Getenv("NANITE_PROVIDER_KEY_ENV") == "1" {
		st.Tried = append(st.Tried, "env:"+spec.KeyEnv)
		if os.Getenv(spec.KeyEnv) != "" {
			st.Found, st.Source = true, "env:"+spec.KeyEnv
			return st
		}
	}
	st.Error = err.Error()
	return st
}
