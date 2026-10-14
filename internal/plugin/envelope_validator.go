package plugin

import (
	"fmt"
	"os"

	sdkplugin "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk"
	"github.com/hollis-labs/libs/ui-go/envelopes"
)

// pluginRegistryName is the key under which a plugin envelope type lives
// in the shared go-envelopes Registry. The lib's RegisterTypeFromManifest
// auto-prefixes with the plugin id when the supplied entry type is bare,
// yielding "<pluginID>.<envType>"; Lookup must use the same form.
func pluginRegistryName(pluginID, envType string) string {
	return pluginID + "." + envType
}

// RegisterPluginEnvelopeSchema compiles a JSON Schema for a plugin-owned
// envelope type and records it in the shared go-envelopes Registry under
// "<pluginID>.<envType>". Called from applyManifestRegistrations when a
// plugin declares an envelope with a schema file. The schema is used by
// ValidatePluginEnvelope / FilterPluginEnvelopes at emission time (B.11).
//
// Empty schemaBytes returns an error — an envelope declared with a schema
// path but no loadable content is a plugin bug the host should not paper
// over. Callers that want "no schema" should simply not call this; the
// type-ownership side-map (h.envelopes via RegisterEnvelope) is enough
// for ValidatePluginEnvelope's "declared without schema → pass-through"
// branch.
func (h *Host) RegisterPluginEnvelopeSchema(pluginID, envType string, schemaBytes []byte) error {
	if pluginID == "" || envType == "" {
		return fmt.Errorf("plugin id and envelope type are required")
	}
	if len(schemaBytes) == 0 {
		return fmt.Errorf("schema bytes empty")
	}
	h.mu.RLock()
	reg := h.envelopeRegistry
	h.mu.RUnlock()
	if reg == nil {
		return fmt.Errorf("envelope registry not configured on host")
	}
	manifestBytes := []byte(`{"type":"` + envType + `"}`)
	if err := reg.RegisterTypeFromManifest(manifestBytes, schemaBytes, pluginID); err != nil {
		return fmt.Errorf("register schema: %w", err)
	}
	return nil
}

// registerPluginEnvelopeSchemaFromFile is a convenience wrapper used by
// applyManifestRegistrations when the manifest carries an on-disk schema path.
func (h *Host) registerPluginEnvelopeSchemaFromFile(pluginID, envType, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	return h.RegisterPluginEnvelopeSchema(pluginID, envType, data)
}

// ValidatePluginEnvelope returns nil if the envelope is valid for the given
// plugin. A non-nil error describes why validation failed (undeclared type,
// cross-plugin type, or schema violation). If the envelope type is declared
// but no schema is registered, validation passes — callers may still log an
// advisory since a schema-less envelope cannot be strictly enforced.
func (h *Host) ValidatePluginEnvelope(pluginID, envType string, data any) error {
	if pluginID == "" {
		return fmt.Errorf("plugin id is required")
	}
	if envType == "" {
		return fmt.Errorf("envelope type is required")
	}
	h.mu.RLock()
	entry, declared := h.envelopes[envType]
	reg := h.envelopeRegistry
	h.mu.RUnlock()

	if !declared {
		return fmt.Errorf("envelope type %q not declared in registry", envType)
	}
	if entry.PluginID != pluginID {
		return fmt.Errorf("envelope type %q owned by plugin %q, not emitter %q",
			envType, entry.PluginID, pluginID)
	}
	if reg == nil {
		// No registry configured (test-only) — degrade to declared-without-schema.
		return nil
	}
	spec, ok := reg.Lookup(pluginRegistryName(pluginID, envType))
	if !ok || spec.DataSchema == nil {
		// Declared without a schema — nothing to validate against.
		return nil
	}
	if err := spec.DataSchema.Validate(data); err != nil {
		return fmt.Errorf("schema validation: %w", err)
	}
	return nil
}

// FilterPluginEnvelopes validates each emitted envelope against its plugin's
// declared schema and returns the list that should flow downstream. Invalid
// envelopes are always dropped with an error log; there is no pass-through mode.
//
// Core-generated envelopes (e.g. from a tool's <!--ENVELOPE_DATA:...-->
// marker, captured by captureEnvelopeData in internal/service/chat_generate.go)
// do NOT pass through this function — they keep the advisory path in
// internal/chat/envelope.go.
func (h *Host) FilterPluginEnvelopes(pluginID string, envs []sdkplugin.EnvelopeOut) []sdkplugin.EnvelopeOut {
	if len(envs) == 0 {
		return envs
	}
	out := make([]sdkplugin.EnvelopeOut, 0, len(envs))
	for _, env := range envs {
		if err := h.ValidatePluginEnvelope(pluginID, env.Type, env.Data); err != nil {
			h.logger.Error("envelope validation failed — dropped",
				"plugin", pluginID, "type", env.Type, "error", err.Error())
			continue
		}
		out = append(out, env)
	}
	return out
}

// pluginRegistryHas reports whether the shared registry knows about the
// "<pluginID>.<envType>" entry. Test helper retained for migration coverage
// (TestUnloadPlugin*Schema tests) so we can assert registry-side cleanup
// without exporting envelopes.Registry through the test seam.
func (h *Host) pluginRegistryHas(pluginID, envType string) bool {
	h.mu.RLock()
	reg := h.envelopeRegistry
	h.mu.RUnlock()
	if reg == nil {
		return false
	}
	return reg.Has(pluginRegistryName(pluginID, envType))
}

// EnvelopeRegistry returns the shared registry installed via
// SetEnvelopeRegistry. May be nil in unit tests that don't exercise schema
// validation. Exported so test files in the same package can stand up a
// registry alongside a Host without re-importing envelopes everywhere.
func (h *Host) EnvelopeRegistry() *envelopes.Registry {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.envelopeRegistry
}
