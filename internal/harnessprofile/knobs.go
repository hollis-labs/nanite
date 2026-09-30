package harnessprofile

import (
	"encoding/json"
	"time"

	agentcontracts "github.com/hollis-labs/agent-contracts-leaf"
)

// Computed defaults. They reproduce the values the chat loop compiled in before
// profiles existed; a parity test in the service package pins that.
const (
	DefaultIdleTimeout         = 900 * time.Second
	DefaultSubagentIdleTimeout = 300 * time.Second
	DefaultHardCeiling         = 200
	DefaultConsecutiveFailCap  = 3
	DefaultRunawayFailCap      = 10
	DefaultPerToolCap          = 0 // 0 = no cap unless app settings supply one
	DefaultCompactPreviewBytes = 512
	DefaultPreviewPct          = 0.004 // fraction of the model window, in bytes (window tokens x 4)
	DefaultPreviewMinBytes     = 4000
	DefaultPreviewMaxBytes     = 32000

	// Tool-output ceiling (D-32): the cumulative bytes of tool output a turn may
	// deliver at full preview size before results step down to the compact
	// preview. A model's window scales it; the floor is the value the harness
	// used before it scaled, so a model with no window information behaves as it
	// always did.
	DefaultToolOutputPct            = 0.03
	DefaultToolOutputMinBytes       = 24 * 1024
	DefaultToolOutputMaxBytes       = 512 * 1024
	DefaultToolOutputRemainingShare = 0.25
	DefaultToolOutputRemainingFloor = 4 * 1024

	// DefaultMaxConcurrentTools bounds how many concurrent-safe tool calls of
	// one multi-tool-call turn run at once (CW-20260929-0012). Before it, a turn
	// that emitted N calls ran N at once.
	DefaultMaxConcurrentTools = 8

	// DefaultWriteClaimGuard is deny in normal operation; the dev profile
	// states warn.
	DefaultWriteClaimGuard = GuardDeny
)

// Knobs are the host-only harness knobs a profile may state. A nil field means
// "not stated in this layer".
type Knobs struct {
	SubagentIdleTimeoutMs *int64   `json:"subagent_idle_timeout_ms,omitempty" yaml:"subagent_idle_timeout_ms,omitempty"`
	HardCeiling           *int     `json:"hard_ceiling,omitempty" yaml:"hard_ceiling,omitempty"`
	ConsecutiveFailCap    *int     `json:"consecutive_fail_cap,omitempty" yaml:"consecutive_fail_cap,omitempty"`
	RunawayFailCap        *int     `json:"runaway_fail_cap,omitempty" yaml:"runaway_fail_cap,omitempty"`
	PerToolCap            *int     `json:"per_tool_cap,omitempty" yaml:"per_tool_cap,omitempty"`
	MaxConcurrentTools    *int     `json:"max_concurrent_tools,omitempty" yaml:"max_concurrent_tools,omitempty"`
	CompactPreviewBytes   *int     `json:"compact_preview_bytes,omitempty" yaml:"compact_preview_bytes,omitempty"`
	PreviewPct            *float64 `json:"preview_pct,omitempty" yaml:"preview_pct,omitempty"`
	PreviewMinBytes       *int     `json:"preview_min_bytes,omitempty" yaml:"preview_min_bytes,omitempty"`
	PreviewMaxBytes       *int     `json:"preview_max_bytes,omitempty" yaml:"preview_max_bytes,omitempty"`
	// Tool-output ceiling shaping. limits.tool_output_bytes states the ceiling
	// outright and replaces the window-scaled value.
	ToolOutputPct            *float64 `json:"tool_output_pct,omitempty" yaml:"tool_output_pct,omitempty"`
	ToolOutputMinBytes       *int     `json:"tool_output_min_bytes,omitempty" yaml:"tool_output_min_bytes,omitempty"`
	ToolOutputMaxBytes       *int     `json:"tool_output_max_bytes,omitempty" yaml:"tool_output_max_bytes,omitempty"`
	ToolOutputRemainingShare *float64 `json:"tool_output_remaining_share,omitempty" yaml:"tool_output_remaining_share,omitempty"`
	ToolOutputRemainingFloor *int     `json:"tool_output_remaining_floor_bytes,omitempty" yaml:"tool_output_remaining_floor_bytes,omitempty"`
}

