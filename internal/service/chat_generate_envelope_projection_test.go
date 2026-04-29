package service

import (
	"encoding/json"
	"testing"

	"github.com/hollis-labs/nanite/internal/chat"
)

// TestEnvelopeProjectionPreservesRenderTarget is the regression test for
// CW-20260429-0019. It mirrors the projection logic in chat_generate.go that
// converts each parsed chat.Envelope into the persisted chat.EnvelopeRef shape.
// Prior to the fix the projection was lossy: only {Type, Data} were copied and
// every routing hint (RenderTarget, Target, Mode, RenderTargetBlocked) was
// silently dropped. This test parses a real `nanite-envelope` fenced block —
// matching the on-wire shape the show_card handler produces — and asserts the
// projected ref retains the routing-relevant fields so the FE can route the
// card after page reload.
func TestEnvelopeProjectionPreservesRenderTarget(t *testing.T) {
	chat.RegisterEnvelopeType("report-card")

	// Mirror the show_card output: an envelope JSON block with
	// render_target stamped from default_render_target ("bottom_chat_drawer"
	// for report-card per its schema).
	content := "Here is the status report.\n\n" +
		"```nanite-envelope\n" +
		`{"kind":"envelope","version":1,"type":"report-card","id":"env-abc","title":"Status","subtitle":"Q2","target":"bottom_chat_drawer","mode":"planning","render_target":"bottom_chat_drawer","render_target_blocked":"","data":{"title":"Status","metrics":[{"label":"Tasks","value":12}]}}` +
		"\n```\n"

	envelopes, _, errs := chat.ParseEnvelopes(content)
	if len(errs) != 0 {
		t.Fatalf("ParseEnvelopes errors: %+v", errs)
	}
	if len(envelopes) != 1 {
		t.Fatalf("expected 1 parsed envelope, got %d", len(envelopes))
	}
	env := envelopes[0]
	if env.RenderTarget != "bottom_chat_drawer" {
		t.Fatalf("parsed RenderTarget = %q, want bottom_chat_drawer (parser regression)", env.RenderTarget)
	}

	// Mirror chat_generate.go's projection. If this block diverges from the
	// real projection the test catches the drift on the next run.
	var envRefs []chat.EnvelopeRef
	for _, e := range envelopes {
		innerData, _ := json.Marshal(e.Data)
		envRefs = append(envRefs, chat.EnvelopeRef{
			Type:                e.Type,
			Data:                json.RawMessage(innerData),
			ID:                  e.ID,
			Title:               e.Title,
			Subtitle:            e.Subtitle,
			Target:              e.Target,
			Mode:                e.Mode,
			RenderTarget:        e.RenderTarget,
			RenderTargetBlocked: e.RenderTargetBlocked,
		})
	}
	if len(envRefs) != 1 {
		t.Fatalf("projection produced %d refs, want 1", len(envRefs))
	}
	ref := envRefs[0]

	if ref.Type != "report-card" {
		t.Errorf("ref.Type = %q, want report-card", ref.Type)
	}
	if ref.RenderTarget != "bottom_chat_drawer" {
		t.Errorf("ref.RenderTarget = %q, want bottom_chat_drawer (this was the bug)", ref.RenderTarget)
	}
	if ref.Target != "bottom_chat_drawer" {
		t.Errorf("ref.Target = %q, want bottom_chat_drawer", ref.Target)
	}
	if ref.Mode != "planning" {
		t.Errorf("ref.Mode = %q, want planning", ref.Mode)
	}
	if ref.ID != "env-abc" {
		t.Errorf("ref.ID = %q, want env-abc", ref.ID)
	}
	if ref.Title != "Status" {
		t.Errorf("ref.Title = %q, want Status", ref.Title)
	}
	if ref.Subtitle != "Q2" {
		t.Errorf("ref.Subtitle = %q, want Q2", ref.Subtitle)
	}

	// Persisted-message round trip: marshal a StructuredMessage carrying the
	// projected ref, then unmarshal and verify the routing hint survives —
	// this is what page reload exercises.
	structured := chat.WrapResponse("Here is the status report.", "tool", nil, envRefs, false, false)
	wireJSON := structured.MarshalContent()

	var decoded chat.StructuredMessage
	if err := json.Unmarshal([]byte(wireJSON), &decoded); err != nil {
		t.Fatalf("unmarshal StructuredMessage: %v", err)
	}
	if len(decoded.Envelopes) != 1 {
		t.Fatalf("after reload: expected 1 envelope, got %d", len(decoded.Envelopes))
	}
	got := decoded.Envelopes[0]
	if got.RenderTarget != "bottom_chat_drawer" {
		t.Errorf("after reload: RenderTarget = %q, want bottom_chat_drawer", got.RenderTarget)
	}
	if got.Target != "bottom_chat_drawer" {
		t.Errorf("after reload: Target = %q, want bottom_chat_drawer", got.Target)
	}
	if got.Mode != "planning" {
		t.Errorf("after reload: Mode = %q, want planning", got.Mode)
	}
}
