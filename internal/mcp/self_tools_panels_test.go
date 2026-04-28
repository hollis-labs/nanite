package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/dispatch"
)

// fakePanelSink captures panel_signal broadcasts for assertion in tests.
type fakePanelSink struct {
	calls []fakePanelCall
}

type fakePanelCall struct {
	sessionID  string
	signalType string
	payload    string
}

func (f *fakePanelSink) BroadcastPanelSignal(sessionID, signalType, payload string) int {
	f.calls = append(f.calls, fakePanelCall{sessionID, signalType, payload})
	return 1
}

// fakeTrustResolver returns a fixed tier (and optional error) for any input.
type fakeTrustResolver struct {
	tier dispatch.TrustTier
	err  error
}

func (f *fakeTrustResolver) ResolveTrust(_ context.Context, _, _ string) (dispatch.TrustTier, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.tier, nil
}

// newPanelTransport builds a minimally-wired SelfToolsTransport for panel tests.
// A nil sink leaves the field unset (interface nil) so callers can verify the
// nil-safe path; passing a non-nil *fakePanelSink wires it as the sink.
func newPanelTransport(sink *fakePanelSink, lookup PanelLookup, resolver PanelTrustResolver) *SelfToolsTransport {
	st := &SelfToolsTransport{
		PanelLookup:   lookup,
		TrustResolver: resolver,
	}
	if sink != nil {
		st.PanelSignalSink = sink
	}
	return st
}

func TestPanelOpen_BuiltinPanel_BroadcastsAgentSignal(t *testing.T) {
	for _, panelID := range []string{"bottom_chat_drawer", "work", "workflows"} {
		t.Run(panelID, func(t *testing.T) {
			sink := &fakePanelSink{}
			st := newPanelTransport(sink, nil, nil)
			ctx := WithSessionID(context.Background(), "sess-1")

			res, err := st.callPanelOpen(ctx, map[string]any{"panel_id": panelID})
			if err != nil {
				t.Fatalf("callPanelOpen returned error: %v", err)
			}
			body := readToolText(t, res)
			var got map[string]any
			if err := json.Unmarshal([]byte(body), &got); err != nil {
				t.Fatalf("result is not JSON: %v (body=%q)", err, body)
			}
			if got["opened"] != true {
				t.Fatalf("expected opened=true for built-in %q, got %v (body=%q)", panelID, got["opened"], body)
			}
			if len(sink.calls) != 1 {
				t.Fatalf("expected 1 panel_signal broadcast, got %d", len(sink.calls))
			}
			call := sink.calls[0]
			if call.sessionID != "sess-1" {
				t.Fatalf("session_id: want sess-1 got %q", call.sessionID)
			}
			if call.signalType != "panel_signal" {
				t.Fatalf("signal type: want panel_signal got %q", call.signalType)
			}
			var sig PanelSignal
			if err := json.Unmarshal([]byte(call.payload), &sig); err != nil {
				t.Fatalf("payload not PanelSignal JSON: %v (raw=%q)", err, call.payload)
			}
			if sig.Action != "open" || sig.PanelID != panelID || sig.Source != "agent" {
				t.Fatalf("payload mismatch: %+v", sig)
			}
		})
	}
}

func TestPanelOpen_UnknownPanel_NoBroadcastAndReason(t *testing.T) {
	sink := &fakePanelSink{}
	st := newPanelTransport(sink, nil, nil)
	res, err := st.callPanelOpen(context.Background(), map[string]any{"panel_id": "ghost"})
	if err != nil {
		t.Fatalf("callPanelOpen returned error: %v", err)
	}
	body := readToolText(t, res)
	if !strings.Contains(body, `"opened":false`) {
		t.Fatalf("expected opened:false, got %q", body)
	}
	if !strings.Contains(body, `"reason":"unknown_panel"`) {
		t.Fatalf("expected reason=unknown_panel, got %q", body)
	}
	if len(sink.calls) != 0 {
		t.Fatalf("expected NO broadcast for unknown panel, got %d", len(sink.calls))
	}
}

func TestPanelOpen_PluginPanel_RequiresTrustedTier(t *testing.T) {
	cases := []struct {
		name       string
		resolver   PanelTrustResolver
		ctxProfile bool
		wantOpened bool
		wantReason string
	}{
		{
			name:       "no caller profile in ctx → untrusted",
			resolver:   &fakeTrustResolver{tier: dispatch.TrustTrusted},
			ctxProfile: false,
			wantOpened: false,
			wantReason: "untrusted",
		},
		{
			name:       "TrustNormal → untrusted (only TrustTrusted opens plugin panels)",
			resolver:   &fakeTrustResolver{tier: dispatch.TrustNormal},
			ctxProfile: true,
			wantOpened: false,
			wantReason: "untrusted",
		},
		{
			name:       "resolver error → untrusted (safe default)",
			resolver:   &fakeTrustResolver{err: errors.New("db down")},
			ctxProfile: true,
			wantOpened: false,
			wantReason: "untrusted",
		},
		{
			name:       "TrustTrusted → opened",
			resolver:   &fakeTrustResolver{tier: dispatch.TrustTrusted},
			ctxProfile: true,
			wantOpened: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sink := &fakePanelSink{}
			lookup := func() []string { return []string{"plugin-doc-pad"} }
			st := newPanelTransport(sink, lookup, tc.resolver)
			ctx := WithSessionID(context.Background(), "sess-1")
			if tc.ctxProfile {
				ctx = WithCallerProfile(ctx, "ws-1", "ap-1")
			}
			res, err := st.callPanelOpen(ctx, map[string]any{"panel_id": "plugin-doc-pad"})
			if err != nil {
				t.Fatalf("callPanelOpen returned error: %v", err)
			}
			body := readToolText(t, res)
			if tc.wantOpened {
				if !strings.Contains(body, `"opened":true`) {
					t.Fatalf("want opened=true, body=%q", body)
				}
				if len(sink.calls) != 1 {
					t.Fatalf("want 1 broadcast on trusted open, got %d", len(sink.calls))
				}
			} else {
				if !strings.Contains(body, `"opened":false`) || !strings.Contains(body, `"reason":"`+tc.wantReason+`"`) {
					t.Fatalf("want opened=false reason=%s, body=%q", tc.wantReason, body)
				}
				if len(sink.calls) != 0 {
					t.Fatalf("want NO broadcast for untrusted plugin open, got %d", len(sink.calls))
				}
			}
		})
	}
}

