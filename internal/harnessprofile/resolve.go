package harnessprofile

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	agentcontracts "github.com/hollis-labs/agent-contracts-leaf"
)

// Inputs are what one run resolves against.
type Inputs struct {
	// Profile selects a named profile; "" uses the registry default.
	Profile string
	// Model is the model id the run is using, for per-model blocks. "" matches
	// none.
	Model string
	// AppSettings, Agent and Launch are the layers the host derives from
	// user_settings, the agent's stored constraints and the launch request.
	AppSettings Layer
	Agent       Layer
	Launch      Layer
	// Getenv reads the environment; nil uses os.LookupEnv.
	Getenv func(string) (string, bool)
}

// Host maximums. A value outside [min, max] is moved to the nearest bound and
// the change is recorded on its Source.
const (
	// maxToolOutputBytes is the host maximum for any tool-output ceiling.
	maxToolOutputBytes = 64 << 20
	minDuration        = time.Second
	maxDuration        = 24 * time.Hour
)

type work struct {
	v       Values
	limits  agentcontracts.Limits
	sources map[string]Source
}

// Resolve resolves in against the registry.
func (r *Registry) Resolve(in Inputs) (*Resolved, error) {
	name := in.Profile
	if name == "" {
		name = r.defaultName
	}
	chain, err := r.chain(name)
	if err != nil {
		return nil, err
	}
	getenv := in.Getenv
	if getenv == nil {
		getenv = os.LookupEnv
	}
	for _, l := range []struct {
		name  string
		layer Layer
	}{{"app settings", in.AppSettings}, {"agent constraints", in.Agent}, {"launch overrides", in.Launch}} {
		if verr := validateLayer(l.layer); verr != nil {
			return nil, fmt.Errorf("%s: %w", l.name, verr)
		}
	}
	envLayers, err := envLayers(getenv)
	if err != nil {
		return nil, err
	}

	w := &work{sources: map[string]Source{}}
	w.setComputed()
	w.apply(in.AppSettings, "app-settings")
	for _, p := range chain {
		w.apply(p.Layer, "profile:"+p.Name)
	}
	for _, p := range chain {
		for _, pattern := range p.modelPatterns(in.Model) {
			w.apply(p.Models[pattern], "profile:"+p.Name+"/model:"+pattern)
		}
	}
	w.apply(in.Agent, "agent")
	w.apply(in.Launch, "launch")
	for _, el := range envLayers {
		w.apply(el.layer, "env:"+el.name)
	}
	w.clamp()

	res := &Resolved{
		Profile: name,
		Digest:  digestChain(chain),
		Model:   in.Model,
		Values:  w.v,
		Sources: w.sources,
		Limits:  w.limits,
	}
	res.Unenforced = unenforced(w.limits)
	return res, nil
}

func (w *work) setComputed() {
	w.v = Values{
		IdleTimeout:         DefaultIdleTimeout,
		SubagentIdleTimeout: DefaultSubagentIdleTimeout,
		HardCeiling:         DefaultHardCeiling,
		MaxConcurrentTools:  DefaultMaxConcurrentTools,
		ConsecutiveFailCap:  DefaultConsecutiveFailCap,
		RunawayFailCap:      DefaultRunawayFailCap,
		PerToolCap:          DefaultPerToolCap,
		CompactPreviewBytes: DefaultCompactPreviewBytes,
		PreviewPct:          DefaultPreviewPct,
		PreviewMinBytes:     DefaultPreviewMinBytes,
		PreviewMaxBytes:     DefaultPreviewMaxBytes,

		ToolOutputPct:            DefaultToolOutputPct,
		ToolOutputMinBytes:       DefaultToolOutputMinBytes,
		ToolOutputMaxBytes:       DefaultToolOutputMaxBytes,
		ToolOutputRemainingShare: DefaultToolOutputRemainingShare,
		ToolOutputRemainingFloor: DefaultToolOutputRemainingFloor,
	}
	for _, k := range []string{
		"idle_timeout_ms", "subagent_idle_timeout_ms", "hard_ceiling", "consecutive_fail_cap",
		"runaway_fail_cap", "per_tool_cap", "max_concurrent_tools", "compact_preview_bytes", "preview_pct",
		"preview_min_bytes", "preview_max_bytes", "tool_output_bytes", "tool_output_pct",
		"tool_output_min_bytes", "tool_output_max_bytes", "tool_output_remaining_share",
		"tool_output_remaining_floor_bytes",
	} {
		w.sources[k] = Source{Layer: "computed"}
	}
}

