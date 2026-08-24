package chat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	nplugin "github.com/hollis-labs/nanite/internal/plugin"
	sdkplugin "github.com/hollis-labs/plugin-sdk"
)

// TestRegisterPluginCommand_AppendsEnvelopeBlocks exercises the B.12 path:
// a plugin command handler returns an "envelopes" key in its result map; the
// chat registry adapter must render each validated envelope as a fenced
// `nanite-envelope` block appended to the response content, so the existing
// chat.ParseEnvelopes pipeline picks them up downstream.
func TestRegisterPluginCommand_AppendsEnvelopeBlocks(t *testing.T) {
	r := NewCommandRegistry()

	cmd := nplugin.SlashCommandDef{
		Name:        "weather",
		Description: "Look up the weather",
		Category:    "tools",
		Handler: func(ctx context.Context, sessionID, args string) (map[string]interface{}, error) {
			return map[string]interface{}{
				"action":  "message",
				"content": "here is your forecast",
				"envelopes": []sdkplugin.EnvelopeOut{
					{Type: "weather-card", Data: map[string]interface{}{"url": "https://example.com/forecast"}},
				},
			}, nil
		},
	}
	r.RegisterPluginCommand(cmd, "weather")

	res, err := r.Execute(context.Background(), "weather", "sess-1", "sf")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Action != "message" {
		t.Fatalf("expected action=message, got %q", res.Action)
	}
	if !strings.Contains(res.Content, "```nanite-envelope") {
		t.Fatalf("expected content to contain fenced envelope, got:\n%s", res.Content)
	}

	// Round-trip through ParseEnvelopes to confirm the downstream pipeline
	// extracts the envelope cleanly — this is the B.12 acceptance criterion.
	RegisterEnvelopeType("weather-card")
	t.Cleanup(func() { UnregisterEnvelopeType("weather-card") })
	envelopes, _, errs := ParseEnvelopes(res.Content)
	if len(errs) != 0 {
		t.Fatalf("ParseEnvelopes errors: %+v", errs)
	}
	if len(envelopes) != 1 {
		t.Fatalf("expected 1 envelope parsed, got %d", len(envelopes))
	}
	if envelopes[0].Type != "weather-card" {
		t.Fatalf("unexpected envelope type: %q", envelopes[0].Type)
	}
	if u, _ := envelopes[0].Data["url"].(string); u != "https://example.com/forecast" {
		t.Fatalf("unexpected envelope data: %+v", envelopes[0].Data)
	}
}

func TestRegisterPluginCommand_OnlyEnvelopesSetsMessageAction(t *testing.T) {
	r := NewCommandRegistry()
	cmd := nplugin.SlashCommandDef{
		Name: "env-only",
		Handler: func(ctx context.Context, sessionID, args string) (map[string]interface{}, error) {
			return map[string]interface{}{
				"envelopes": []sdkplugin.EnvelopeOut{
					{Type: "env-only", Data: map[string]interface{}{"v": 1}},
				},
			}, nil
		},
	}
	r.RegisterPluginCommand(cmd, "owner")

	res, err := r.Execute(context.Background(), "env-only", "s", "")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Action != "message" {
		t.Fatalf("expected action=message when only envelopes present, got %q", res.Action)
	}
}

func TestRegisterPluginCommand_NoEnvelopesPreservesContent(t *testing.T) {
	r := NewCommandRegistry()
	cmd := nplugin.SlashCommandDef{
		Name: "plain",
		Handler: func(ctx context.Context, sessionID, args string) (map[string]interface{}, error) {
			return map[string]interface{}{
				"action":  "message",
				"content": "hello",
			}, nil
		},
	}
	r.RegisterPluginCommand(cmd, "owner")
	res, err := r.Execute(context.Background(), "plain", "s", "")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Content != "hello" {
		t.Fatalf("expected content unchanged, got %q", res.Content)
	}
}

// TestRenderEnvelopeBlock_Roundtrip confirms the fenced block format parses
// back cleanly via ParseEnvelopes.
func TestRenderEnvelopeBlock_Roundtrip(t *testing.T) {
	RegisterEnvelopeType("rt")
	t.Cleanup(func() { UnregisterEnvelopeType("rt") })

	block, err := renderEnvelopeBlock(sdkplugin.EnvelopeOut{
		Type: "rt",
		Data: map[string]interface{}{"k": "v"},
	})
	if err != nil {
		t.Fatalf("renderEnvelopeBlock: %v", err)
	}
	envelopes, _, errs := ParseEnvelopes(block)
	if len(errs) != 0 {
		t.Fatalf("ParseEnvelopes errors: %+v", errs)
	}
	if len(envelopes) != 1 {
		t.Fatalf("expected 1 envelope, got %d", len(envelopes))
	}
	raw, _ := json.Marshal(envelopes[0].Data)
	if !strings.Contains(string(raw), `"k":"v"`) {
		t.Fatalf("expected data round-trip, got %s", raw)
	}
}
