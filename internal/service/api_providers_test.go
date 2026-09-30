package service

import (
	"testing"

	"github.com/hollis-labs/nanite/internal/secrets"
)

func TestResolveAPIKey_KeychainBeforeEnvironment(t *testing.T) {
	keychain := map[string]string{}
	env := map[string]string{}
	secret := func(k string) string { return keychain[k] }
	getenv := func(k string) string { return env[k] }
	keyName := secrets.ProviderKeyName("anthropic-001")

	cases := []struct {
		name          string
		keychain, env string
		wantKey       string
		wantSource    string
	}{
		{"neither", "", "", "", ""},
		{"environment only", "", "env-key", "env-key", APIKeySourceEnvironment},
		{"keychain only", "kc-key", "", "kc-key", APIKeySourceKeychain},
		{"keychain wins over environment", "kc-key", "env-key", "kc-key", APIKeySourceKeychain},
		{"whitespace keychain falls through", "  ", "env-key", "env-key", APIKeySourceEnvironment},
		{"values are trimmed", " kc-key\n", "", "kc-key", APIKeySourceKeychain},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			keychain[keyName] = tc.keychain
			env["ANTHROPIC_API_KEY"] = tc.env
			key, source := resolveAPIKey(secret, getenv, "anthropic-001", "ANTHROPIC_API_KEY")
			if key != tc.wantKey || source != tc.wantSource {
				t.Fatalf("resolveAPIKey = (%q, %q), want (%q, %q)", key, source, tc.wantKey, tc.wantSource)
			}
		})
	}
}

func TestResolveAPIKey_NoEnvKeyNoEnvironmentFallback(t *testing.T) {
	getenv := func(string) string { return "should-not-be-read" }
	key, source := resolveAPIKey(func(string) string { return "" }, getenv, "x-001", "")
	if key != "" || source != "" {
		t.Fatalf("resolveAPIKey = (%q, %q), want empty", key, source)
	}
}

func TestAPIProviderSpecByID(t *testing.T) {
	for _, spec := range APIProviderSpecs() {
		got, ok := APIProviderSpecByID(spec.ProviderID)
		if !ok || got.Name != spec.Name {
			t.Fatalf("APIProviderSpecByID(%q) = %q, %v; want %q", spec.ProviderID, got.Name, ok, spec.Name)
		}
		if spec.NewProvider("k") == nil {
			t.Fatalf("%s: NewProvider returned nil", spec.Name)
		}
	}
	if _, ok := APIProviderSpecByID("gemini-api-001"); ok {
		t.Fatal("an adapter-less row resolved to a spec")
	}
}
