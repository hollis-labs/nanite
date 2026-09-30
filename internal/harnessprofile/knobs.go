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
)

// Knobs are the host-only harness knobs a profile may state. A nil field means
// "not stated in this layer".
type Knobs struct {
	SubagentIdleTimeoutMs *int64   `json:"subagent_idle_timeout_ms,omitempty" yaml:"subagent_idle_timeout_ms,omitempty"`
	HardCeiling           *int     `json:"hard_ceiling,omitempty" yaml:"hard_ceiling,omitempty"`
	ConsecutiveFailCap    *int     `json:"consecutive_fail_cap,omitempty" yaml:"consecutive_fail_cap,omitempty"`
	RunawayFailCap        *int     `json:"runaway_fail_cap,omitempty" yaml:"runaway_fail_cap,omitempty"`
	PerToolCap            *int     `json:"per_tool_cap,omitempty" yaml:"per_tool_cap,omitempty"`
	CompactPreviewBytes   *int     `json:"compact_preview_bytes,omitempty" yaml:"compact_preview_bytes,omitempty"`
	PreviewPct            *float64 `json:"preview_pct,omitempty" yaml:"preview_pct,omitempty"`
	PreviewMinBytes       *int     `json:"preview_min_bytes,omitempty" yaml:"preview_min_bytes,omitempty"`
	PreviewMaxBytes       *int     `json:"preview_max_bytes,omitempty" yaml:"preview_max_bytes,omitempty"`
}

// Layer is what one configuration layer states: the shared limits shape plus
// the host-only knobs.
type Layer struct {
	Limits  agentcontracts.Limits `json:"limits,omitempty" yaml:"limits,omitempty"`
	Harness Knobs                 `json:"harness,omitempty" yaml:"harness,omitempty"`
}

// Values are the resolved, concrete harness values for one run.
type Values struct {
	IdleTimeout         time.Duration
	SubagentIdleTimeout time.Duration
	HardCeiling         int
	ConsecutiveFailCap  int
	RunawayFailCap      int
	PerToolCap          int
	CompactPreviewBytes int
	PreviewPct          float64
	PreviewMinBytes     int
	PreviewMaxBytes     int
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
	return Effective{
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
			"compact_preview_bytes":    v.CompactPreviewBytes,
			"preview_pct":              v.PreviewPct,
			"preview_min_bytes":        v.PreviewMinBytes,
			"preview_max_bytes":        v.PreviewMaxBytes,
		},
		Sources:    r.Sources,
		Limits:     r.Limits,
		Unenforced: r.Unenforced,
	}
}

// EffectiveJSON is Effective encoded as JSON, for storage.
func (r *Resolved) EffectiveJSON() string {
	raw, err := json.Marshal(r.Effective())
	if err != nil {
		return ""
	}
	return string(raw)
}
