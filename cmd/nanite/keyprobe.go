package main

import (
	"context"

	libsecrets "github.com/hollis-labs/libs/security/secrets"
	"github.com/hollis-labs/nanite/internal/provider"
	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/secrets"
)

// keychainLookup is keychainHas; tests replace it (TestMain), so no test reaches a real keychain.
var keychainLookup = keychainHas

// keyHas checks a key reference for `nanite auth status` without reading anything that could
// prompt: a keychain entry by its attributes only (keychainHas), a file or env reference by
// resolving it, and a helper reference (op://, ...) not at all, reported as configured but not
// checked. exo runs auth status during inventory, so it must never raise a keychain dialog.
func keyHas(app *module.App) provider.Has {
	service := libsecrets.DefaultService
	if cfg, _ := app.Config["secrets"].(map[string]any); cfg != nil {
		if s, _ := cfg["service"].(string); s != "" {
			service = s
		}
	}
	resolver := secrets.Resolver(app)
	return func(ctx context.Context, ref string) (bool, bool, error) {
		r, err := libsecrets.ParseRef(ref)
		if err != nil {
			return false, true, err
		}
		switch r.Scheme {
		case "keychain", "keyring":
			return keychainLookup(ctx, service, r.Key())
		case "file", "env":
			_, err := resolver.Resolve(ctx, ref)
			return err == nil, true, err
		default:
			return true, false, nil
		}
	}
}
