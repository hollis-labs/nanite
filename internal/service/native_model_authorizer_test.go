package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hollis-labs/substrate/harness/adapters/provider"
)

// Nanite has no user-level or app-level default provider or model
// (CW-20261011-0031). The native authorizer accepts exactly the selection the
// agent's host settings or the launch request carry, and refuses a blank one
// naming what is missing.
func TestConfiguredNativeModel_NoDefaultFallback(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register("anthropic", stubLLMProvider{})
	authorize := configuredNativeModel(reg)
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
