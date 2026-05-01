package service

import (
	"encoding/json"
	"testing"
)

// TestBuildPluginEnvelopeWrap_OmitsEmptyRouting locks the wire shape consumed
// by useChat's plugin_envelope handler. Empty routing fields must be omitted
// so the FE can rely on field presence to signal "open this drawer / route to
// this panel / apply this mode preset". Includes the historical {id, type,
// data} contract — the c110 / CW-20260429-0019 regression was the parallel
// EnvelopeRef projection dropping these on the persisted side; this test
// guards the live-stream side (CW-20260429-0029).
func TestBuildPluginEnvelopeWrap_OmitsEmptyRouting(t *testing.T) {
	payload := []byte(`{"title":"Status","metric":12}`)
	wrap, err := buildPluginEnvelopeWrap("env-1", "report-card", payload, EnvelopeRouting{})
	if err != nil {
		t.Fatalf("buildPluginEnvelopeWrap: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(wrap, &got); err != nil {
		t.Fatalf("unmarshal wrap: %v", err)
	}
	if got["id"] != "env-1" {
		t.Errorf("id = %v, want env-1", got["id"])
	}
	if got["type"] != "report-card" {
		t.Errorf("type = %v, want report-card", got["type"])
	}
	if _, ok := got["data"]; !ok {
		t.Error("data missing from wrap")
	}
	for _, k := range []string{"target", "render_target", "render_target_blocked", "mode"} {
		if _, ok := got[k]; ok {
			t.Errorf("empty routing field %q should be omitted, got %v", k, got[k])
		}
	}
}

// TestBuildPluginEnvelopeWrap_IncludesNonEmptyRouting asserts that the
// routing hints surface verbatim on the wire so applyEnvelopePanelEffects
// (ui/src/lib/panel-signal.ts) can drive openPanelById /
// routeEnvelopeToPanel / resolvePanelMode from the SSE event. This is the
// fix path for the bottom_chat_drawer regression (CW-20260429-0029) where
// nanite_show_card envelopes carried render_target but the wire wrapper
// dropped it.
func TestBuildPluginEnvelopeWrap_IncludesNonEmptyRouting(t *testing.T) {
	payload := []byte(`{"title":"Status"}`)
	wrap, err := buildPluginEnvelopeWrap("env-1", "report-card", payload, EnvelopeRouting{
		Target:              "bottom_chat_drawer",
		RenderTarget:        "bottom_chat_drawer",
		RenderTargetBlocked: "",
		Mode:                "planning",
	})
	if err != nil {
		t.Fatalf("buildPluginEnvelopeWrap: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(wrap, &got); err != nil {
		t.Fatalf("unmarshal wrap: %v", err)
	}
	if got["target"] != "bottom_chat_drawer" {
		t.Errorf("target = %v, want bottom_chat_drawer", got["target"])
	}
	if got["render_target"] != "bottom_chat_drawer" {
		t.Errorf("render_target = %v, want bottom_chat_drawer (this was the bug)", got["render_target"])
	}
	if got["mode"] != "planning" {
		t.Errorf("mode = %v, want planning", got["mode"])
	}
	// Empty render_target_blocked still elided when zero-valued.
	if _, ok := got["render_target_blocked"]; ok {
		t.Errorf("render_target_blocked = %v, expected omitted when empty", got["render_target_blocked"])
	}
}

// TestBuildPluginEnvelopeWrap_EmitsRenderTargetBlocked covers the negative
// path where the trust gate dropped a render_target. The FE still wants the
// reason code so it can surface a debug pill — the routing side effects are
// no-ops because RenderTarget is empty, but the indicator round-trips.
func TestBuildPluginEnvelopeWrap_EmitsRenderTargetBlocked(t *testing.T) {
	payload := []byte(`{}`)
	wrap, err := buildPluginEnvelopeWrap("env-2", "info-card", payload, EnvelopeRouting{
		RenderTargetBlocked: "untrusted_plugin_panel",
	})
	if err != nil {
		t.Fatalf("buildPluginEnvelopeWrap: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(wrap, &got); err != nil {
		t.Fatalf("unmarshal wrap: %v", err)
	}
	if got["render_target_blocked"] != "untrusted_plugin_panel" {
		t.Errorf("render_target_blocked = %v, want untrusted_plugin_panel", got["render_target_blocked"])
	}
	if _, ok := got["render_target"]; ok {
		t.Errorf("render_target should be omitted when empty, got %v", got["render_target"])
	}
}