// apply overlays every knob l states and records layer as its source.
func (w *work) apply(l Layer, layer string) {
	src := func(key string) { w.sources[key] = Source{Layer: layer} }
	lim, h := l.Limits, l.Harness
	if lim.IdleTimeoutMs != nil {
		w.v.IdleTimeout = time.Duration(*lim.IdleTimeoutMs) * time.Millisecond
		src("idle_timeout_ms")
	}
	if h.SubagentIdleTimeoutMs != nil {
		w.v.SubagentIdleTimeout = time.Duration(*h.SubagentIdleTimeoutMs) * time.Millisecond
		src("subagent_idle_timeout_ms")
	}
	if h.HardCeiling != nil {
		w.v.HardCeiling = *h.HardCeiling
		src("hard_ceiling")
	}
	if h.MaxConcurrentTools != nil {
		w.v.MaxConcurrentTools = *h.MaxConcurrentTools
		src("max_concurrent_tools")
	}
	if h.ConsecutiveFailCap != nil {
		w.v.ConsecutiveFailCap = *h.ConsecutiveFailCap
		src("consecutive_fail_cap")
	}
	if h.RunawayFailCap != nil {
		w.v.RunawayFailCap = *h.RunawayFailCap
		src("runaway_fail_cap")
	}
	if h.PerToolCap != nil {
		w.v.PerToolCap = *h.PerToolCap
		src("per_tool_cap")
	}
	if h.CompactPreviewBytes != nil {
		w.v.CompactPreviewBytes = *h.CompactPreviewBytes
		src("compact_preview_bytes")
	}
	if h.PreviewPct != nil {
		w.v.PreviewPct = *h.PreviewPct
		src("preview_pct")
	}
	if h.PreviewMinBytes != nil {
		w.v.PreviewMinBytes = *h.PreviewMinBytes
		src("preview_min_bytes")
	}
	if h.PreviewMaxBytes != nil {
		w.v.PreviewMaxBytes = *h.PreviewMaxBytes
		src("preview_max_bytes")
	}
	if h.ToolOutputPct != nil {
		w.v.ToolOutputPct = *h.ToolOutputPct
		src("tool_output_pct")
	}
	if h.ToolOutputMinBytes != nil {
		w.v.ToolOutputMinBytes = *h.ToolOutputMinBytes
		src("tool_output_min_bytes")
	}
	if h.ToolOutputMaxBytes != nil {
		w.v.ToolOutputMaxBytes = *h.ToolOutputMaxBytes
		src("tool_output_max_bytes")
	}
	if h.ToolOutputRemainingShare != nil {
		w.v.ToolOutputRemainingShare = *h.ToolOutputRemainingShare
		src("tool_output_remaining_share")
	}
	if h.ToolOutputRemainingFloor != nil {
		w.v.ToolOutputRemainingFloor = *h.ToolOutputRemainingFloor
		src("tool_output_remaining_floor_bytes")
	}
	// The remaining shared limits are carried, not enforced by the chat loop.
	if lim.MaxDurationMs != nil {
		w.limits.MaxDurationMs = lim.MaxDurationMs
		src("max_duration_ms")
	}
	if lim.CostBudget != nil {
		w.limits.CostBudget = lim.CostBudget
		src("cost_budget")
	}
	if lim.TokenBudget != nil {
		w.limits.TokenBudget = lim.TokenBudget
		src("token_budget")
	}
	if lim.MaxTurns != nil {
		w.limits.MaxTurns = lim.MaxTurns
		src("max_turns")
	}
	if lim.MaxRetries != nil {
		w.limits.MaxRetries = lim.MaxRetries
		src("max_retries")
	}
	if lim.ToolOutputBytes != nil {
		v := *lim.ToolOutputBytes
		w.v.ToolOutputBytes = &v
		w.limits.ToolOutputBytes = &v
		src("tool_output_bytes")
	}
}

