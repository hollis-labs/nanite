package service

import (
	"context"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/tool/intent"
)

func TestToolCacheCommand_On(t *testing.T) {
	st := newToolCacheOverrideStore()
	h := toolCacheCommandHandler(st)

	res, err := h(context.Background(), "sess-A", "on")
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if res.Action != "message" {
		t.Fatalf("action: got %q, want %q", res.Action, "message")
	}
	if !strings.Contains(strings.ToLower(res.Content), "pinned **on**") {
		t.Fatalf("content should confirm pin on: %q", res.Content)
	}
	if st.Get("sess-A") != intent.OverrideOn {
		t.Fatalf("override should be OverrideOn, got %v", st.Get("sess-A"))
	}
}

func TestToolCacheCommand_Off(t *testing.T) {
	st := newToolCacheOverrideStore()
	h := toolCacheCommandHandler(st)

	res, err := h(context.Background(), "sess-A", "off")
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if !strings.Contains(strings.ToLower(res.Content), "pinned **off**") {
		t.Fatalf("content: %q", res.Content)
	}
	if st.Get("sess-A") != intent.OverrideOff {
		t.Fatalf("override: got %v", st.Get("sess-A"))
	}
}

func TestToolCacheCommand_AutoClearsPin(t *testing.T) {
	st := newToolCacheOverrideStore()
	st.Set("sess-A", intent.OverrideOn)
	h := toolCacheCommandHandler(st)

	for _, arg := range []string{"auto", "clear"} {
		t.Run(arg, func(t *testing.T) {
			st.Set("sess-A", intent.OverrideOn)
			res, err := h(context.Background(), "sess-A", arg)
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			if !strings.Contains(res.Content, "cleared") {
				t.Errorf("content: %q", res.Content)
			}
			if st.Get("sess-A") != intent.OverrideNone {
				t.Errorf("override should clear to OverrideNone, got %v", st.Get("sess-A"))
			}
		})
	}
}

func TestToolCacheCommand_Status(t *testing.T) {
	st := newToolCacheOverrideStore()
	h := toolCacheCommandHandler(st)

	// Default: auto.
	res, _ := h(context.Background(), "sess-A", "")
	if !strings.Contains(res.Content, "auto") {
		t.Errorf("default status should say auto; got %q", res.Content)
	}

	// Pinned on.
	st.Set("sess-A", intent.OverrideOn)
	res, _ = h(context.Background(), "sess-A", "")
	if !strings.Contains(res.Content, "on") {
		t.Errorf("status should reflect on pin; got %q", res.Content)
	}

	// Pinned off.
	st.Set("sess-A", intent.OverrideOff)
	res, _ = h(context.Background(), "sess-A", "")
	if !strings.Contains(res.Content, "off") {
		t.Errorf("status should reflect off pin; got %q", res.Content)
	}
}

func TestToolCacheCommand_UnknownArg(t *testing.T) {
	st := newToolCacheOverrideStore()
	h := toolCacheCommandHandler(st)

	res, err := h(context.Background(), "sess-A", "turn-on")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Action != "error" {
		t.Fatalf("unknown arg should return error action; got %q", res.Action)
	}
	if !strings.Contains(res.Content, "turn-on") {
		t.Errorf("error should echo unknown arg; got %q", res.Content)
	}
	// Pin must not change on invalid input.
	if st.Get("sess-A") != intent.OverrideNone {
		t.Errorf("pin should remain None on invalid arg; got %v", st.Get("sess-A"))
	}
}

func TestToolCacheCommand_NoActiveSession(t *testing.T) {
	st := newToolCacheOverrideStore()
	h := toolCacheCommandHandler(st)

	res, _ := h(context.Background(), "", "on")
	if res.Action != "error" {
		t.Fatalf("empty session should return error; got %+v", res)
	}
}

func TestToolCacheCommand_PerSessionIsolation(t *testing.T) {
	st := newToolCacheOverrideStore()
	h := toolCacheCommandHandler(st)

	_, _ = h(context.Background(), "sess-A", "on")
	_, _ = h(context.Background(), "sess-B", "off")

	if st.Get("sess-A") != intent.OverrideOn {
		t.Errorf("sess-A: got %v", st.Get("sess-A"))
	}
	if st.Get("sess-B") != intent.OverrideOff {
		t.Errorf("sess-B: got %v", st.Get("sess-B"))
	}
}
