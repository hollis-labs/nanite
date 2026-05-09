package service

import (
	"context"
	"testing"
)

func testDeps(secrets map[string]string, env map[string]string) EmbedderSelectDeps {
	return EmbedderSelectDeps{
		LookupSecret: func(id string) string { return secrets[id] },
		Getenv:       func(k string) string { return env[k] },
	}
}

func TestSelectEmbedder_DisabledModes(t *testing.T) {
	cases := []struct {
		name     string
		settings EmbedderSettings
	}{
		{"empty mode", EmbedderSettings{}},
		{"disabled mode", EmbedderSettings{Mode: "disabled", Provider: "openai"}},
		{"empty provider", EmbedderSettings{Mode: "explicit"}},
		{"unsupported provider", EmbedderSettings{Mode: "explicit", Provider: "anthropic"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, _, status := SelectEmbedder(context.Background(), tc.settings, testDeps(nil, nil))
			if e != nil {
				t.Errorf("expected nil embedder, got %T", e)
			}
			if status != EmbeddingStatusDisabled {
				t.Errorf("status: got %q, want disabled", status)
			}
		})
	}
}

func TestSelectEmbedder_CredentialProviders(t *testing.T) {
	type caseT struct {
		provider     string
		secretKey    string
		envKey       string
		defaultModel string
	}
	cases := []caseT{
		{"openai", "openai-001", "OPENAI_API_KEY", "text-embedding-3-large"},
	}

	for _, tc := range cases {
		t.Run(tc.provider+"_missing_credentials", func(t *testing.T) {
			e, _, status := SelectEmbedder(context.Background(),
				EmbedderSettings{Mode: "explicit", Provider: tc.provider, Model: tc.defaultModel},
				testDeps(nil, nil))
			if e != nil {
				t.Errorf("expected nil embedder when credentials missing")
			}
			if status != EmbeddingStatusMissingCredentials {
				t.Errorf("status: got %q, want missing_credentials", status)
			}
		})

		t.Run(tc.provider+"_with_keychain_key", func(t *testing.T) {
			e, model, status := SelectEmbedder(context.Background(),
				EmbedderSettings{Mode: "explicit", Provider: tc.provider, Model: tc.defaultModel},
				testDeps(map[string]string{tc.secretKey: "sk-test"}, nil))
			if e == nil {
				t.Errorf("expected embedder, got nil")
			}
			if status != EmbeddingStatusActive {
				t.Errorf("status: got %q, want active", status)
			}
			if model != tc.defaultModel {
				t.Errorf("model: got %q, want %q", model, tc.defaultModel)
			}
		})

		t.Run(tc.provider+"_with_env_fallback", func(t *testing.T) {
			e, _, status := SelectEmbedder(context.Background(),
				EmbedderSettings{Mode: "explicit", Provider: tc.provider, Model: tc.defaultModel},
				testDeps(nil, map[string]string{tc.envKey: "sk-test"}))
			if e == nil {
				t.Errorf("expected embedder via env fallback, got nil")
			}
			if status != EmbeddingStatusActive {
				t.Errorf("status: got %q, want active", status)
			}
		})
	}
}

func TestSelectEmbedder_DefaultsModelWhenBlank(t *testing.T) {
	cases := map[string]string{
		"openai": "text-embedding-3-large",
	}
	for prov, want := range cases {
		t.Run(prov, func(t *testing.T) {
			env := map[string]string{
				"OPENAI_API_KEY": "x",
			}
			_, model, status := SelectEmbedder(context.Background(),
				EmbedderSettings{Mode: "explicit", Provider: prov}, // empty Model
				testDeps(nil, env))
			if status != EmbeddingStatusActive {
				t.Fatalf("status: got %q, want active", status)
			}
			if model != want {
				t.Errorf("model default: got %q, want %q", model, want)
			}
		})
	}
}

func TestIsSupportedEmbeddingProvider(t *testing.T) {
	for _, p := range SupportedEmbeddingProviders {
		if !IsSupportedEmbeddingProvider(p) {
			t.Errorf("expected %q to be supported", p)
		}
	}
	if IsSupportedEmbeddingProvider("anthropic") {
		t.Errorf("anthropic should not be supported (chat-only)")
	}
	if IsSupportedEmbeddingProvider("") {
		t.Errorf("empty should not be supported")
	}
}