// clamp applies the host maximums to every value and records each change.
func (w *work) clamp() {
	clampDur := func(key string, d *time.Duration) {
		lo, hi := minDuration, maxDuration
		if *d >= lo && *d <= hi {
			return
		}
		w.markClamped(key, d.String())
		*d = max(lo, min(*d, hi))
	}
	clampInt := func(key string, v *int, lo, hi int) {
		if *v >= lo && *v <= hi {
			return
		}
		w.markClamped(key, strconv.Itoa(*v))
		*v = max(lo, min(*v, hi))
	}
	clampDur("idle_timeout_ms", &w.v.IdleTimeout)
	clampDur("subagent_idle_timeout_ms", &w.v.SubagentIdleTimeout)
	clampInt("hard_ceiling", &w.v.HardCeiling, 1, 10000)
	clampInt("max_concurrent_tools", &w.v.MaxConcurrentTools, 1, 64)
	clampInt("consecutive_fail_cap", &w.v.ConsecutiveFailCap, 1, 1000)
	clampInt("runaway_fail_cap", &w.v.RunawayFailCap, 1, 1000)
	clampInt("per_tool_cap", &w.v.PerToolCap, 0, 100000)
	clampInt("compact_preview_bytes", &w.v.CompactPreviewBytes, 64, 1<<20)
	clampInt("preview_min_bytes", &w.v.PreviewMinBytes, 256, 1<<20)
	clampInt("preview_max_bytes", &w.v.PreviewMaxBytes, 256, 4<<20)
	if w.v.PreviewPct < 0.0005 || w.v.PreviewPct > 0.05 {
		w.markClamped("preview_pct", strconv.FormatFloat(w.v.PreviewPct, 'g', -1, 64))
		w.v.PreviewPct = max(0.0005, min(w.v.PreviewPct, 0.05))
	}
	// Couplings: the terminal runaway cap may not sit below the soft warning
	// cap (the warning must be reachable), and the preview ceiling may not sit
	// below its floor.
	if w.v.RunawayFailCap < w.v.ConsecutiveFailCap {
		w.markClamped("runaway_fail_cap", strconv.Itoa(w.v.RunawayFailCap))
		w.v.RunawayFailCap = w.v.ConsecutiveFailCap
	}
	if b := w.v.ToolOutputBytes; b != nil && *b != 0 && (*b < 1024 || *b > maxToolOutputBytes) {
		w.markClamped("tool_output_bytes", strconv.FormatInt(*b, 10))
		c := max(int64(1024), min(*b, int64(maxToolOutputBytes)))
		w.v.ToolOutputBytes, w.limits.ToolOutputBytes = &c, &c
	}
	clampInt("tool_output_min_bytes", &w.v.ToolOutputMinBytes, 1024, maxToolOutputBytes)
	clampInt("tool_output_max_bytes", &w.v.ToolOutputMaxBytes, 1024, maxToolOutputBytes)
	clampInt("tool_output_remaining_floor_bytes", &w.v.ToolOutputRemainingFloor, 256, 1<<20)
	if w.v.ToolOutputPct < 0.0005 || w.v.ToolOutputPct > 0.5 {
		w.markClamped("tool_output_pct", strconv.FormatFloat(w.v.ToolOutputPct, 'g', -1, 64))
		w.v.ToolOutputPct = max(0.0005, min(w.v.ToolOutputPct, 0.5))
	}
	if w.v.ToolOutputRemainingShare < 0.01 || w.v.ToolOutputRemainingShare > 1 {
		w.markClamped("tool_output_remaining_share", strconv.FormatFloat(w.v.ToolOutputRemainingShare, 'g', -1, 64))
		w.v.ToolOutputRemainingShare = max(0.01, min(w.v.ToolOutputRemainingShare, 1))
	}
	if w.v.ToolOutputMaxBytes < w.v.ToolOutputMinBytes {
		w.markClamped("tool_output_max_bytes", strconv.Itoa(w.v.ToolOutputMaxBytes))
		w.v.ToolOutputMaxBytes = w.v.ToolOutputMinBytes
	}
	if w.v.PreviewMaxBytes < w.v.PreviewMinBytes {
		w.markClamped("preview_max_bytes", strconv.Itoa(w.v.PreviewMaxBytes))
		w.v.PreviewMaxBytes = w.v.PreviewMinBytes
	}
}

func (w *work) markClamped(key, requested string) {
	s := w.sources[key]
	s.Clamped, s.Requested = true, requested
	w.sources[key] = s
}

func unenforced(l agentcontracts.Limits) []string {
	var out []string
	if l.MaxDurationMs != nil {
		out = append(out, "max_duration_ms")
	}
	if l.CostBudget != nil {
		out = append(out, "cost_budget")
	}
	if l.TokenBudget != nil {
		out = append(out, "token_budget")
	}
	if l.MaxTurns != nil {
		out = append(out, "max_turns")
	}
	if l.MaxRetries != nil {
		out = append(out, "max_retries")
	}
	return out
}

type envLayer struct {
	name  string
	layer Layer
}

// envPrefix names the environment overrides: NANITE_HARNESS_<KNOB>.
const envPrefix = "NANITE_HARNESS_"

