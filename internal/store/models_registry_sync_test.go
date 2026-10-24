package store

import (
	"context"
	"testing"
)

// TestSyncModelsFromRegistry_CurrentChatModelsReachTheTable pins the whole
// path CW-20260912-0107 was about: a model is offered in the chat launcher only
// if pkg/models.AllSeeded() names it AND SyncModelsFromRegistry materializes a
// row for it. models.dev enriches entries the registry already names and never
// adds one, so the Go file is the set and this test is what proves an addition
// to it actually lands.
//
// Checking the table rather than the Go slice is the point. A registry entry
// whose provider has no row fails the models.provider_id foreign key, so
// "it is in AllSeeded()" and "chat can offer it" are different claims.
func TestSyncModelsFromRegistry_CurrentChatModelsReachTheTable(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	// The boot order from cmd/nanite/main.go: Seed, then SeedProviders, then
	// the catalog sync. SeedProviders is what creates the provider rows the
	// models table references — calling only Seed leaves openai-001 absent and
	// the sync fails a foreign key rather than skipping, which is how this
	// test was first written and what it now documents.
	if err := s.Seed(ctx); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if err := s.SeedProviders(ctx); err != nil {
		t.Fatalf("SeedProviders: %v", err)
	}
	if _, err := s.SyncModelsFromRegistry(ctx); err != nil {
		t.Fatalf("SyncModelsFromRegistry: %v", err)
	}

	// One current model per provider, which is the minimum that makes the chat
	// launcher useful. Named explicitly rather than counted: a count would pass
	// while offering the wrong models, and the identifiers are the thing that
	// has to be right — a wrong one reaches a user as a provider error at the
	// first token.
	cases := []struct{ modelID, providerType string }{
		{"claude-opus-5", "anthropic"},
		{"claude-sonnet-5", "anthropic"},
		{"claude-fable-5-1", "anthropic"},
		{"gpt-6-astra", "openai"},
		{"gpt-5.6", "openai"},
		{"gpt-5.6-luna", "openai"},
	}
	for _, tc := range cases {
		t.Run(tc.modelID, func(t *testing.T) {
			var providerID string
			err := s.DB.QueryRowContext(ctx,
				`SELECT p.provider_type
				   FROM models m JOIN providers p ON p.id = m.provider_id
				  WHERE m.model_id = ?`, tc.modelID).Scan(&providerID)
			if err != nil {
				t.Fatalf("no models row for %q: %v", tc.modelID, err)
			}
			if providerID != tc.providerType {
				t.Errorf("%q is under provider %q, want %q — the launcher groups by "+
					"provider, so a row under the wrong one is not offered where a user looks",
					tc.modelID, providerID, tc.providerType)
			}
		})
	}
}

// Gateway aliases reach the models table under the right station provider with
// their limits and tool support, and their price is recorded as unknown, not 0.
func TestSyncModelsFromRegistry_StationAliases(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	if err := s.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.SeedProviders(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SyncModelsFromRegistry(ctx); err != nil {
		t.Fatal(err)
	}
	for modelID, want := range map[string]struct {
		provider string
		tools    bool
	}{
		"go-glm": {"station-openai", true}, "go-gpt": {"station-openai", true}, "openai-mini": {"station-openai", true},
		"gemini-flash": {"station-openai", true}, "openrouter-auto": {"station-openai", true},
		"hf-llama": {"station-openai", false}, "local-light": {"station-openai", false},
		"go-haiku": {"station-anthropic", true}, "anthropic-haiku": {"station-anthropic", true},
	} {
		var provider, pricing string
		var window, maxOut int
		var tools bool
		err := s.DB.QueryRowContext(ctx,
			`SELECT p.provider_type, m.context_window, m.max_output, m.supports_tools, m.pricing
			   FROM models m JOIN providers p ON p.id = m.provider_id WHERE m.model_id = ?`, modelID).
			Scan(&provider, &window, &maxOut, &tools, &pricing)
		if err != nil {
			t.Errorf("%s: no models row: %v", modelID, err)
			continue
		}
		if provider != want.provider || tools != want.tools || window <= 0 || maxOut <= 0 {
			t.Errorf("%s: provider %q tools %v window %d out %d", modelID, provider, tools, window, maxOut)
		}
		if pricing != `{"price_unknown":true}` {
			t.Errorf("%s: pricing = %s, want unknown", modelID, pricing)
		}
	}
}
