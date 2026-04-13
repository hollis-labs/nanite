package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"sync/atomic"

	sdkplugin "github.com/hollis-labs/plugin-sdk"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// envelopeValidatorDevModeFn returns whether the host should treat envelope
// validation failures as dev-mode warnings (pass-through) instead of prod-mode
// drops. Wired from main.go via SetEnvelopeValidatorDevModeFunc; defaults to
// nil (strict/prod behavior).
var envelopeValidatorDevModeFn atomic.Pointer[func() bool]

// SetEnvelopeValidatorDevModeFunc installs the runtime developer-mode signal
// used by plugin envelope validation. The function is called once per
// FilterPluginEnvelopes invocation; callers should make it cheap (cache the
// settings read if needed). Passing nil clears the override and reverts to
// production-strict behavior.
func SetEnvelopeValidatorDevModeFunc(fn func() bool) {
	if fn == nil {
		envelopeValidatorDevModeFn.Store(nil)
		return
	}
	envelopeValidatorDevModeFn.Store(&fn)
}

func isEnvelopeValidatorDevMode() bool {
	if p := envelopeValidatorDevModeFn.Load(); p != nil {
		return (*p)()
	}
	return false
}

// RegisterPluginEnvelopeSchema compiles a JSON Schema for a plugin-owned
// envelope type and records it on the host. Called from
// applyManifestRegistrations when a plugin declares an envelope with a schema
// file. The schema is used by ValidatePluginEnvelope / FilterPluginEnvelopes
// at emission time (B.11).
//
// Passing schemaBytes == nil is a no-op; register only succeeds when the raw
// bytes parse as JSON and compile successfully.
func (h *Host) RegisterPluginEnvelopeSchema(pluginID, envType string, schemaBytes []byte) error {
	if pluginID == "" || envType == "" {
		return fmt.Errorf("plugin id and envelope type are required")
	}
	if len(schemaBytes) == 0 {
		return fmt.Errorf("schema bytes empty")
	}
	var raw any
	if err := json.Unmarshal(schemaBytes, &raw); err != nil {
		return fmt.Errorf("parse schema: %w", err)
	}
	c := jsonschema.NewCompiler()
	url := fmt.Sprintf("plugin://%s/envelopes/%s.schema.json", pluginID, envType)
	if err := c.AddResource(url, raw); err != nil {
		return fmt.Errorf("add schema resource: %w", err)
	}
	schema, err := c.Compile(url)
	if err != nil {
		return fmt.Errorf("compile schema: %w", err)
	}
	h.mu.Lock()
	if h.envelopeSchemas == nil {
		h.envelopeSchemas = make(map[string]map[string]*jsonschema.Schema)
	}
	byType, ok := h.envelopeSchemas[pluginID]
	if !ok {
		byType = make(map[string]*jsonschema.Schema)
		h.envelopeSchemas[pluginID] = byType
	}
	byType[envType] = schema
	h.mu.Unlock()
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
	byType := h.envelopeSchemas[pluginID]
	schema, haveSchema := byType[envType]
	h.mu.RUnlock()

	if !declared {
		return fmt.Errorf("envelope type %q not declared in registry", envType)
	}
	if entry.PluginID != pluginID {
		return fmt.Errorf("envelope type %q owned by plugin %q, not emitter %q",
			envType, entry.PluginID, pluginID)
	}
	if !haveSchema {
		// Declared without a schema — nothing to validate against.
		return nil
	}
	if err := schema.Validate(data); err != nil {
		return fmt.Errorf("schema validation: %w", err)
	}
	return nil
}

// FilterPluginEnvelopes validates each emitted envelope against its plugin's
// declared schema and returns the list that should flow downstream. In
// production mode (default) invalid envelopes are dropped with an error log;
// in developer mode (set via SetEnvelopeValidatorDevModeFunc) they pass
// through with a warning log and a "__nanite_validation_warning" marker
// injected into Data so the UI layer can surface the fault during development.
//
// Core-generated envelopes (e.g. chat.BuildKBEnvelope) do NOT pass through
// this function — they keep the advisory path in internal/chat/envelope.go.
func (h *Host) FilterPluginEnvelopes(pluginID string, envs []sdkplugin.EnvelopeOut) []sdkplugin.EnvelopeOut {
	if len(envs) == 0 {
		return envs
	}
	dev := isEnvelopeValidatorDevMode()
	out := make([]sdkplugin.EnvelopeOut, 0, len(envs))
	for _, env := range envs {
		if err := h.ValidatePluginEnvelope(pluginID, env.Type, env.Data); err != nil {
			if dev {
				h.logger.Warn("envelope validation failed (dev-mode pass-through)",
					"plugin", pluginID, "type", env.Type, "error", err.Error())
				if env.Data == nil {
					env.Data = map[string]interface{}{}
				}
				env.Data["__nanite_validation_warning"] = err.Error()
				out = append(out, env)
			} else {
				h.logger.Error("envelope validation failed — dropped",
					"plugin", pluginID, "type", env.Type, "error", err.Error())
			}
			continue
		}
		out = append(out, env)
	}
	return out
}

// unregisterPluginEnvelopeSchemasLocked drops all envelope schemas owned by a
// plugin. Caller must hold h.mu (write). Returns the number of schemas removed.
func (h *Host) unregisterPluginEnvelopeSchemasLocked(pluginID string) int {
	if h.envelopeSchemas == nil {
		return 0
	}
	n := len(h.envelopeSchemas[pluginID])
	delete(h.envelopeSchemas, pluginID)
	return n
}