// LegacyToolCeilingEnv is the tool-output ceiling's environment variable from
// before profiles; it is an alias for NANITE_HARNESS_TOOL_OUTPUT_BYTES.
const LegacyToolCeilingEnv = "NANITE_TOOL_TURN_CEILING_BYTES"

// envLayers reads the environment overrides. A set but unparsable value is an
// error, never silently ignored. Layers are returned in a fixed order.
func envLayers(getenv func(string) (string, bool)) ([]envLayer, error) {
	var out []envLayer
	add := func(key string, set func(*Layer, string) error) error {
		name := envPrefix + strings.ToUpper(key)
		raw, ok := getenv(name)
		if !ok || strings.TrimSpace(raw) == "" {
			return nil
		}
		var l Layer
		if err := set(&l, strings.TrimSpace(raw)); err != nil {
			return fmt.Errorf("%s=%q: %w", name, raw, err)
		}
		if err := validateLayer(l); err != nil {
			return fmt.Errorf("%s=%q: %w", name, raw, err)
		}
		out = append(out, envLayer{name: name, layer: l})
		return nil
	}
	i64 := func(f func(*Layer) **int64) func(*Layer, string) error {
		return func(l *Layer, s string) error {
			v, err := strconv.ParseInt(s, 10, 64)
			if err != nil {
				return err
			}
			*f(l) = &v
			return nil
		}
	}
	num := func(f func(*Layer) **int) func(*Layer, string) error {
		return func(l *Layer, s string) error {
			v, err := strconv.Atoi(s)
			if err != nil {
				return err
			}
			*f(l) = &v
			return nil
		}
	}
	steps := []struct {
		key string
		set func(*Layer, string) error
	}{
		{"idle_timeout_ms", i64(func(l *Layer) **int64 { return &l.Limits.IdleTimeoutMs })},
		{"subagent_idle_timeout_ms", i64(func(l *Layer) **int64 { return &l.Harness.SubagentIdleTimeoutMs })},
		{"hard_ceiling", num(func(l *Layer) **int { return &l.Harness.HardCeiling })},
		{"max_concurrent_tools", num(func(l *Layer) **int { return &l.Harness.MaxConcurrentTools })},
		{"consecutive_fail_cap", num(func(l *Layer) **int { return &l.Harness.ConsecutiveFailCap })},
		{"runaway_fail_cap", num(func(l *Layer) **int { return &l.Harness.RunawayFailCap })},
		{"per_tool_cap", num(func(l *Layer) **int { return &l.Harness.PerToolCap })},
		{"compact_preview_bytes", num(func(l *Layer) **int { return &l.Harness.CompactPreviewBytes })},
		{"preview_min_bytes", num(func(l *Layer) **int { return &l.Harness.PreviewMinBytes })},
		{"preview_max_bytes", num(func(l *Layer) **int { return &l.Harness.PreviewMaxBytes })},
		{"preview_pct", func(l *Layer, s string) error {
			v, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return err
			}
			l.Harness.PreviewPct = &v
			return nil
		}},
		{"tool_output_bytes", i64(func(l *Layer) **int64 { return &l.Limits.ToolOutputBytes })},
		{"tool_output_min_bytes", num(func(l *Layer) **int { return &l.Harness.ToolOutputMinBytes })},
		{"tool_output_max_bytes", num(func(l *Layer) **int { return &l.Harness.ToolOutputMaxBytes })},
		{"tool_output_remaining_floor_bytes", num(func(l *Layer) **int { return &l.Harness.ToolOutputRemainingFloor })},
		{"tool_output_pct", func(l *Layer, s string) error {
			v, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return err
			}
			l.Harness.ToolOutputPct = &v
			return nil
		}},
		{"tool_output_remaining_share", func(l *Layer, s string) error {
			v, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return err
			}
			l.Harness.ToolOutputRemainingShare = &v
			return nil
		}},
	}
	// The tool-output ceiling's original variable keeps working. It is an
	// environment layer like the rest and is applied first, so the
	// NANITE_HARNESS_ name wins when both are set. As before, a value that does
	// not parse is ignored.
	if raw, ok := getenv(LegacyToolCeilingEnv); ok {
		if v, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64); err == nil && v >= 0 {
			var l Layer
			l.Limits.ToolOutputBytes = &v
			out = append(out, envLayer{name: LegacyToolCeilingEnv, layer: l})
		}
	}
	for _, st := range steps {
		if err := add(st.key, st.set); err != nil {
			return nil, err
		}
	}
	return out, nil
}
