package api

import (
	"context"
	"math"

	"github.com/hollis-labs/go-envelopes/admin"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

type adminPreferencesBackend struct {
	api      *API
	revision string
}

func (b adminPreferencesBackend) settings() (*service.UserSettingsService, error) {
	if b.api == nil || b.api.Services == nil || b.api.Services.Settings == nil {
		return nil, adminUnavailable()
	}
	return b.api.Services.Settings, nil
}

func (b adminPreferencesBackend) state(p *store.AdminPreferences) (admin.State, error) {
	if p == nil {
		return admin.State{}, adminUnavailable()
	}
	stream, err := admin.String(p.ToolStreamBehavior)
	if err != nil {
		return admin.State{}, err
	}
	retention, err := admin.Integer(int64(p.ToolDrawerRetention))
	if err != nil {
		return admin.State{}, err
	}
	return admin.State{Revision: b.revision, Version: b.revision + ":" + p.Version, Values: map[string]admin.ResolvedValue{
		"tool_stream_behavior":  preferenceValue("tool_stream_behavior", stream),
		"tool_drawer_retention": preferenceValue("tool_drawer_retention", retention),
	}}, nil
}

// Baseline equality is the declared canonical representation, not historical
// provenance. Explicit SET-default and keyed reset have identical semantics.
func preferenceValue(key string, value admin.Scalar) admin.ResolvedValue {
	source := admin.Source{Kind: admin.OverrideSource, Label: "Persisted application preference"}
	override := true
	if key == "tool_stream_behavior" && value.Value() == "streaming" || key == "tool_drawer_retention" && value.Value() == float64(15) {
		source = admin.Source{Kind: admin.DefaultSource, Label: "Application preference default"}
		override = false
	}
	return admin.ResolvedValue{Present: true, Value: value, Source: &source, Editable: true, HasOverride: override, ApplyState: admin.UnknownApply}
}

func (b adminPreferencesBackend) Read(ctx context.Context) (admin.State, error) {
	settings, err := b.settings()
	if err != nil {
		return admin.State{}, err
	}
	p, err := settings.AdminPreferences(ctx)
	if err != nil {
		return admin.State{}, err
	}
	return b.state(p)
}

func resolvePreferences(base admin.State, changes admin.Changes) (admin.State, error) {
	candidate := base
	candidate.Values = make(map[string]admin.ResolvedValue, len(base.Values))
	for key, value := range base.Values {
		candidate.Values[key] = value
	}
	for key, value := range changes.Set {
		if _, ok := base.Values[key]; !ok {
			return admin.State{}, &admin.Failure{Code: admin.MalformedInput, Message: "Unknown preference key."}
		}
		candidate.Values[key] = preferenceValue(key, value)
	}
	for _, key := range changes.Unset {
		var value admin.Scalar
		switch key {
		case "tool_stream_behavior":
			value, _ = admin.String("streaming")
		case "tool_drawer_retention":
			value, _ = admin.Integer(15)
		default:
			return admin.State{}, &admin.Failure{Code: admin.MalformedInput, Message: "Unknown preference key."}
		}
		candidate.Values[key] = preferenceValue(key, value)
	}
	return candidate, nil
}

func (b adminPreferencesBackend) Preview(ctx context.Context, changes admin.Changes) (admin.State, error) {
	base, err := b.Read(ctx)
	if err != nil {
		return admin.State{}, err
	}
	return resolvePreferences(base, changes) // Retain this fresh base's version; no writes.
}

func (b adminPreferencesBackend) WithTransaction(ctx context.Context, fn func(admin.GroupTransaction) error) error {
	settings, err := b.settings()
	if err != nil {
		return err
	}
	return settings.WithAdminPreferencesTransaction(ctx, func(tx *store.PreferencesTransaction) error {
		return fn(adminPreferencesTransaction{backend: b, tx: tx})
	})
}

type adminPreferencesTransaction struct {
	backend adminPreferencesBackend
	tx      *store.PreferencesTransaction
}

func (t adminPreferencesTransaction) Current() (admin.State, error) {
	p, err := t.tx.Current()
	if err != nil {
		return admin.State{}, err
	}
	return t.backend.state(p)
}
func (t adminPreferencesTransaction) Resolve(changes admin.Changes) (admin.State, error) {
	base, err := t.Current()
	if err != nil {
		return admin.State{}, err
	}
	return resolvePreferences(base, changes)
}
func (t adminPreferencesTransaction) Stage(changes admin.Changes) (admin.Staged, error) {
	before, err := t.Current()
	if err != nil {
		return admin.Staged{}, err
	}
	candidate, err := resolvePreferences(before, changes)
	if err != nil {
		return admin.Staged{}, err
	}
	stream, ok := candidate.Values["tool_stream_behavior"].Value.Value().(string)
	retention, number := candidate.Values["tool_drawer_retention"].Value.Value().(float64)
	// The binding validates the complete candidate before Stage. Fail closed if
	// called directly with values that cannot faithfully enter native persistence.
	if !ok || !number || math.Trunc(retention) != retention || service.ValidateToolStreamBehavior(stream) != nil || retention < -1 || retention > 60 || service.ValidateToolDrawerRetention(int(retention)) != nil {
		return admin.Staged{}, adminUnavailable()
	}
	p, err := t.tx.Stage(store.AdminPreferences{ToolStreamBehavior: stream, ToolDrawerRetention: int(retention)})
	if err != nil {
		return admin.Staged{}, err
	}
	after, err := t.backend.state(p)
	if err != nil {
		return admin.Staged{}, err
	}
	changed := []string{}
	for _, key := range []string{"tool_stream_behavior", "tool_drawer_retention"} {
		if before.Values[key].Value.Value() != after.Values[key].Value.Value() {
			changed = append(changed, key)
		}
	}
	return admin.Staged{State: after, ChangedKeys: changed, Restart: admin.Restart{Required: false, Targets: []string{}}}, nil
}