// GuardMode is how strictly a hook-based guard acts.
type GuardMode string

// The guard modes. Deny blocks what the guard catches. Ask and Warn never
// block; Ask is a distinct recorded decision that a later release may turn into
// an operator prompt. Off disables the guard.
const (
	GuardOff  GuardMode = "off"
	GuardWarn GuardMode = "warn"
	GuardAsk  GuardMode = "ask"
	GuardDeny GuardMode = "deny"
)

// Valid reports whether m is one of the four modes.
func (m GuardMode) Valid() bool {
	switch m {
	case GuardOff, GuardWarn, GuardAsk, GuardDeny:
		return true
	}
	return false
}

// Hooks are the strictness settings of the harness's hook-based guards.
type Hooks struct {
	// WriteClaimGuard is the strictness of the guard that catches a reply
	// claiming a completed write, with an id, when no write tool succeeded.
	WriteClaimGuard *GuardMode `json:"write_claim_guard,omitempty" yaml:"write_claim_guard,omitempty"`
}

// Layer is what one configuration layer states: the shared limits shape plus
// the host-only knobs.
type Layer struct {
	Limits  agentcontracts.Limits `json:"limits,omitempty" yaml:"limits,omitempty"`
	Harness Knobs                 `json:"harness,omitempty" yaml:"harness,omitempty"`
	Hooks   Hooks                 `json:"hooks,omitempty" yaml:"hooks,omitempty"`
}

// Values are the resolved, concrete harness values for one run.
type Values struct {
	IdleTimeout         time.Duration
	SubagentIdleTimeout time.Duration
	HardCeiling         int
	ConsecutiveFailCap  int
	RunawayFailCap      int
	PerToolCap          int
	MaxConcurrentTools  int
	CompactPreviewBytes int
	PreviewPct          float64
	PreviewMinBytes     int
	PreviewMaxBytes     int

	// ToolOutputBytes is the explicit tool-output ceiling when one is stated
	// (limits.tool_output_bytes); nil means "scale it from the model". Zero
	// means no cumulative ceiling.
	ToolOutputBytes          *int64
	ToolOutputPct            float64
	ToolOutputMinBytes       int
	ToolOutputMaxBytes       int
	ToolOutputRemainingShare float64
	ToolOutputRemainingFloor int

	WriteClaimGuard GuardMode
}

// TurnCeiling is the cumulative tool-output ceiling for a turn, in bytes; 0
// means no cumulative ceiling. windowTokens is the model's context window, 0
// when unknown; remainingTokens is the context still available before the
// loop's own budget ceiling, negative when unknown.
//
// The base is the explicit tool_output_bytes when stated, else a share of the
// window (tokens x 4) clamped to [min, max]; an unknown window gives the min,
// which is the value the harness used before the ceiling scaled. The base is
// then limited to a share of what remains of the context, never below the
// remaining floor, so a nearly full context cannot be overrun.
func (v Values) TurnCeiling(windowTokens, remainingTokens int) int {
	var base int
	switch {
	case v.ToolOutputBytes != nil:
		if *v.ToolOutputBytes == 0 {
			return 0
		}
		base = int(min(*v.ToolOutputBytes, int64(maxToolOutputBytes)))
	case windowTokens <= 0:
		base = v.ToolOutputMinBytes
	default:
		base = int(float64(windowTokens*4) * v.ToolOutputPct)
		base = max(v.ToolOutputMinBytes, min(base, v.ToolOutputMaxBytes))
	}
	if limit := v.RemainingCap(remainingTokens); limit >= 0 {
		base = min(base, limit)
	}
	return base
}

// RemainingCap is the most tool output worth delivering given the context that
// remains: a share of the remaining bytes, never below the remaining floor.
// Negative when the remaining context is unknown. It also bounds a single
// result, so one result cannot overrun a nearly full context.
func (v Values) RemainingCap(remainingTokens int) int {
	if remainingTokens < 0 {
		return -1
	}
	return max(v.ToolOutputRemainingFloor, int(float64(remainingTokens*4)*v.ToolOutputRemainingShare))
}

