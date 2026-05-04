package service

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
)

// devModeStore satisfies the chatServiceImpl.store surface needed by
// devModeEnabled. We only need GetUserSettings — the rest of Store is
// nil-able for this test seam.
type devModeStore struct {
	settings *store.UserSettings
	err      error
	Store    // embedded so unrelated methods exist (panic if called)
}

func (d *devModeStore) GetUserSettings() (*store.UserSettings, error) {
	return d.settings, d.err
}

// TestEmitChatLoopBudgetSoftWarning_DevModeOnViaEnv asserts the SSE
// envelope fires when NANITE_DEVMODE is truthy. Always-on slog INFO log
// fires regardless; this test exercises the SSE-stream gate. CW-20260504-0001.
func TestEmitChatLoopBudgetSoftWarning_DevModeOnViaEnv(t *testing.T) {
	prev, had := os.LookupEnv("NANITE_DEVMODE")
	t.Cleanup(func() {
		if had {
			os.Setenv("NANITE_DEVMODE", prev)
		} else {
			os.Unsetenv("NANITE_DEVMODE")
		}
	})
	os.Setenv("NANITE_DEVMODE", "1")

	s := &chatServiceImpl{}
	ch := make(chan chat.StreamEvent, 2)
	s.emitChatLoopBudgetSoftWarning("sess-x", 10, 10, ch)

	select {
	case evt := <-ch:
		if evt.Type != "plugin_envelope" {
			t.Fatalf("event type = %q, want plugin_envelope", evt.Type)
		}
		var wrap struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal([]byte(evt.Envelope), &wrap); err != nil {
			t.Fatalf("unmarshal wrap: %v", err)
		}
		if wrap.Type != chatLoopBudgetSoftWarningEnvelopeType {
			t.Errorf("envelope type = %q, want %q", wrap.Type, chatLoopBudgetSoftWarningEnvelopeType)
		}
		var payload chatLoopBudgetSoftWarningPayload
		if err := json.Unmarshal(wrap.Data, &payload); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		if payload.MaxTurns != 10 || payload.Iteration != 10 {
			t.Errorf("payload = %+v, want max_turns=10 iteration=10", payload)
		}
		if payload.Reason == "" || payload.Timestamp == "" {
			t.Errorf("payload missing reason/timestamp: %+v", payload)
		}
	default:
		t.Fatal("expected plugin_envelope when NANITE_DEVMODE=1; got nothing")
	}
}

// TestEmitChatLoopBudgetSoftWarning_DevModeOff asserts the SSE channel
// stays silent when devmode is off (env unset, store nil/false). The
// always-on slog INFO log is still emitted but isn't asserted here — the
// gate this test covers is the SSE noise path. CW-20260504-0001.
func TestEmitChatLoopBudgetSoftWarning_DevModeOff(t *testing.T) {
	prev, had := os.LookupEnv("NANITE_DEVMODE")
	t.Cleanup(func() {
		if had {
			os.Setenv("NANITE_DEVMODE", prev)
		} else {
			os.Unsetenv("NANITE_DEVMODE")
		}
	})
	os.Unsetenv("NANITE_DEVMODE")

	s := &chatServiceImpl{} // no store wired → falls through to env-only check
	ch := make(chan chat.StreamEvent, 2)
	s.emitChatLoopBudgetSoftWarning("sess-x", 10, 10, ch)

	select {
	case evt := <-ch:
		t.Fatalf("expected SSE silence in production; got event type=%q", evt.Type)
	default:
		// expected silence
	}
}

// TestEmitChatLoopBudgetSoftWarning_DevModeOnViaUserSettings asserts the
// store-backed fallback fires when the env var is unset but
// user_settings.developer_mode = true. CW-20260504-0001.
func TestEmitChatLoopBudgetSoftWarning_DevModeOnViaUserSettings(t *testing.T) {
	prev, had := os.LookupEnv("NANITE_DEVMODE")
	t.Cleanup(func() {
		if had {
			os.Setenv("NANITE_DEVMODE", prev)
		} else {
			os.Unsetenv("NANITE_DEVMODE")
		}
	})
	os.Unsetenv("NANITE_DEVMODE")

	s := &chatServiceImpl{
		store: &devModeStore{settings: &store.UserSettings{DeveloperMode: true}},
	}
	ch := make(chan chat.StreamEvent, 2)
	s.emitChatLoopBudgetSoftWarning("sess-x", 10, 10, ch)

	select {
	case evt := <-ch:
		if evt.Type != "plugin_envelope" {
			t.Fatalf("event type = %q, want plugin_envelope", evt.Type)
		}
	default:
		t.Fatal("expected plugin_envelope when user_settings.developer_mode=true; got nothing")
	}
}
