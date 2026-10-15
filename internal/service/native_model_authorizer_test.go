package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	secrets "github.com/hollis-labs/go-secrets"
	"github.com/hollis-labs/go-secrets/secretstest"
	"github.com/hollis-labs/nanite/internal/providerkeys"
	"github.com/hollis-labs/substrate/harness/adapters/provider"
)

// Nanite has no user-level or app-level default provider or model
// (CW-20261011-0031). The native authorizer accepts exactly the selection the
// agent's host settings or the launch request carry, and refuses a blank one
// naming what is missing.
func TestConfiguredNativeModel_NoDefaultFallback(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register("anthropic", stubLLMProvider{})
	authorize := configuredNativeModel(reg, nil)
	ref := DefinitionRef{}

	t.Run("accepts the requested selection as given", func(t *testing.T) {
		got, err := authorize.AuthorizeModel(context.Background(), ref, &ModelSelection{Provider: "anthropic", Model: "claude-opus-5-5"})
		if err != nil {
			t.Fatalf("AuthorizeModel: %v", err)
		}
		if got.Provider != "anthropic" || got.Model != "claude-opus-5-5" {
			t.Fatalf("selection = %+v, want the requested one unchanged", got)
		}
	})

	for name, sel := range map[string]*ModelSelection{
		"nil selection":    nil,
		"blank provider":   {Model: "claude-opus-5-5"},
		"blank model":      {Provider: "anthropic"},
		"whitespace model": {Provider: "anthropic", Model: "  "},
	} {
		t.Run("refuses "+name, func(t *testing.T) {
			_, err := authorize.AuthorizeModel(context.Background(), ref, sel)
			if !errors.Is(err, ErrUnsupportedModel) {
				t.Fatalf("err = %v, want ErrUnsupportedModel", err)
			}
			if !strings.Contains(err.Error(), "select a provider and model") {
				t.Fatalf("err = %q, want it to say what is missing", err)
			}
		})
	}

	t.Run("refuses an unregistered provider", func(t *testing.T) {
		_, err := authorize.AuthorizeModel(context.Background(), ref, &ModelSelection{Provider: "nowhere", Model: "m"})
		if !errors.Is(err, ErrUnsupportedModel) {
			t.Fatalf("err = %v, want ErrUnsupportedModel", err)
		}
	})
}

// An unregistered API provider is refused with the reason its key could not be
// used, typed, instead of the bare "not a registered native provider".
func TestConfiguredNativeModel_RefusalNamesTheKeyProblem(t *testing.T) {
	const leak = "sk-ant-api03-MUST-NOT-APPEAR-0123456789"
	cases := []struct {
		name  string
		setup func(*secretstest.FakeStore)
		want  []string
	}{
		{"no key saved", func(*secretstest.FakeStore) {}, []string{"secret_not_found", "provider set-key anthropic-001"}},
		{"keychain denied", func(fs *secretstest.FakeStore) { fs.Fail = &secrets.Error{Kind: secrets.KindDenied} }, []string{"secret_denied"}},
		{"keychain unavailable", func(fs *secretstest.FakeStore) { fs.Fail = &secrets.Error{Kind: secrets.KindUnavailable} }, []string{"secret_unavailable"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			secretstest.Install(t)
			fs := &secretstest.FakeStore{}
			tc.setup(fs)
			keys := providerkeys.New(providerkeys.Options{Store: fs, EnvNames: ProviderKeyEnvName,
				LookupEnv: func(string) (string, bool) { return leak, true }})
			authorize := configuredNativeModel(provider.NewRegistry(), keys)

			_, err := authorize.AuthorizeModel(context.Background(), DefinitionRef{}, &ModelSelection{Provider: "anthropic", Model: "m"})
			if !errors.Is(err, ErrUnsupportedModel) {
				t.Fatalf("err = %v, want ErrUnsupportedModel", err)
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Fatalf("err = %q, want it to contain %q", err, w)
				}
			}
			secretstest.AssertNoLeak(t, leak, err)
		})
	}
}
