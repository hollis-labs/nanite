package service

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

// fakeUserSettingsStore serves one settings row, or getErr, and records the
// row it was last asked to write.
type fakeUserSettingsStore struct {
	us      *store.UserSettings
	getErr  error
	gets    int
	written *store.UserSettings
}

func (f *fakeUserSettingsStore) WithAdminPreferencesTransaction(context.Context, func(*store.PreferencesTransaction) error) error {
	return errors.New("unused transaction")
}

func (f *fakeUserSettingsStore) GetUserSettings(context.Context) (*store.UserSettings, error) {
	f.gets++
	if f.getErr != nil {
		return nil, f.getErr
	}
	cp := *f.us
	return &cp, nil
}

func (f *fakeUserSettingsStore) UpdateUserSettings(_ context.Context, us *store.UserSettings) error {
	f.written = us
	return nil
}

func TestUserSettingsDevModeEnabled(t *testing.T) {
	loadErr := errors.New("db down")
	for _, tc := range []struct {
		name    string
		env     string
		devMode bool
		getErr  error
		want    bool
	}{
		{name: "env on, setting off", env: "yes", want: true},
		{name: "env on, load error", env: " TRUE ", getErr: loadErr, want: true},
		{name: "env off, setting on", devMode: true, want: true},
		{name: "env off, setting off", want: false},
		{name: "env off, load error", getErr: loadErr, want: false},
		{name: "env not truthy, setting off", env: "0", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("NANITE_DEVMODE", tc.env)
			svc := NewUserSettingsService(&fakeUserSettingsStore{
				us:     &store.UserSettings{DeveloperMode: tc.devMode},
				getErr: tc.getErr,
			})
			if got := svc.DevModeEnabled(context.Background()); got != tc.want {
				t.Fatalf("DevModeEnabled = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestUserSettingsUpdateToolLoadPreferences(t *testing.T) {
	ctx := context.Background()

	t.Run("invalid value rejected before the row is read", func(t *testing.T) {
		f := &fakeUserSettingsStore{getErr: errors.New("db down")}
		_, err := NewUserSettingsService(f).UpdateToolLoadPreferences(ctx, map[string]string{"t": "sometimes"})
		if !IsSettingsValidation(err) {
			t.Fatalf("err = %v, want a *SettingsValidationError", err)
		}
		want := `invalid load_type for tool t: must be "auto", "opt-in", or "" (remove)`
		if err.Error() != want {
			t.Fatalf("message = %q, want %q", err.Error(), want)
		}
		if f.gets != 0 {
			t.Fatalf("settings read %d times before validation failed", f.gets)
		}
	})

	t.Run("merge, and empty removes", func(t *testing.T) {
		f := &fakeUserSettingsStore{us: &store.UserSettings{
			ToolLoadPreferences: map[string]string{"keep": "auto", "drop": "opt-in"},
		}}
		got, err := NewUserSettingsService(f).UpdateToolLoadPreferences(ctx, map[string]string{"drop": "", "add": "opt-in"})
		if err != nil {
			t.Fatalf("UpdateToolLoadPreferences: %v", err)
		}
		want := map[string]string{"keep": "auto", "add": "opt-in"}
		if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(f.written.ToolLoadPreferences, want) {
			t.Fatalf("got %v, written %v, want %v", got, f.written.ToolLoadPreferences, want)
		}
	})

	t.Run("load error returned unwrapped", func(t *testing.T) {
		loadErr := errors.New("db down")
		_, err := NewUserSettingsService(&fakeUserSettingsStore{getErr: loadErr}).UpdateToolLoadPreferences(ctx, map[string]string{"t": "auto"})
		if !errors.Is(err, loadErr) || err.Error() != loadErr.Error() {
			t.Fatalf("err = %v, want the store error itself", err)
		}
	})
}

func TestUserSettingsFieldValidators(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		msg  string
	}{
		{"stream ok", ValidateToolStreamBehavior("persist"), ""},
		{"stream bad", ValidateToolStreamBehavior(""), "tool_stream_behavior must be one of: streaming, persist, hidden"},
		{"retention ok", ValidateToolDrawerRetention(-1), ""},
		{"retention bad", ValidateToolDrawerRetention(10), "tool_drawer_retention must be one of: -1, 5, 15, 30, 60"},
		{"provider unset", ValidateEmbeddingProvider(""), ""},
		{"provider ok", ValidateEmbeddingProvider("openai"), ""},
		{"provider bad", ValidateEmbeddingProvider("anthropic"), "embedding_provider must be one of: openai"},
		{"mode unset", ValidateEmbeddingMode(""), ""},
		{"mode bad", ValidateEmbeddingMode("on"), "embedding_mode must be 'disabled' or 'explicit'"},
	} {
		got := ""
		if tc.err != nil {
			got = tc.err.Error()
		}
		if got != tc.msg {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.msg)
		}
	}
}

func TestAgentCapabilitiesListAlwaysAllowedToolNames(t *testing.T) {
	ctx := context.Background()
	st, err := storetest.New(t, ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })

	for _, row := range []struct{ name, status string }{
		{"zz_always_available", "available"},
		{"zz_always_unavailable", "unavailable"},
		{"zz_not_always", "available"},
	} {
		if _, uerr := st.UpsertKnownTool(ctx, row.name, "builtin", row.status, ""); uerr != nil {
			t.Fatalf("UpsertKnownTool %s: %v", row.name, uerr)
		}
	}
	if _, err = st.DB.ExecContext(ctx, `UPDATE known_tools SET always_included = TRUE WHERE name IN ('zz_always_available', 'zz_always_unavailable')`); err != nil {
		t.Fatalf("flag always_included: %v", err)
	}

	names, err := NewAgentCapabilitiesService(st).ListAlwaysAllowedToolNames(ctx)
	if err != nil {
		t.Fatalf("ListAlwaysAllowedToolNames: %v", err)
	}
	sort.Strings(names)
	has := func(n string) bool {
		i := sort.SearchStrings(names, n)
		return i < len(names) && names[i] == n
	}
	if !has("zz_always_available") {
		t.Errorf("available always-included tool missing: %v", names)
	}
	if has("zz_always_unavailable") {
		t.Errorf("unavailable always-included tool listed: %v", names)
	}
	if has("zz_not_always") {
		t.Errorf("tool without always_included listed: %v", names)
	}
}

func (f *fakeUserSettingsStore) GetAdminPreferences(context.Context) (*store.AdminPreferences, error) {
	return nil, errors.New("settings unavailable")
}

func TestUserSettingsFreshInstallDeveloperModeOffAndOptInSurvivesReopen(t *testing.T) {
	t.Setenv("NANITE_DEVMODE", "")
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "fresh.db")
	st, err := store.New(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(ctx) })
	if seedErr := st.Seed(ctx); seedErr != nil {
		t.Fatal(seedErr)
	}
	settings, err := st.GetUserSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings.DeveloperMode || NewUserSettingsService(st).DevModeEnabled(ctx) {
		t.Fatal("fresh fully migrated/seeded install enabled developer mode")
	}
	if closeErr := st.Close(ctx); closeErr != nil {
		t.Fatal(closeErr)
	}
	st, err = store.New(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if NewUserSettingsService(st).DevModeEnabled(ctx) {
		t.Fatal("reopen enabled developer mode without opt-in")
	}
	settings.DeveloperMode = true
	if updateErr := st.UpdateUserSettings(ctx, settings); updateErr != nil {
		t.Fatal(updateErr)
	}
	if closeErr := st.Close(ctx); closeErr != nil {
		t.Fatal(closeErr)
	}
	st, err = store.New(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if !NewUserSettingsService(st).DevModeEnabled(ctx) {
		t.Fatal("reopen discarded stored developer-mode opt-in")
	}
}
