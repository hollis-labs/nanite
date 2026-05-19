package subagent

import (
	"context"
	"errors"
	"testing"
	"time"
)

// deadlineExpiredCtx returns a context whose deadline has already
// passed, so ctx.Err() reports context.DeadlineExceeded — the shape
// `execute`'s runCtx has when the wall-clock backstop fired.
func deadlineExpiredCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	t.Cleanup(cancel)
	// Touching Err() is enough; the deadline is already in the past.
	if ctx.Err() == nil {
		t.Fatal("expected expired context to report a non-nil Err")
	}
	return ctx
}

// progressResult is a partial Result whose result_json records N tool
// calls — the progress signal classifyRunOutcome reads.
func progressResult(calls int) *Result {
	return &Result{
		Summary: "did some work",
		ResultJSON: `{"partial":true,"summary":"did some work","tools":{"calls":` +
			itoa(calls) + `,"results_success":` + itoa(calls) + `,"results_error":0}}`,
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestClassifyRunOutcome_StalledSentinel(t *testing.T) {
	// A stalled stream is classified `stalled` regardless of whether the
	// run made earlier tool calls — the run still ended on silence.
	for _, calls := range []int{0, 5} {
		err := errors.Join(errors.New("stream error"), ErrStalled)
		got := classifyRunOutcome(err, progressResult(calls), context.Background())
		if got != StatusStalled {
			t.Errorf("calls=%d: got %q, want %q", calls, got, StatusStalled)
		}
	}
}

func TestClassifyRunOutcome_StalledWinsOverExpiredCtx(t *testing.T) {
	// Even when the run context is also expired, the explicit stall
	// sentinel takes precedence — it is the more specific signal.
	err := errors.Join(errors.New("stream error"), ErrStalled)
	got := classifyRunOutcome(err, progressResult(3), deadlineExpiredCtx(t))
	if got != StatusStalled {
		t.Errorf("got %q, want %q", got, StatusStalled)
	}
}

func TestClassifyRunOutcome_OverBudget_DeadlineWithProgress(t *testing.T) {
	// Wall-clock backstop fired (ctx expired) AND the run made progress
	// (non-zero tool calls) → over_budget, not failed.
	err := errors.New("http chat stream error: context deadline exceeded")
	got := classifyRunOutcome(err, progressResult(4), deadlineExpiredCtx(t))
	if got != StatusOverBudget {
		t.Errorf("got %q, want %q", got, StatusOverBudget)
	}
}

func TestClassifyRunOutcome_Stalled_DeadlineNoProgress(t *testing.T) {
	// Wall-clock backstop fired but the run made zero progress and never
	// tripped its own inactivity terminator — a silent/stuck run. The
	// taxonomy buckets a no-progress deadline as `stalled`.
	err := errors.New("http chat stream error: context deadline exceeded")
	got := classifyRunOutcome(err, progressResult(0), deadlineExpiredCtx(t))
	if got != StatusStalled {
		t.Errorf("got %q, want %q", got, StatusStalled)
	}
	// Same outcome when no partial result was captured at all.
	got = classifyRunOutcome(err, nil, deadlineExpiredCtx(t))
	if got != StatusStalled {
		t.Errorf("nil result: got %q, want %q", got, StatusStalled)
	}
}

func TestClassifyRunOutcome_Failed_GenericErrorLiveCtx(t *testing.T) {
	// A generic stream error with the run context still live (no
	// backstop, no stall) is a genuine failure — even if some tool calls
	// were made before the crash.
	err := errors.New("http chat stream error / cause:http_stream")
	got := classifyRunOutcome(err, progressResult(2), context.Background())
	if got != StatusFailed {
		t.Errorf("got %q, want %q", got, StatusFailed)
	}
}

func TestClassifyRunOutcome_Failed_FabricationTripLiveCtx(t *testing.T) {
	// The fabrication-detector trip surfaces as a plain error with a live
	// run context → failed (reserved for crashes + fabrication).
	err := errors.New("subagent: fabrication suspected — tools attempted but none succeeded")
	got := classifyRunOutcome(err, progressResult(3), context.Background())
	if got != StatusFailed {
		t.Errorf("got %q, want %q", got, StatusFailed)
	}
}

func TestClassifyRunOutcome_NilRunCtxTolerated(t *testing.T) {
	// Defensive: a nil runCtx must not panic. With no stall sentinel and
	// no expired ctx the outcome is `failed`.
	got := classifyRunOutcome(errors.New("boom"), nil, nil)
	if got != StatusFailed {
		t.Errorf("got %q, want %q", got, StatusFailed)
	}
}

func TestRunMadeProgress(t *testing.T) {
	cases := []struct {
		name   string
		result *Result
		want   bool
	}{
		{"nil result", nil, false},
		{"empty result_json", &Result{ResultJSON: ""}, false},
		{"no tools key", &Result{ResultJSON: `{"summary":"x"}`}, false},
		{"zero counts", &Result{ResultJSON: `{"tools":{"calls":0,"results_success":0,"results_error":0}}`}, false},
		{"non-zero calls", &Result{ResultJSON: `{"tools":{"calls":3}}`}, true},
		{"only results_success", &Result{ResultJSON: `{"tools":{"results_success":1}}`}, true},
		{"only results_error", &Result{ResultJSON: `{"tools":{"results_error":2}}`}, true},
		{"malformed json", &Result{ResultJSON: `{not json`}, false},
	}
	for _, c := range cases {
		if got := runMadeProgress(c.result); got != c.want {
			t.Errorf("%s: runMadeProgress = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestIsTerminalStatus(t *testing.T) {
	terminal := []string{
		StatusCompleted, StatusFailed, StatusOverBudget,
		StatusStalled, StatusCancelled, StatusRejected,
	}
	for _, s := range terminal {
		if !IsTerminalStatus(s) {
			t.Errorf("IsTerminalStatus(%q) = false, want true", s)
		}
	}
	for _, s := range []string{StatusRequested, StatusApproved, StatusRunning, ""} {
		if IsTerminalStatus(s) {
			t.Errorf("IsTerminalStatus(%q) = true, want false", s)
		}
	}
}
