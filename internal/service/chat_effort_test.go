package service

// F1 (CW-20260420-0014) — Effort scalar wiring tests.
//
// These tests verify that:
//   - loopState stores and retrieves the Effort scalar correctly.
//   - Default (EffortNormal) is returned when no effort has been set.
//   - Budget multiplier and reasoning config are correctly derived from the
//     stored effort via the effort package.

import (
	"testing"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/effort"
)

// TestLoopState_Effort_Default ensures that an unset effort returns the default.
func TestLoopState_Effort_Default(t *testing.T) {
	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	if got := ls.Effort(); got != effort.Default {
		t.Errorf("unset Effort() = %v, want Default (%v)", got, effort.Default)
	}
}

// TestLoopState_SetEffort_RoundTrip verifies that every valid Effort level is
// stored and retrieved correctly.
func TestLoopState_SetEffort_RoundTrip(t *testing.T) {
	levels := []effort.Effort{
		effort.EffortLow,
		effort.EffortNormal,
		effort.EffortHigh,
		effort.EffortMax,
	}
	for _, e := range levels {
		ls := newLoopState(chat.AgentConstraints{}, nil, false)
		ls.SetEffort(e)
		if got := ls.Effort(); got != e {
			t.Errorf("SetEffort(%v) → Effort() = %v, want %v", e, got, e)
		}
	}
}

// TestLoopState_SetEffort_Invalid confirms that an invalid effort is silently
// promoted to Default so downstream budget math never sees a bad value.
func TestLoopState_SetEffort_Invalid(t *testing.T) {
	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	ls.SetEffort(effort.EffortInvalid)
	if got := ls.Effort(); got != effort.Default {
		t.Errorf("SetEffort(Invalid) → Effort() = %v, want Default (%v)", got, effort.Default)
	}
	ls.SetEffort(effort.Effort(99))
	if got := ls.Effort(); got != effort.Default {
		t.Errorf("SetEffort(99) → Effort() = %v, want Default (%v)", got, effort.Default)
	}
}

// TestLoopState_Effort_BudgetMultiplierTable validates the multiplier table for
// every level that a budget seam consumer would use.
func TestLoopState_Effort_BudgetMultiplierTable(t *testing.T) {
	cases := []struct {
		e    effort.Effort
		mult float64
	}{
		{effort.EffortLow, 0.5},
		{effort.EffortNormal, 1.0},
		{effort.EffortHigh, 2.0},
		{effort.EffortMax, 4.0},
	}
	for _, tc := range cases {
		ls := newLoopState(chat.AgentConstraints{}, nil, false)
		ls.SetEffort(tc.e)
		if got := ls.Effort().BudgetMultiplier(); got != tc.mult {
			t.Errorf("Effort(%v).BudgetMultiplier() = %v, want %v", tc.e, got, tc.mult)
		}
	}
}

// TestLoopState_Effort_ReasoningCfgTable validates reasoning config for every
// level. Low/Normal must be off; High/Max must be on with increasing budgets.
func TestLoopState_Effort_ReasoningCfgTable(t *testing.T) {
	ls := newLoopState(chat.AgentConstraints{}, nil, false)

	// Low — reasoning off.
	ls.SetEffort(effort.EffortLow)
	if cfg := ls.Effort().ReasoningCfg(); cfg.Enabled {
		t.Error("EffortLow: ReasoningCfg.Enabled = true, want false")
	}

	// Normal — reasoning off.
	ls.SetEffort(effort.EffortNormal)
	if cfg := ls.Effort().ReasoningCfg(); cfg.Enabled {
		t.Error("EffortNormal: ReasoningCfg.Enabled = true, want false")
	}

	// High — reasoning on, positive budget.
	ls.SetEffort(effort.EffortHigh)
	highCfg := ls.Effort().ReasoningCfg()
	if !highCfg.Enabled {
		t.Error("EffortHigh: ReasoningCfg.Enabled = false, want true")
	}
	if highCfg.BudgetTokens <= 0 {
		t.Errorf("EffortHigh: ReasoningCfg.BudgetTokens = %d, want > 0", highCfg.BudgetTokens)
	}

	// Max — reasoning on, budget larger than High.
	ls.SetEffort(effort.EffortMax)
	maxCfg := ls.Effort().ReasoningCfg()
	if !maxCfg.Enabled {
		t.Error("EffortMax: ReasoningCfg.Enabled = false, want true")
	}
	if maxCfg.BudgetTokens <= highCfg.BudgetTokens {
		t.Errorf("EffortMax BudgetTokens (%d) must exceed EffortHigh (%d)",
			maxCfg.BudgetTokens, highCfg.BudgetTokens)
	}
}

// TestApplyToCeiling_WithLoopStateEffort is an integration gate that verifies
// the budget seam — the pattern used in chat_generate.go.
// It mirrors the actual code path: read effort from loopState, apply to ceiling.
func TestApplyToCeiling_WithLoopStateEffort(t *testing.T) {
	const base = 160_000 // 80% of 200K — a realistic HardCeiling value

	cases := []struct {
		e    effort.Effort
		want int
	}{
		{effort.EffortLow, 80_000},     // 0.5×
		{effort.EffortNormal, 160_000}, // 1.0×
		{effort.EffortHigh, 320_000},   // 2.0×
		{effort.EffortMax, 640_000},    // 4.0×
	}
	for _, tc := range cases {
		ls := newLoopState(chat.AgentConstraints{}, nil, false)
		ls.SetEffort(tc.e)
		got := effort.ApplyToCeiling(base, ls.Effort())
		if got != tc.want {
			t.Errorf("effort=%v: ApplyToCeiling(%d) = %d, want %d",
				tc.e, base, got, tc.want)
		}
	}
}
