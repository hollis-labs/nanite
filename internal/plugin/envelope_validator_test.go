package plugin

import (
	"net/http"
	"strings"
	"testing"

	"github.com/hollis-labs/go-envelopes"
	sdkplugin "github.com/hollis-labs/plugin-sdk"
)

const objectSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": ["title"],
  "properties": {
    "title": {"type": "string"}
  },
  "additionalProperties": true
}`

func testHost(t *testing.T) *Host {
	t.Helper()
	h := NewHost(http.NewServeMux(), NewLogger("test"))
	// Stand up an empty registry so RegisterPluginEnvelopeSchema /
	// ValidatePluginEnvelope have storage. Tests that exercise plugin
	// types only — without core types — don't need LoadCore.
	h.SetEnvelopeRegistry(envelopes.NewRegistry())
	return h
}

func TestValidatePluginEnvelope_UndeclaredType(t *testing.T) {
	h := testHost(t)
	err := h.ValidatePluginEnvelope("p1", "missing", map[string]interface{}{"x": 1})
	if err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("expected undeclared-type error, got %v", err)
	}
}

func TestValidatePluginEnvelope_CrossPluginType(t *testing.T) {
	h := testHost(t)
	if err := h.RegisterEnvelope(EnvelopeRegistryEntry{Type: "shared", PluginID: "owner", Component: "C", Version: 1}); err != nil {
		t.Fatalf("RegisterEnvelope: %v", err)
	}
	err := h.ValidatePluginEnvelope("attacker", "shared", map[string]interface{}{})
	if err == nil || !strings.Contains(err.Error(), "owned by") {
		t.Fatalf("expected cross-plugin owner error, got %v", err)
	}
}

func TestValidatePluginEnvelope_DeclaredNoSchema_Passes(t *testing.T) {
	h := testHost(t)
	if err := h.RegisterEnvelope(EnvelopeRegistryEntry{Type: "open", PluginID: "p1", Component: "C", Version: 1}); err != nil {
		t.Fatalf("RegisterEnvelope: %v", err)
	}
	if err := h.ValidatePluginEnvelope("p1", "open", map[string]interface{}{"anything": true}); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestValidatePluginEnvelope_Schema_ValidAndInvalid(t *testing.T) {
	h := testHost(t)
	if err := h.RegisterEnvelope(EnvelopeRegistryEntry{Type: "card", PluginID: "p1", Component: "C", Version: 1}); err != nil {
		t.Fatalf("RegisterEnvelope: %v", err)
	}
	if err := h.RegisterPluginEnvelopeSchema("p1", "card", []byte(objectSchema)); err != nil {
		t.Fatalf("RegisterPluginEnvelopeSchema: %v", err)
	}
	if err := h.ValidatePluginEnvelope("p1", "card", map[string]interface{}{"title": "hello"}); err != nil {
		t.Fatalf("valid payload rejected: %v", err)
	}
	err := h.ValidatePluginEnvelope("p1", "card", map[string]interface{}{"missing": "title"})
	if err == nil || !strings.Contains(err.Error(), "schema validation") {
		t.Fatalf("expected schema validation error, got %v", err)
	}
}

func TestFilterPluginEnvelopes_ProdDropsInvalid(t *testing.T) {
	h := testHost(t)
	if err := h.RegisterEnvelope(EnvelopeRegistryEntry{Type: "card", PluginID: "p1", Component: "C", Version: 1}); err != nil {
		t.Fatalf("RegisterEnvelope: %v", err)
	}
	if err := h.RegisterPluginEnvelopeSchema("p1", "card", []byte(objectSchema)); err != nil {
		t.Fatalf("RegisterPluginEnvelopeSchema: %v", err)
	}
	// Ensure prod mode (default).
	SetEnvelopeValidatorDevModeFunc(nil)
	envs := []sdkplugin.EnvelopeOut{
		{Type: "card", Data: map[string]interface{}{"title": "ok"}},
		{Type: "card", Data: map[string]interface{}{"nope": true}}, // missing "title" → invalid
		{Type: "ghost", Data: map[string]interface{}{"a": 1}},       // undeclared type
	}
	out := h.FilterPluginEnvelopes("p1", envs)
	if len(out) != 1 {
		t.Fatalf("expected 1 envelope through prod filter, got %d", len(out))
	}
	if out[0].Data["title"] != "ok" {
		t.Fatalf("expected valid payload to pass, got %+v", out[0])
	}
}

func TestFilterPluginEnvelopes_DevPassesWithWarning(t *testing.T) {
	h := testHost(t)
	if err := h.RegisterEnvelope(EnvelopeRegistryEntry{Type: "card", PluginID: "p1", Component: "C", Version: 1}); err != nil {
		t.Fatalf("RegisterEnvelope: %v", err)
	}
	if err := h.RegisterPluginEnvelopeSchema("p1", "card", []byte(objectSchema)); err != nil {
		t.Fatalf("RegisterPluginEnvelopeSchema: %v", err)
	}
	SetEnvelopeValidatorDevModeFunc(func() bool { return true })
	t.Cleanup(func() { SetEnvelopeValidatorDevModeFunc(nil) })

	envs := []sdkplugin.EnvelopeOut{
		{Type: "card", Data: map[string]interface{}{"nope": true}},
	}
	out := h.FilterPluginEnvelopes("p1", envs)
	if len(out) != 1 {
		t.Fatalf("dev mode should pass invalid through, got %d", len(out))
	}
	if _, ok := out[0].Data["__nanite_validation_warning"]; !ok {
		t.Fatalf("expected dev-mode warning marker, got %+v", out[0].Data)
	}
}

func TestUnloadPluginDropsEnvelopeSchemas(t *testing.T) {
	h := testHost(t)
	if err := h.RegisterEnvelope(EnvelopeRegistryEntry{Type: "card", PluginID: "p1", Component: "C", Version: 1}); err != nil {
		t.Fatalf("RegisterEnvelope: %v", err)
	}
	if err := h.RegisterPluginEnvelopeSchema("p1", "card", []byte(objectSchema)); err != nil {
		t.Fatalf("RegisterPluginEnvelopeSchema: %v", err)
	}
	if !h.pluginRegistryHas("p1", "card") {
		t.Fatalf("expected p1.card registered before unload sweep")
	}

	// Plugin envelope schemas now live in the shared go-envelopes Registry
	// under "<pluginID>.<envType>". Hot-unload cleanup goes through
	// Registry.UnregisterPlugin (called from Host.UnloadPlugin); the test
	// exercises the same path directly to avoid standing up a full plugin
	// lifecycle for a unit-scoped check.
	reg := h.EnvelopeRegistry()
	if reg == nil {
		t.Fatalf("expected envelope registry on test host")
	}
	if n := reg.UnregisterPlugin("p1"); n != 1 {
		t.Fatalf("expected 1 schema removed, got %d", n)
	}
	if h.pluginRegistryHas("p1", "card") {
		t.Fatalf("expected p1.card cleared from registry after UnregisterPlugin")
	}
}
