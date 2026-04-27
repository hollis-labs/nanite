// Package effort defines the F1 Effort scalar — a single dial that biases
// (a) the token budget allocator and (b) reasoning-block enablement.
//
// Effort is orthogonal to ScopeTier (internal/classify) which governs role
// selection, tool surface, and execution pattern. Effort does NOT influence
// model choice, turn count, or orchestration shape — see CW-20260420-0014.
//
// # Values
//
//	EffortLow    — 0.5× budget, reasoning off
//	EffortNormal — 1.0× budget, reasoning off (default)
//	EffortHigh   — 2.0× budget, reasoning on (8 000 token hint)
//	EffortMax    — 4.0× budget, reasoning on (20 000 token hint)
//
// # Consumer contract
//
// Budget seam — scale the existing token ceiling:
//
//	ceiling = effort.ApplyToCeiling(baseCeiling, e)
//
// Reasoning seam — retrieve on/off + intensity:
//
//	cfg := e.ReasoningCfg()
//	if cfg.Enabled { ... }
//
// F3 (interleaved thinking adoption, CW-20260420-0023) extends ReasoningConfig
// with provider-specific fields. Keep this package provider-agnostic.
package effort

import (
	"context"
	"strings"
)

// Effort is the single dial that biases budget allocation and reasoning.
// The zero value (0) is invalid; always use EffortNormal as the default.
type Effort int

const (
	// EffortInvalid is the zero sentinel. Callers must not pass this to
	// consumers; use EffortNormal when no explicit value is set.
	EffortInvalid Effort = iota

	// EffortLow conserves tokens — 0.5× budget, reasoning disabled.
	// Useful for quick/clarifying turns where depth isn't needed.
	EffortLow

	// EffortNormal is the default. 1.0× budget, reasoning disabled.
	EffortNormal

	// EffortHigh enables reasoning blocks at the default intensity
	// and doubles the token budget (2.0×).
	EffortHigh

	// EffortMax enables reasoning blocks at high intensity and
	// quadruples the token budget (4.0×).
	EffortMax
)

// Default is the Effort used when none is specified by the caller.
const Default = EffortNormal

// String returns the canonical lower-case wire name for an Effort level.
func (e Effort) String() string {
	switch e {
	case EffortLow:
		return "low"
	case EffortNormal:
		return "normal"
	case EffortHigh:
		return "high"
	case EffortMax:
		return "max"
	default:
		return "invalid"
	}
}

// IsValid reports whether e is a known, non-invalid Effort level.
func (e Effort) IsValid() bool {
	return e >= EffortLow && e <= EffortMax
}

// Parse converts a string produced by String() back to an Effort.
// Returns EffortInvalid for unknown values. Empty string → EffortNormal.
// Case-insensitive; leading/trailing space stripped.
func Parse(s string) Effort {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "low":
		return EffortLow
	case "normal", "":
		return EffortNormal
	case "high":
		return EffortHigh
	case "max":
		return EffortMax
	default:
		return EffortInvalid
	}
}

// BudgetMultiplier returns the floating-point multiplier applied to the
// existing token ceiling for this Effort level. The caller is responsible
// for clamping the result to provider limits.
//
//	low    → 0.5
//	normal → 1.0
//	high   → 2.0
//	max    → 4.0
//
// Unknown values return 1.0 to prevent accidental budget zeroing.
func (e Effort) BudgetMultiplier() float64 {
	switch e {
	case EffortLow:
		return 0.5
	case EffortNormal:
		return 1.0
	case EffortHigh:
		return 2.0
	case EffortMax:
		return 4.0
	default:
		return 1.0
	}
}

// ReasoningConfig holds the per-turn reasoning-block configuration derived
// from an Effort level.
//
// F3 (interleaved thinking adoption, CW-20260420-0023) extends this struct
// with provider-specific fields (e.g., the betas header). Keep this struct
// provider-agnostic so F3 can augment without breaking this package.
type ReasoningConfig struct {
	// Enabled reports whether reasoning/thinking blocks should be requested.
	Enabled bool

	// BudgetTokens is the token budget hint for the reasoning pass.
	// Only meaningful when Enabled is true. Providers that don't support
	// a budget hint ignore this field.
	//
	// 0 means "use the provider's default budget."
	BudgetTokens int
}

// reasoningBudgetHigh is the token budget for EffortHigh reasoning.
// 8 000 tokens — a thorough reasoning pass without dominating the 2× output budget.
const reasoningBudgetHigh = 8_000

// reasoningBudgetMax is the token budget for EffortMax reasoning.
// 20 000 tokens — intensive reasoning for hard problems.
const reasoningBudgetMax = 20_000

// ReasoningCfg returns the ReasoningConfig for this Effort level.
//
//	low, normal → Enabled=false, BudgetTokens=0
//	high        → Enabled=true,  BudgetTokens=8 000
//	max         → Enabled=true,  BudgetTokens=20 000
func (e Effort) ReasoningCfg() ReasoningConfig {
	switch e {
	case EffortHigh:
		return ReasoningConfig{Enabled: true, BudgetTokens: reasoningBudgetHigh}
	case EffortMax:
		return ReasoningConfig{Enabled: true, BudgetTokens: reasoningBudgetMax}
	default:
		return ReasoningConfig{Enabled: false}
	}
}

// ApplyToCeiling scales baseCeiling by e.BudgetMultiplier() and returns
// the result as an int. Non-positive inputs pass through unchanged.
// The result is always >= 1 for positive inputs.
//
// Usage:
//
//	budgetCeiling = effort.ApplyToCeiling(baseCeiling, e)
func ApplyToCeiling(baseCeiling int, e Effort) int {
	if baseCeiling <= 0 {
		return baseCeiling
	}
	result := int(float64(baseCeiling) * e.BudgetMultiplier())
	if result < 1 {
		return 1
	}
	return result
}

// --- Context transport ---

// ctxKey is the unexported key type used to store Effort in a context.Value.
type ctxKey struct{}

// WithContext returns a new context derived from ctx that carries e.
// Pass the resulting context into HandleMessage / generateResponse so the
// effort scalar is request-scoped, not global.
func WithContext(ctx context.Context, e Effort) context.Context {
	return context.WithValue(ctx, ctxKey{}, e)
}

// FromContext extracts the Effort stored in ctx by WithContext.
// Returns Default (EffortNormal) when no value is present or the stored
// value is not a valid Effort.
func FromContext(ctx context.Context) Effort {
	v, ok := ctx.Value(ctxKey{}).(Effort)
	if !ok || !v.IsValid() {
		return Default
	}
	return v
}
