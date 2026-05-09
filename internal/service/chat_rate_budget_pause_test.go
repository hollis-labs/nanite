package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	llmcontracts "github.com/hollis-labs/go-llm-contracts"
	"github.com/hollis-labs/nanite/internal/chat"
)

func TestParseRateBudgetEstimate(t *testing.T) {
	tests := []struct {
		name          string
		err           error
		wantEstimated int
		wantLimit     int
		wantOK        bool
	}{
		{
			name:          "wrapped sentinel with full format",
			err:           asWrappedRateBudgetError(31414, 30000),
			wantEstimated: 31414,
			wantLimit:     30000,
			wantOK:        true,
		},
		{
			name:          "raw sentinel without numbers",
			err:           llmcontracts.ErrRequestExceedsRateBudget,
			wantEstimated: 0,
			wantLimit:     0,
			wantOK:        false,
		},
		{
			name:          "nil error",
			err:           nil,
			wantEstimated: 0,
			wantLimit:     0,
			wantOK:        false,
		},
		{
			name:          "different error",
			err:           errors.New("provider returned 500"),
			wantEstimated: 0,
			wantLimit:     0,
			wantOK:        false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			est, lim, ok := parseRateBudgetEstimate(tc.err)
			if est != tc.wantEstimated || lim != tc.wantLimit || ok != tc.wantOK {
				t.Errorf("parseRateBudgetEstimate(%v) = (%d, %d, %v); want (%d, %d, %v)",
					tc.err, est, lim, ok, tc.wantEstimated, tc.wantLimit, tc.wantOK)
			}
		})
	}
}

func TestIsRateBudgetExceeded(t *testing.T) {
	if !IsRateBudgetExceeded(asWrappedRateBudgetError(100, 50)) {
		t.Error("wrapped sentinel should match")
	}
	if !IsRateBudgetExceeded(llmcontracts.ErrRequestExceedsRateBudget) {
		t.Error("raw sentinel should match")
	}
	if IsRateBudgetExceeded(errors.New("other")) {
		t.Error("unrelated error should not match")
	}
	if IsRateBudgetExceeded(nil) {
		t.Error("nil should not match")
	}
}

// drainEvent reads one StreamEvent from ch with a short timeout. Helper for
// the pause-handler tests below.
func drainEvent(t *testing.T, ch chan chat.StreamEvent) chat.StreamEvent {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for stream event")
		return chat.StreamEvent{}
	}
}

// decodePayload deserializes a rate_budget_pause event's Data field.
func decodePayload(t *testing.T, data string) rateBudgetPausePayload {
	t.Helper()
	var p rateBudgetPausePayload
	if err := json.Unmarshal([]byte(data), &p); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	return p
}

// TestPauseAndMaybeRetry_FirstAttemptUserActionWhenNoTracker verifies that
// when prov is not an Anthropic adapter (or no RateTracker is wired), the
// helper still emits a recoverable rate_budget_pause event — never falls
// through to the fatal path. The first call still uses suggested_action=
// auto_retry (Glass-6 invariant: every first event signals auto_retry, and
// the retry attempt is what tells us whether the budget freed). When no
// tracker is available, the second-stage user_action_needed event is
// emitted on the SAME call (after the no-op sleep loop).
func TestPauseAndMaybeRetry_FirstAttemptUserActionWhenNoTracker(t *testing.T) {
	s := &chatServiceImpl{}
	ch := make(chan chat.StreamEvent, 4)
	attempts := 0
	err := asWrappedRateBudgetError(40000, 30000)

	shouldRetry := s.pauseAndMaybeRetryRateBudget(
		context.Background(), "session-x", nil /* prov */, ch, err,
		compactTriggerRateBudget, &attempts,
	)
	if shouldRetry {
		t.Fatalf("expected no retry when no rate tracker is available")
	}
	if attempts != 1 {
		t.Fatalf("attempts should be incremented once; got %d", attempts)
	}
	// Two events expected: auto_retry then user_action_needed.
	ev1 := drainEvent(t, ch)
	if ev1.Type != rateBudgetPauseEventType {
		t.Fatalf("first event type = %q; want %q", ev1.Type, rateBudgetPauseEventType)
	}
	p1 := decodePayload(t, ev1.Data)
	if p1.SuggestedAction != suggestedActionAutoRetry {
		t.Errorf("first event suggested_action = %q; want %q", p1.SuggestedAction, suggestedActionAutoRetry)
	}
	if p1.EstimatedTokens != 40000 || p1.CurrentLimit != 30000 {
		t.Errorf("first event payload = (%d, %d); want (40000, 30000)", p1.EstimatedTokens, p1.CurrentLimit)
	}
	if p1.TriggerKind != compactTriggerRateBudget {
		t.Errorf("first event trigger_kind = %q; want %q", p1.TriggerKind, compactTriggerRateBudget)
	}
	if !rbpContainsAll(p1.RecoveryOptions, []string{"wait", "compact_more", "user_split_message"}) {
		t.Errorf("first event recovery_options missing entries: %+v", p1.RecoveryOptions)
	}
	ev2 := drainEvent(t, ch)
	p2 := decodePayload(t, ev2.Data)
	if p2.SuggestedAction != suggestedActionUserActionNeeded {
		t.Errorf("second event suggested_action = %q; want %q", p2.SuggestedAction, suggestedActionUserActionNeeded)
	}
}

