package agent

import (
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// TestEffectiveProvider_Precedence pins the c197 regression
// (CW-20260514-0053). The helper's contract:
//
//	opts.Provider WHEN non-empty  →  opts.Provider
//	profile.DefaultProvider only  →  profile.DefaultProvider
//	both set, opts wins           →  opts.Provider
//	both empty / nil profile      →  ""
//
// The function is small but every dispatch site downstream
// (bootdirLayoutFor, deps.ProviderAdapter, RuntimeRow.Provider,
// Session.Provider, runtimeConfigForAdapter) depends on this contract
// silently, so a future refactor that flips precedence would have
// downstream consequences this test localizes.
func TestEffectiveProvider_Precedence(t *testing.T) {
	tests := []struct {
		name    string
		opts    Options
		profile *store.AgentProfile
		want    string
	}{
		{
			name:    "opts.Provider wins over profile.DefaultProvider",
			opts:    Options{Provider: "claude"},
			profile: &store.AgentProfile{DefaultProvider: "anthropic"},
			want:    "claude",
		},
		{
			name:    "empty opts falls back to profile",
			opts:    Options{},
			profile: &store.AgentProfile{DefaultProvider: "anthropic"},
			want:    "anthropic",
		},
		{
			name:    "c197 reproducer: spec.Provider via opts when profile is empty",
			opts:    Options{Provider: "claude"},
			profile: &store.AgentProfile{DefaultProvider: ""},
			want:    "claude",
		},
		{
			name:    "both empty stays empty",
			opts:    Options{},
			profile: &store.AgentProfile{DefaultProvider: ""},
			want:    "",
		},
		{
			name:    "nil profile + opts override → opts",
			opts:    Options{Provider: "codex"},
			profile: nil,
			want:    "codex",
		},
		{
			name:    "nil profile + empty opts → empty",
			opts:    Options{},
			profile: nil,
			want:    "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := effectiveProvider(tt.opts, tt.profile); got != tt.want {
				t.Errorf("effectiveProvider = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestComposeBootdirParams_OptionsProviderRoutesToClaude is the
// c197 end-to-end pin. With Options.Provider="claude" and a profile
// carrying an empty DefaultProvider (the file-default shape), the
// dispatch must land on claudeLayout — not unsupportedLayout{name:""}
// which is what shipped before this fix.
func TestComposeBootdirParams_OptionsProviderRoutesToClaude(t *testing.T) {
	profile := &store.AgentProfile{DefaultProvider: ""} // file-default shape
	opts := Options{Provider: "claude", SessionID: "sess-c197"}

	layout, _ := composeBootdirParams(nil, opts, profile, "sess-c197")
	if _, ok := layout.(claudeLayout); !ok {
		t.Fatalf("composeBootdirParams returned %T, want claudeLayout (c197 regression — Options.Provider must override empty profile.DefaultProvider)", layout)
	}
}

// TestComposeBootdirParams_LegacyProfileProviderStillWorks confirms
// the no-regression case: when Options.Provider is empty, the legacy
// profile.DefaultProvider path keeps dispatching as it did before.
func TestComposeBootdirParams_LegacyProfileProviderStillWorks(t *testing.T) {
	profile := &store.AgentProfile{DefaultProvider: "codex"}
	opts := Options{SessionID: "sess-legacy"}

	layout, _ := composeBootdirParams(nil, opts, profile, "sess-legacy")
	if _, ok := layout.(codexLayout); !ok {
		t.Fatalf("composeBootdirParams returned %T, want codexLayout (legacy profile-driven dispatch must keep working)", layout)
	}
}
