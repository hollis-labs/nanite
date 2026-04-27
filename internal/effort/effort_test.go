package effort_test

import (
	"context"
	"testing"

	"github.com/hollis-labs/nanite/internal/effort"
)

// TestEffort_String checks the canonical wire names.
func TestEffort_String(t *testing.T) {
	cases := []struct {
		e    effort.Effort
		want string
	}{
		{effort.EffortInvalid, "invalid"},
		{effort.EffortLow, "low"},
		{effort.EffortNormal, "normal"},
		{effort.EffortHigh, "high"},
		{effort.EffortMax, "max"},
	}
	for _, tc := range cases {
		if got := tc.e.String(); got != tc.want {
			t.Errorf("Effort(%d).String() = %q, want %q", tc.e, got, tc.want)
		}
	}
}

// TestEffort_IsValid confirms only the four defined levels are valid.
func TestEffort_IsValid(t *testing.T) {
	valid := []effort.Effort{effort.EffortLow, effort.EffortNormal, effort.EffortHigh, effort.EffortMax}
	for _, e := range valid {
		if !e.IsValid() {
			t.Errorf("%v.IsValid() = false, want true", e)
		}
	}
	invalid := []effort.Effort{effort.EffortInvalid, effort.Effort(99)}
	for _, e := range invalid {
		if e.IsValid() {
			t.Errorf("%v.IsValid() = true, want false", e)
		}
	}
}

// TestEffort_Default confirms the Default constant is EffortNormal.
func TestEffort_Default(t *testing.T) {
	if effort.Default != effort.EffortNormal {
		t.Errorf("Default = %v, want EffortNormal", effort.Default)
	}
}

// TestEffort_BudgetMultiplier verifies the multiplier table is consistent
// with the spec in CW-20260420-0014.
func TestEffort_BudgetMultiplier(t *testing.T) {
	cases := []struct {
		e    effort.Effort
		want float64
	}{
		{effort.EffortLow, 0.5},
		{effort.EffortNormal, 1.0},
		{effort.EffortHigh, 2.0},
		{effort.EffortMax, 4.0},
		// Unknown values must not zero-out the budget.
		{effort.EffortInvalid, 1.0},
		{effort.Effort(99), 1.0},
	}
	for _, tc := range cases {
		if got := tc.e.BudgetMultiplier(); got != tc.want {
			t.Errorf("%v.BudgetMultiplier() = %v, want %v", tc.e, got, tc.want)
		}
	}
}

// TestEffort_Parse checks round-trip fidelity and edge cases.
func TestEffort_Parse(t *testing.T) {
	cases := []struct {
		input string
		want  effort.Effort
	}{
		{"low", effort.EffortLow},
		{"Low", effort.EffortLow},
		{"LOW", effort.EffortLow},
		{"normal", effort.EffortNormal},
		{"", effort.EffortNormal}, // empty → default
		{"high", effort.EffortHigh},
		{"max", effort.EffortMax},
		{"unknown", effort.EffortInvalid},
		{"turbo", effort.EffortInvalid},
	}
	for _, tc := range cases {
		if got := effort.Parse(tc.input); got != tc.want {
			t.Errorf("Parse(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

// TestEffort_ReasoningCfg verifies on/off + budget split.
func TestEffort_ReasoningCfg(t *testing.T) {
	// Low and Normal must have reasoning off.
	for _, e := range []effort.Effort{effort.EffortLow, effort.EffortNormal} {
		cfg := e.ReasoningCfg()
		if cfg.Enabled {
			t.Errorf("%v.ReasoningCfg().Enabled = true, want false", e)
		}
		if cfg.BudgetTokens != 0 {
			t.Errorf("%v.ReasoningCfg().BudgetTokens = %d, want 0", e, cfg.BudgetTokens)
		}
	}

	// High must have reasoning on with a positive budget.
	highCfg := effort.EffortHigh.ReasoningCfg()
	if !highCfg.Enabled {
		t.Error("EffortHigh.ReasoningCfg().Enabled = false, want true")
	}
	if highCfg.BudgetTokens <= 0 {
		t.Errorf("EffortHigh.ReasoningCfg().BudgetTokens = %d, want > 0", highCfg.BudgetTokens)
	}

	// Max must have reasoning on with a budget larger than High.
	maxCfg := effort.EffortMax.ReasoningCfg()
	if !maxCfg.Enabled {
		t.Error("EffortMax.ReasoningCfg().Enabled = false, want true")
	}
	if maxCfg.BudgetTokens <= highCfg.BudgetTokens {
		t.Errorf("EffortMax.ReasoningCfg().BudgetTokens (%d) must exceed EffortHigh (%d)",
			maxCfg.BudgetTokens, highCfg.BudgetTokens)
	}
}

// TestEffort_ContextRoundTrip verifies WithContext / FromContext symmetry.
func TestEffort_ContextRoundTrip(t *testing.T) {
	for _, e := range []effort.Effort{effort.EffortLow, effort.EffortNormal, effort.EffortHigh, effort.EffortMax} {
		ctx := effort.WithContext(context.Background(), e)
		got := effort.FromContext(ctx)
		if got != e {
			t.Errorf("round-trip %v: FromContext got %v", e, got)
		}
	}
}

// TestEffort_FromContext_Default checks that a bare context returns Default.
func TestEffort_FromContext_Default(t *testing.T) {
	got := effort.FromContext(context.Background())
	if got != effort.Default {
		t.Errorf("FromContext(bare) = %v, want Default (%v)", got, effort.Default)
	}
}

// TestApplyToCeiling verifies the scale helper handles edge cases correctly.
func TestApplyToCeiling(t *testing.T) {
	cases := []struct {
		base int
		e    effort.Effort
		want int
	}{
		{100_000, effort.EffortNormal, 100_000}, // 1.0×
		{100_000, effort.EffortLow, 50_000},     // 0.5×
		{100_000, effort.EffortHigh, 200_000},   // 2.0×
		{100_000, effort.EffortMax, 400_000},    // 4.0×
		// Edge: zero base passes through unchanged.
		{0, effort.EffortMax, 0},
		// Edge: negative base passes through unchanged.
		{-1, effort.EffortMax, -1},
		// Edge: floor-of-1 prevents zeroing positive base.
		{1, effort.EffortLow, 1}, // 0.5 → rounds to 0 → clamped to 1
	}
	for _, tc := range cases {
		got := effort.ApplyToCeiling(tc.base, tc.e)
		if got != tc.want {
			t.Errorf("ApplyToCeiling(%d, %v) = %d, want %d", tc.base, tc.e, got, tc.want)
		}
	}
}
