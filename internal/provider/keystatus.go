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

// Has reports whether a secret reference resolves, without the secret: for a status check that
// must not read it (a keychain read can prompt). checked is false when it could not tell
// without reading (a helper backend), and then found is the caller's best guess.
type Has func(ctx context.Context, ref string) (found, checked bool, err error)

// KeyStatusFor is ResolveKey's lookup without reading the key: the same references in the same
// order, each asked of has.
func KeyStatusFor(ctx context.Context, provider string, gateway Gateway, has Has) KeyStatus {
	st := KeyStatus{Provider: provider, Tried: []string{}}
	var refs []string
	if gateway.KeyRef != "" {
		refs = append(refs, gateway.KeyRef)
	}
	refs = append(refs, "keychain://nanite/provider/"+provider)
	name := "ANTHROPIC_API_KEY"
	if provider == "openai" {
		name = "OPENAI_API_KEY"
	}
	return walk(ctx, st, refs, name, has)
}

// NamedKeyStatus is KeyStatusFor for a providers.yaml provider: its key_ref (else
// keychain://nanite/provider/<name>), then its key_env with NANITE_PROVIDER_KEY_ENV=1. A provider
// with auth none or warden needs no key.
func NamedKeyStatus(ctx context.Context, spec Spec, has Has) KeyStatus {
	st := KeyStatus{Provider: spec.Name, Tried: []string{}}
	if spec.Auth == AuthNone || spec.Auth == AuthWarden {
		st.Found, st.Source = true, spec.Auth
		return st
	}
	ref := spec.KeyRef
	if ref == "" {
		ref = "keychain://nanite/provider/" + spec.Name
	}
	return walk(ctx, st, []string{ref}, spec.KeyEnv, has)
}

func walk(ctx context.Context, st KeyStatus, refs []string, envName string, has Has) KeyStatus {
	var failures []error
	for _, ref := range refs {
		st.Tried = append(st.Tried, ref)
		found, checked, err := has(ctx, ref)
		if found {
			st.Found, st.Source = true, ref
			if !checked {
				st.Error = "not checked: reading " + ref + " could prompt"
			}
			return st
		}
		if err != nil {
			failures = append(failures, err)
		}
	}
	if envName != "" && os.Getenv("NANITE_PROVIDER_KEY_ENV") == "1" {
		st.Tried = append(st.Tried, "env:"+envName)
		if os.Getenv(envName) != "" {
			st.Found, st.Source = true, "env:"+envName
			return st
		}
	}
	if err := errors.Join(failures...); err != nil {
		st.Error = err.Error()
	} else {
		st.Error = "no key found"
	}
	return st
}
