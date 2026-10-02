package service

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/store"
)

type pluginConfigFixture struct {
	row               *store.PluginSettings
	reads             int
	writes            []map[string]any
	readErr, writeErr error
	failAfterWrite    bool
	contexts          []context.Context
}

func (f *pluginConfigFixture) GetPluginSettings(ctx context.Context, id string) (*store.PluginSettings, error) {
	f.contexts = append(f.contexts, ctx)
	f.reads++
	if f.readErr != nil || (f.failAfterWrite && len(f.writes) > 0) {
		return nil, errors.New("fixture read failure")
	}
	if f.row == nil {
		return nil, errors.New("missing")
	}
	row := *f.row
	row.Settings = map[string]any{}
	for k, v := range f.row.Settings {
		row.Settings[k] = v
	}
	return &row, nil
}
func (f *pluginConfigFixture) UpsertPluginSettings(ctx context.Context, id string, settings map[string]any) error {
	f.contexts = append(f.contexts, ctx)
	if f.writeErr != nil {
		return f.writeErr
	}
	copySettings := map[string]any{}
	for k, v := range settings {
		copySettings[k] = v
	}
	f.writes = append(f.writes, copySettings)
	if f.row == nil {
		f.row = &store.PluginSettings{PluginID: id}
	}
	f.row.Settings = copySettings
	return nil
}
func (f *pluginConfigFixture) ListPluginSettings(ctx context.Context) ([]*store.PluginSettings, error) {
	return []*store.PluginSettings{f.row}, nil
}

type configChangedRecord struct{ id, key, value string }
type pluginConfigEvents struct{ calls chan configChangedRecord }

func (e pluginConfigEvents) EmitConfigChanged(id, key, value string) {
	e.calls <- configChangedRecord{id, key, value}
}

func TestPluginConfigService_MergeSecretsAndEvents(t *testing.T) {
	ctx := t.Context()
	f := &pluginConfigFixture{row: &store.PluginSettings{PluginID: "probe", Settings: map[string]any{"keep": "retained", "replace": "old", "token": "legacy-db-value"}, Schema: []store.ConfigField{{Key: "token", Type: "secret"}, {Key: "replace", Type: "string"}}, Icon: "icon", UpdatedAt: "stamp"}}
	svc := NewPluginConfigService(f, nil)
	secrets := map[string]string{}
	svc.hasSecret = func(key string) bool { return secrets[key] != "" }
	svc.setSecret = func(key, value string) error { secrets[key] = value; return nil }
	events := make(chan configChangedRecord, 4)
	svc.events = pluginConfigEvents{events}
	result, err := svc.Update(ctx, "probe", map[string]any{"replace": "new", "token": "fresh-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.writes, []map[string]any{{"keep": "retained", "replace": "new"}}) {
		t.Fatalf("settings writes: %+v", f.writes)
	}
	if secrets["plugin:probe:token"] != "fresh-secret" {
		t.Fatalf("keychain: %+v", secrets)
	}
	if result.Settings.Settings["token"] != "********" || result.Settings.Icon != "icon" || result.Settings.UpdatedAt != "stamp" {
		t.Fatalf("response: %+v", result)
	}
	seen := map[string]bool{}
	for len(seen) < 3 {
		select {
		case event := <-events:
			if event.id != "probe" || event.value != "" {
				t.Fatalf("unsafe event: %+v", event)
			}
			seen[event.key] = true
		case <-time.After(3 * time.Second):
			t.Fatal("missing config-changed events")
		}
	}
	if !seen["keep"] || !seen["replace"] || !seen["token"] {
		t.Fatal(seen)
	}
	for _, got := range f.contexts {
		if got != ctx {
			t.Fatal("lost caller context")
		}
	}
}

func TestPluginConfigService_SecretFailurePreventsWriteAndEvents(t *testing.T) {
	f := &pluginConfigFixture{row: &store.PluginSettings{PluginID: "probe", Settings: map[string]any{}, Schema: []store.ConfigField{{Key: "token", Type: "secret"}}}}
	svc := NewPluginConfigService(f, nil)
	svc.setSecret = func(string, string) error { return errors.New("keychain failure") }
	events := make(chan configChangedRecord, 1)
	svc.events = pluginConfigEvents{events}
	_, err := svc.Update(t.Context(), "probe", map[string]any{"token": "secret"})
	if !errors.Is(err, ErrPluginSecretWrite) || len(f.writes) != 0 {
		t.Fatalf("result: %v writes=%+v", err, f.writes)
	}
	select {
	case event := <-events:
		t.Fatalf("unexpected event: %+v", event)
	default:
	}
}

func TestPluginConfigService_MaskingAndPlaceholders(t *testing.T) {
	for _, value := range []any{"", "********", 42} {
		t.Run("placeholder", func(t *testing.T) {
			f := &pluginConfigFixture{row: &store.PluginSettings{PluginID: "probe", Settings: map[string]any{"token": "old"}, Schema: []store.ConfigField{{Key: "token", Type: "secret"}}}}
			svc := NewPluginConfigService(f, nil)
			svc.hasSecret = func(string) bool { return false }
			svc.setSecret = func(string, string) error { t.Fatal("placeholder reached keychain"); return nil }
			result, err := svc.Update(t.Context(), "probe", map[string]any{"token": value})
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := result.Settings.Settings["token"]; ok {
				t.Fatalf("missing secret exposed: %+v", result)
			}
			if _, ok := f.writes[0]["token"]; ok {
				t.Fatalf("secret persisted: %+v", f.writes)
			}
		})
	}
}
func TestPluginConfigService_WriteFailureAndReadFallback(t *testing.T) {
	f := &pluginConfigFixture{writeErr: errors.New("database failure")}
	svc := NewPluginConfigService(f, nil)
	if _, err := svc.Update(t.Context(), "probe", map[string]any{"plain": "value"}); !errors.Is(err, ErrPluginConfigWrite) {
		t.Fatal(err)
	}
	f.writeErr = nil
	f.failAfterWrite = true
	result, err := svc.Update(t.Context(), "probe", map[string]any{"plain": "value"})
	if err != nil || result.Settings != nil || !reflect.DeepEqual(result.Fallback, map[string]any{"plain": "value"}) {
		t.Fatalf("fallback: %+v %v", result, err)
	}
}