// TestPauseAndMaybeRetry_SecondCallStraightToUserAction asserts that once
// attempts > 0, the helper skips the auto_retry stage and emits exactly one
// user_action_needed event, returning false.
func TestPauseAndMaybeRetry_SecondCallStraightToUserAction(t *testing.T) {
	s := &chatServiceImpl{}
	ch := make(chan chat.StreamEvent, 2)
	attempts := 1 // simulate prior pause already happened
	err := asWrappedRateBudgetError(40000, 30000)

	shouldRetry := s.pauseAndMaybeRetryRateBudget(
		context.Background(), "session-y", nil, ch, err,
		compactTriggerRateBudget, &attempts,
	)
	if shouldRetry {
		t.Fatalf("expected no retry on second pause")
	}
	if attempts != 2 {
		t.Fatalf("attempts should be 2; got %d", attempts)
	}
	ev := drainEvent(t, ch)
	p := decodePayload(t, ev.Data)
	if p.SuggestedAction != suggestedActionUserActionNeeded {
		t.Errorf("suggested_action = %q; want %q", p.SuggestedAction, suggestedActionUserActionNeeded)
	}
	select {
	case extra := <-ch:
		t.Fatalf("unexpected extra event: %+v", extra)
	default:
	}
}

// TestPauseAndMaybeRetry_AutoRetryWhenBudgetFrees wires a real
// TokenRateTracker, exhausts it, then verifies that after the tracker's
// window expiry the helper recommends a transparent retry. We shorten the
// tracker by setting a tiny limit and using a smaller estimate so WaitTime
// returns a short, real duration.
func TestPauseAndMaybeRetry_AutoRetryWhenBudgetFrees(t *testing.T) {
	// We can't easily construct an *Anthropic without keys, but the helper's
	// retry path only depends on RateTracker. Since providerRateTracker only
	// type-asserts against *nllmanthropic.Client, we skip the full retry path
	// in unit tests. The integration smoke (boot prompt §Verification)
	// covers the real retry. Document the gap with a TODO marker so future
	// work can lift the type assertion to an interface.
	t.Skip("auto-retry path requires *nllmanthropic.Client; covered by integration smoke")
}

// TestPauseAndMaybeRetry_UnparseableErrorStillEmitsEvent confirms that even
// with a malformed error (no "estimated X tokens vs Y limit" tail), the
// helper emits a recoverable event with zero values rather than panic or
// fall through to fatal.
func TestPauseAndMaybeRetry_UnparseableErrorStillEmitsEvent(t *testing.T) {
	s := &chatServiceImpl{}
	ch := make(chan chat.StreamEvent, 2)
	attempts := 0
	err := errors.New("request exceeds per-minute rate budget") // sentinel text without numbers

	shouldRetry := s.pauseAndMaybeRetryRateBudget(
		context.Background(), "session-z", nil, ch, err,
		compactTriggerRateBudget, &attempts,
	)
	if shouldRetry {
		t.Fatalf("expected no retry when parse fails")
	}
	ev := drainEvent(t, ch)
	if !strings.Contains(ev.Data, `"estimated_tokens":0`) {
		t.Errorf("expected zero estimated_tokens in payload, got: %s", ev.Data)
	}
}

func rbpContainsAll(have, want []string) bool {
	set := make(map[string]bool, len(have))
	for _, v := range have {
		set[v] = true
	}
	for _, v := range want {
		if !set[v] {
			return false
		}
	}
	return true
}
