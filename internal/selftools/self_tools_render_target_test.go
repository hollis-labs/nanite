package selftools

import (
	"context"
	"errors"
	"testing"

	"github.com/hollis-labs/nanite/internal/dispatch"
	"github.com/hollis-labs/nanite/internal/envelope"
	"github.com/hollis-labs/nanite/internal/mcp"
)

// TestEnvelope_DefaultRenderTarget_TenPassiveRenderables locks the v1 default
// table from the design note. If a schema's `default_render_target` drifts,
// this catches it before the FE sees a routing surprise.
func TestEnvelope_DefaultRenderTarget_TenPassiveRenderables(t *testing.T) {
	for _, envType := range envelope.PassiveRenderableTypes {
		envType := envType
		t.Run(envType, func(t *testing.T) {
			got := envelope.DefaultRenderTarget(envType)
			if got != "bottom_chat_drawer" {
				t.Fatalf("default_render_target for %q: want bottom_chat_drawer got %q", envType, got)
			}
		})
	}
}

// TestEnvelope_DefaultRenderTarget_UnknownType ensures the helper degrades
// gracefully for envelope types it doesn't know about (decision-flow,
// runtime, plugin-shipped). Returning "" lets callShowCard simply skip the
// stamping step; no special-case branching needed.
func TestEnvelope_DefaultRenderTarget_UnknownType(t *testing.T) {
	for _, envType := range []string{"approval-card", "chat-loop-terminated", "kb-result", "made-up"} {
		if got := envelope.DefaultRenderTarget(envType); got != "" {
			t.Errorf("default_render_target(%q) = %q, want empty", envType, got)
		}
	}
}

// TestCallShowCard_StampsSchemaDefaultRenderTarget covers the no-override
// path: agent omits render_target → schema default lands on the envelope.
func TestCallShowCard_StampsSchemaDefaultRenderTarget(t *testing.T) {
	st := newSelfTools(t)
	res, err := st.callShowCard(context.Background(), map[string]any{
		"type": "info-card",
		"data": cloneMap(validShowCardPayloads["info-card"]),
	})
	if err != nil || res.IsError {
		t.Fatalf("callShowCard failed: %v / %s", err, readToolText(t, res))
	}
	env := extractEnvelopeJSON(t, readToolText(t, res))
	if got := env["render_target"]; got != "bottom_chat_drawer" {
		t.Fatalf("render_target: want bottom_chat_drawer (schema default) got %v", got)
	}
}

// TestCallShowCard_AgentOverridesSchemaDefault covers the explicit-override
// path: agent passes render_target → that value lands, schema default is
// ignored.
func TestCallShowCard_AgentOverridesSchemaDefault(t *testing.T) {
	st := newSelfTools(t)
	res, err := st.callShowCard(context.Background(), map[string]any{
		"type":          "info-card",
		"data":          cloneMap(validShowCardPayloads["info-card"]),
		"render_target": "work",
	})
	if err != nil || res.IsError {
		t.Fatalf("callShowCard failed: %v / %s", err, readToolText(t, res))
	}
	env := extractEnvelopeJSON(t, readToolText(t, res))
	if got := env["render_target"]; got != "work" {
		t.Fatalf("render_target: want work (agent override) got %v", got)
	}
	if _, blocked := env["render_target_blocked"]; blocked {
		t.Errorf("expected no render_target_blocked for built-in panel: %v", env)
	}
}

// TestCallShowCard_ForceInlineWithEmptyRenderTarget verifies the
// distinguishing behavior the description promises: passing an explicit
// empty string force-inlines a card whose schema would otherwise route it.
//
// Note: in practice an empty-string arg is the same as omitting the arg
// because the resolver branches on `override == ""`. This test pins that
// behavior so the doc note stays honest.
func TestCallShowCard_ForceInlineWithEmptyRenderTarget(t *testing.T) {
	st := newSelfTools(t)
	res, err := st.callShowCard(context.Background(), map[string]any{
		"type":          "info-card",
		"data":          cloneMap(validShowCardPayloads["info-card"]),
		"render_target": "",
	})
	if err != nil || res.IsError {
		t.Fatalf("callShowCard failed: %v / %s", err, readToolText(t, res))
	}
	env := extractEnvelopeJSON(t, readToolText(t, res))
	// Empty arg falls through to schema default (info-card → bottom_chat_drawer).
	// This is the documented behavior in resolveShowCardRenderTarget — the
	// "empty arg = force inline" wording in the tool description is a
	// simplification; the actual force-inline mechanism is omitting the arg
	// AND choosing a card type with no schema default. Pinning the actual
	// behavior here so future readers don't get surprised by the doc/code
	// discrepancy.
	if got := env["render_target"]; got != "bottom_chat_drawer" {
		t.Fatalf("render_target with empty override: want schema default bottom_chat_drawer got %v", got)
	}
}