// EffectiveIdleTimeout is the inactivity window for the run: the subagent
// window for a subagent dispatch, the interactive window otherwise.
func (v Values) EffectiveIdleTimeout(subagent bool) time.Duration {
	if subagent {
		return v.SubagentIdleTimeout
	}
	return v.IdleTimeout
}

// Source says which layer supplied a resolved value.
type Source struct {
	// Layer is "computed", "app-settings", "profile:<name>",
	// "profile:<name>/model:<pattern>", "agent", "launch" or "env:<VAR>".
	Layer string `json:"layer"`
	// Clamped is true when a host maximum changed the value the layer asked
	// for; Requested then holds what was asked.
	Clamped   bool   `json:"clamped,omitempty"`
	Requested string `json:"requested,omitempty"`
}

// Resolved is the outcome of resolving a profile for one run.
type Resolved struct {
	Profile string `json:"profile"`
	// Digest is a sha256 over the selected profile's full definition (its
	// extends chain and per-model blocks), so equal digests mean equal profile
	// content even when a file was edited in place.
	Digest  string            `json:"digest"`
	Model   string            `json:"model,omitempty"`
	Values  Values            `json:"-"`
	Sources map[string]Source `json:"sources"`
	// Limits carries the shared-contract limits after layering. The chat loop
	// enforces only idle_timeout_ms today; the rest (including
	// tool_output_bytes, whose consumer is the tool-output ceiling) are
	// recorded so a run's stated bounds are visible, and listed in Unenforced.
	Limits     agentcontracts.Limits `json:"limits"`
	Unenforced []string              `json:"unenforced,omitempty"`
}

// Effective is the recorded, self-describing form of a resolution: every
// enforced value under its knob name (durations in milliseconds), where each
// value came from, and the carried limits the loop does not yet enforce.
type Effective struct {
	Profile    string                `json:"profile"`
	Digest     string                `json:"digest"`
	Model      string                `json:"model,omitempty"`
	Values     map[string]any        `json:"values"`
	Sources    map[string]Source     `json:"sources"`
	Limits     agentcontracts.Limits `json:"limits"`
	Unenforced []string              `json:"unenforced,omitempty"`
}

// Effective returns the recorded form of r.
func (r *Resolved) Effective() Effective {
	v := r.Values
	eff := Effective{
		Profile: r.Profile,
		Digest:  r.Digest,
		Model:   r.Model,
		Values: map[string]any{
			"idle_timeout_ms":          v.IdleTimeout.Milliseconds(),
			"subagent_idle_timeout_ms": v.SubagentIdleTimeout.Milliseconds(),
			"hard_ceiling":             v.HardCeiling,
			"consecutive_fail_cap":     v.ConsecutiveFailCap,
			"runaway_fail_cap":         v.RunawayFailCap,
			"per_tool_cap":             v.PerToolCap,
			"max_concurrent_tools":     v.MaxConcurrentTools,
			"compact_preview_bytes":    v.CompactPreviewBytes,
			"preview_pct":              v.PreviewPct,
			"preview_min_bytes":        v.PreviewMinBytes,
			"preview_max_bytes":        v.PreviewMaxBytes,

			"tool_output_pct":                   v.ToolOutputPct,
			"tool_output_min_bytes":             v.ToolOutputMinBytes,
			"tool_output_max_bytes":             v.ToolOutputMaxBytes,
			"tool_output_remaining_share":       v.ToolOutputRemainingShare,
			"tool_output_remaining_floor_bytes": v.ToolOutputRemainingFloor,
			"write_claim_guard":                 string(v.WriteClaimGuard),
		},
		Sources:    r.Sources,
		Limits:     r.Limits,
		Unenforced: r.Unenforced,
	}
	if v.ToolOutputBytes != nil {
		eff.Values["tool_output_bytes"] = *v.ToolOutputBytes
	}
	return eff
}

// EffectiveJSON is Effective encoded as JSON, for storage.
func (r *Resolved) EffectiveJSON() string {
	raw, err := json.Marshal(r.Effective())
	if err != nil {
		return ""
	}
	return string(raw)
}
