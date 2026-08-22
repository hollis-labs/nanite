package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/store"
)

func TestBuildRepairConfig_ProviderResolution(t *testing.T) {
	type settings struct {
		utilityProvider string
		defaultProvider string
		defaultModel    string
	}
	tests := []struct {
		name              string
		envProvider       string
		utilityProvider   string
		settings          *settings
		registered        []string
		wantProvider      string
		wantSettingsStore bool
	}{
		{
			name:            "operator env overrides every lower level",
			envProvider:     "operator",
			utilityProvider: "caller-utility",
			settings: &settings{
				utilityProvider: "settings-utility",
				defaultProvider: "settings-default",
				defaultModel:    "settings-model",
			},
			registered:        []string{"operator", "caller-utility", "settings-utility", "settings-default"},
			wantProvider:      "operator",
			wantSettingsStore: true,
		},
		{
			name:            "caller utility overrides stored utility",
			utilityProvider: "caller-utility",
			settings: &settings{
				utilityProvider: "settings-utility",
				defaultProvider: "settings-default",
				defaultModel:    "settings-model",
			},
			registered:        []string{"caller-utility", "settings-utility", "settings-default"},
			wantProvider:      "caller-utility",
			wantSettingsStore: true,
		},
		{
			name: "stored utility overrides resolver default",
			settings: &settings{
				utilityProvider: "settings-utility",
				defaultProvider: "settings-default",
				defaultModel:    "settings-model",
			},
			registered:        []string{"settings-utility", "settings-default"},
			wantProvider:      "settings-utility",
			wantSettingsStore: true,
		},
		{
			name: "resolver uses user default",
			settings: &settings{
				defaultProvider: "settings-default",
				defaultModel:    "settings-model",
			},
			registered:        []string{"settings-default"},
			wantProvider:      "settings-default",
			wantSettingsStore: true,
		},
		{
			name:              "resolver uses seeded platform default",
			settings:          &settings{},
			registered:        []string{"anthropic"},
			wantProvider:      "anthropic",
			wantSettingsStore: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("NANITE_AUTO_REPAIR", "true")
			t.Setenv("NANITE_REPAIR_PROVIDER", tc.envProvider)
			t.Setenv("NANITE_REPAIR_MODEL", "")
			t.Setenv("NANITE_REPAIR_TIMEOUT_MS", "")

			var st *store.Store
			if tc.settings != nil {
				st = newRepairConfigTestStore(t)
				us, err := st.GetUserSettings(context.Background())
				if err != nil {
					t.Fatalf("GetUserSettings: %v", err)
				}
				us.UtilityProvider = tc.settings.utilityProvider
				us.DefaultProvider = tc.settings.defaultProvider
				us.DefaultModel = tc.settings.defaultModel
				if err := st.UpdateUserSettings(context.Background(), us); err != nil {
					t.Fatalf("UpdateUserSettings: %v", err)
				}
			}

			reg := provider.NewRegistry()
			registered := make(map[string]*repairStubProvider, len(tc.registered))
			for _, name := range tc.registered {
				p := &repairStubProvider{}
				registered[name] = p
				reg.Register(name, p)
			}

			rc := buildRepairConfig(reg, st, tc.utilityProvider)
			if rc == nil {
				t.Fatal("buildRepairConfig returned nil")
			}
			if rc.Provider != registered[tc.wantProvider] {
				t.Fatalf("provider = %T(%p), want registered %q provider %p", rc.Provider, rc.Provider, tc.wantProvider, registered[tc.wantProvider])
			}
			if got := rc.SettingsReader != nil; got != tc.wantSettingsStore {
				t.Fatalf("SettingsReader present = %v, want %v", got, tc.wantSettingsStore)
			}
		})
	}
}

func TestBuildRepairConfig_GatesAndMissingProvider(t *testing.T) {
	t.Run("nil registry", func(t *testing.T) {
		t.Setenv("NANITE_AUTO_REPAIR", "true")
		if got := buildRepairConfig(nil, nil, "utility"); got != nil {
			t.Fatalf("buildRepairConfig = %#v, want nil", got)
		}
	})

	t.Run("operator kill switch", func(t *testing.T) {
		t.Setenv("NANITE_AUTO_REPAIR", " OFF ")
		reg := provider.NewRegistry()
		reg.Register("utility", &repairStubProvider{})
		if got := buildRepairConfig(reg, nil, "utility"); got != nil {
			t.Fatalf("buildRepairConfig = %#v, want nil", got)
		}
	})

	t.Run("dry provider chain", func(t *testing.T) {
		t.Setenv("NANITE_AUTO_REPAIR", "true")
		t.Setenv("NANITE_REPAIR_PROVIDER", "")
		if got := buildRepairConfig(provider.NewRegistry(), nil, ""); got != nil {
			t.Fatalf("buildRepairConfig = %#v, want nil", got)
		}
	})

	t.Run("selected provider is not registered", func(t *testing.T) {
		t.Setenv("NANITE_AUTO_REPAIR", "true")
		t.Setenv("NANITE_REPAIR_PROVIDER", "missing")
		reg := provider.NewRegistry()
		reg.Register("lower-level", &repairStubProvider{})
		if got := buildRepairConfig(reg, nil, "lower-level"); got != nil {
			t.Fatalf("buildRepairConfig = %#v, want nil", got)
		}
	})
}

func TestBuildRepairConfig_ModelTimeoutAndDeterminism(t *testing.T) {
	tests := []struct {
		name        string
		model       string
		timeout     string
		wantTimeout time.Duration
	}{
		{name: "explicit model and timeout", model: "repair-model", timeout: "125", wantTimeout: 125 * time.Millisecond},
		{name: "empty values preserve downstream defaults"},
		{name: "invalid timeout preserves downstream default", timeout: "not-a-number"},
		{name: "zero timeout preserves downstream default", timeout: "0"},
		{name: "negative timeout preserves downstream default", timeout: "-10"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("NANITE_AUTO_REPAIR", "true")
			t.Setenv("NANITE_REPAIR_PROVIDER", "")
			t.Setenv("NANITE_REPAIR_MODEL", tc.model)
			t.Setenv("NANITE_REPAIR_TIMEOUT_MS", tc.timeout)
			reg := provider.NewRegistry()
			p := &repairStubProvider{}
			reg.Register("utility", p)

			first := buildRepairConfig(reg, nil, "utility")
			second := buildRepairConfig(reg, nil, "utility")
			if first == nil || second == nil {
				t.Fatalf("buildRepairConfig returned nil: first=%#v second=%#v", first, second)
			}
			for i, got := range []*RepairConfig{first, second} {
				if got.Provider != p || got.Model != tc.model || got.Timeout != tc.wantTimeout || got.SettingsReader != nil {
					t.Fatalf("call %d = {Provider:%T(%p) Model:%q Timeout:%s SettingsReader:%T}, want provider %p model %q timeout %s and nil reader",
						i+1, got.Provider, got.Provider, got.Model, got.Timeout, got.SettingsReader, p, tc.model, tc.wantTimeout)
				}
			}
		})
	}
}

func newRepairConfigTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.New(context.Background(), filepath.Join(t.TempDir(), "repair-config.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	if err := st.Seed(context.Background()); err != nil {
		t.Fatalf("Store.Seed: %v", err)
	}
	return st
}