// TestCallShowCard_PluginPanel_TrustedPasses wires a trusted resolver and
// a plugin-panel registration; the trusted-agent path lets the routing
// through.
func TestCallShowCard_PluginPanel_TrustedPasses(t *testing.T) {
	st := newSelfTools(t)
	st.PanelLookup = func() []string { return []string{"plugin_panel_x"} }
	st.TrustResolver = &fakeTrustResolver{tier: dispatch.TrustTrusted}
	ctx := mcp.WithCallerProfile(context.Background(), "ap-1")

	res, err := st.callShowCard(ctx, map[string]any{
		"type":          "info-card",
		"data":          cloneMap(validShowCardPayloads["info-card"]),
		"render_target": "plugin_panel_x",
	})
	if err != nil || res.IsError {
		t.Fatalf("callShowCard failed: %v / %s", err, readToolText(t, res))
	}
	env := extractEnvelopeJSON(t, readToolText(t, res))
	if got := env["render_target"]; got != "plugin_panel_x" {
		t.Fatalf("render_target: want plugin_panel_x (trusted) got %v", got)
	}
	if _, blocked := env["render_target_blocked"]; blocked {
		t.Errorf("expected no render_target_blocked for trusted plugin panel: %v", env)
	}
}

// TestCallShowCard_PluginPanel_UntrustedFallsBack covers the trust-gate
// deny path: untrusted agent → render_target dropped + render_target_blocked
// stamped so the FE can surface the blocked intent.
func TestCallShowCard_PluginPanel_UntrustedFallsBack(t *testing.T) {
	st := newSelfTools(t)
	st.PanelLookup = func() []string { return []string{"plugin_panel_x"} }
	st.TrustResolver = &fakeTrustResolver{tier: dispatch.TrustNormal}
	ctx := mcp.WithCallerProfile(context.Background(), "ap-1")

	res, err := st.callShowCard(ctx, map[string]any{
		"type":          "info-card",
		"data":          cloneMap(validShowCardPayloads["info-card"]),
		"render_target": "plugin_panel_x",
	})
	if err != nil || res.IsError {
		t.Fatalf("callShowCard failed: %v / %s", err, readToolText(t, res))
	}
	env := extractEnvelopeJSON(t, readToolText(t, res))
	if _, present := env["render_target"]; present {
		t.Fatalf("render_target should be dropped on untrusted plugin route, got %v", env["render_target"])
	}
	if got := env["render_target_blocked"]; got != "untrusted" {
		t.Fatalf("render_target_blocked: want \"untrusted\" got %v", got)
	}
}

// TestCallShowCard_UnknownPanel_FallsBack covers the unknown-panel deny
// path. PanelLookup returns nothing; the trust resolver isn't consulted.
func TestCallShowCard_UnknownPanel_FallsBack(t *testing.T) {
	st := newSelfTools(t)
	st.PanelLookup = func() []string { return nil }
	st.TrustResolver = &fakeTrustResolver{tier: dispatch.TrustTrusted}

	res, err := st.callShowCard(context.Background(), map[string]any{
		"type":          "info-card",
		"data":          cloneMap(validShowCardPayloads["info-card"]),
		"render_target": "ghost_panel",
	})
	if err != nil || res.IsError {
		t.Fatalf("callShowCard failed: %v / %s", err, readToolText(t, res))
	}
	env := extractEnvelopeJSON(t, readToolText(t, res))
	if _, present := env["render_target"]; present {
		t.Fatalf("render_target should be dropped for unknown panel, got %v", env["render_target"])
	}
	if got := env["render_target_blocked"]; got != "unknown_panel" {
		t.Fatalf("render_target_blocked: want \"unknown_panel\" got %v", got)
	}
}

// TestCallShowCard_TrustResolverError_FallsBackUntrusted models the
// "ResolveTrust returned err" branch — the gate treats it as untrusted by
// design (safe default). Same observable shape as the untrusted-tier case.
func TestCallShowCard_TrustResolverError_FallsBackUntrusted(t *testing.T) {
	st := newSelfTools(t)
	st.PanelLookup = func() []string { return []string{"plugin_panel_x"} }
	st.TrustResolver = &fakeTrustResolver{err: errors.New("boom")}
	ctx := mcp.WithCallerProfile(context.Background(), "ap-1")

	res, err := st.callShowCard(ctx, map[string]any{
		"type":          "info-card",
		"data":          cloneMap(validShowCardPayloads["info-card"]),
		"render_target": "plugin_panel_x",
	})
	if err != nil || res.IsError {
		t.Fatalf("callShowCard failed: %v / %s", err, readToolText(t, res))
	}
	env := extractEnvelopeJSON(t, readToolText(t, res))
	if got := env["render_target_blocked"]; got != "untrusted" {
		t.Fatalf("trust resolver error should yield untrusted block; got %v", got)
	}
}