func TestPanelClose_BuiltinPanel_BroadcastsCloseSignal(t *testing.T) {
	sink := &fakePanelSink{}
	st := newPanelTransport(sink, nil, nil)
	ctx := WithSessionID(context.Background(), "sess-1")

	res, err := st.callPanelClose(ctx, map[string]any{"panel_id": "work"})
	if err != nil {
		t.Fatalf("callPanelClose returned error: %v", err)
	}
	body := readToolText(t, res)
	if !strings.Contains(body, `"closed":true`) {
		t.Fatalf("want closed=true, body=%q", body)
	}
	if len(sink.calls) != 1 {
		t.Fatalf("want 1 broadcast, got %d", len(sink.calls))
	}
	var sig PanelSignal
	if err := json.Unmarshal([]byte(sink.calls[0].payload), &sig); err != nil {
		t.Fatalf("bad payload JSON: %v", err)
	}
	if sig.Action != "close" || sig.PanelID != "work" || sig.Source != "agent" {
		t.Fatalf("unexpected payload: %+v", sig)
	}
}

func TestPanelOpen_MissingPanelID_ReturnsError(t *testing.T) {
	sink := &fakePanelSink{}
	st := newPanelTransport(sink, nil, nil)
	res, err := st.callPanelOpen(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("expected error result, got %+v", res)
	}
}

func TestSignalMode_BroadcastsModeSignal(t *testing.T) {
	sink := &fakePanelSink{}
	st := newPanelTransport(sink, nil, nil)
	ctx := WithSessionID(context.Background(), "sess-1")

	res, err := st.callSignalMode(ctx, map[string]any{"mode": "planning"})
	if err != nil {
		t.Fatalf("callSignalMode returned error: %v", err)
	}
	body := readToolText(t, res)
	if !strings.Contains(body, `"signaled":true`) || !strings.Contains(body, `"mode":"planning"`) {
		t.Fatalf("unexpected result body: %q", body)
	}
	if len(sink.calls) != 1 {
		t.Fatalf("want 1 broadcast, got %d", len(sink.calls))
	}
	var sig PanelSignal
	if err := json.Unmarshal([]byte(sink.calls[0].payload), &sig); err != nil {
		t.Fatalf("bad payload JSON: %v", err)
	}
	if sig.Action != "mode" || sig.Mode != "planning" || sig.Source != "agent" {
		t.Fatalf("unexpected payload: %+v", sig)
	}
}

func TestSignalMode_UnknownModeStillBroadcastsByDesign(t *testing.T) {
	// The contract: emission always succeeds; the FE owns preset interpretation.
	// Unknown modes must still emit so a forward-compatible FE can pick them up
	// without a backend change.
	sink := &fakePanelSink{}
	st := newPanelTransport(sink, nil, nil)
	ctx := WithSessionID(context.Background(), "sess-1")
	if _, err := st.callSignalMode(ctx, map[string]any{"mode": "future-mode-not-in-v1"}); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(sink.calls) != 1 {
		t.Fatalf("want 1 broadcast for unknown mode (FE silently ignores), got %d", len(sink.calls))
	}
}

func TestPanelOpen_NoSink_StillReturnsConfirmation(t *testing.T) {
	// When PanelSignalSink is unwired (e.g. headless test) the tool still
	// returns {opened:true} so a transcript replay can reconstruct the intent
	// from the tool call alone.
	st := newPanelTransport(nil, nil, nil)
	ctx := WithSessionID(context.Background(), "sess-1")
	res, err := st.callPanelOpen(ctx, map[string]any{"panel_id": "work"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !strings.Contains(readToolText(t, res), `"opened":true`) {
		t.Fatalf("expected opened=true even with nil sink")
	}
}

// readToolText extracts the text content from a *ToolResult or fails.
func readToolText(t *testing.T, res *ToolResult) string {
	t.Helper()
	if res == nil {
		t.Fatal("nil ToolResult")
	}
	if len(res.Content) == 0 {
		t.Fatal("ToolResult has no content")
	}
	c := res.Content[0]
	return c.Text
}
