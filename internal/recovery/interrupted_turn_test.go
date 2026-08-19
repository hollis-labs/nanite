package recovery

import "testing"

// TestDetectInterruptedTurn is the direct unit test for the pure decision
// core — internal/api/interrupted_turn_test.go covers the same behavior
// at the API/endpoint level (HTTP round-trip); this exercises the
// extracted logic in isolation.
func TestDetectInterruptedTurn(t *testing.T) {
	cases := []struct {
		name            string
		lastRole        string
		hasLiveStream   bool
		wantInterrupted bool
	}{
		{
			name:            "dangling user turn, no live stream -> interrupted",
			lastRole:        "user",
			hasLiveStream:   false,
			wantInterrupted: true,
		},
		{
			name:            "completed turn (last message assistant) -> not interrupted",
			lastRole:        "assistant",
			hasLiveStream:   false,
			wantInterrupted: false,
		},
		{
			name:            "dangling user turn but live stream present -> not interrupted",
			lastRole:        "user",
			hasLiveStream:   true,
			wantInterrupted: false,
		},
		{
			name:            "tool as last role -> not interrupted",
			lastRole:        "tool",
			hasLiveStream:   false,
			wantInterrupted: false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := DetectInterruptedTurn(c.lastRole, "msg-1", "2026-05-25T10:00:00Z", c.hasLiveStream)
			if c.wantInterrupted {
				if got == nil {
					t.Fatalf("DetectInterruptedTurn() = nil, want interrupted map")
				}
				if got["interrupted"] != true {
					t.Errorf("interrupted = %v, want true", got["interrupted"])
				}
				if got["reason"] != "service_restart" {
					t.Errorf("reason = %v, want service_restart", got["reason"])
				}
				if got["last_message_id"] != "msg-1" {
					t.Errorf("last_message_id = %v, want msg-1", got["last_message_id"])
				}
				if got["last_activity_at"] != "2026-05-25T10:00:00Z" {
					t.Errorf("last_activity_at = %v, want 2026-05-25T10:00:00Z", got["last_activity_at"])
				}
			} else if got != nil {
				t.Errorf("DetectInterruptedTurn() = %v, want nil", got)
			}
		})
	}
}
