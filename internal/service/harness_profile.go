package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/harnessprofile"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/truncate"
)

// Session metadata keys that select and tune a harness profile at launch.
const (
	// SessionProfileKey names the harness profile a session runs under.
	SessionProfileKey = "harness_profile"
	// SessionOverridesKey holds per-launch overrides in the profile layer shape
	// ({"limits": {...}, "harness": {...}}).
	SessionOverridesKey = "harness_overrides"
)

var (
	builtinRegistryOnce sync.Once
	builtinRegistry     *harnessprofile.Registry
)

// builtinOnlyRegistry is the registry used when none is configured, such as in
// tests: the embedded profiles only.
func builtinOnlyRegistry() *harnessprofile.Registry {
	builtinRegistryOnce.Do(func() {
		builtinRegistry, _ = harnessprofile.NewRegistry("", "")
	})
	return builtinRegistry
}

// SessionHarnessSelection reads the profile name and per-launch overrides from
// a session's metadata JSON. Empty metadata selects nothing.
func SessionHarnessSelection(metadata string) (profile string, overrides harnessprofile.Layer, err error) {
	if strings.TrimSpace(metadata) == "" {
		return "", overrides, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(metadata), &m); err != nil {
		// Unrelated malformed metadata must not break a turn.
		return "", overrides, nil //nolint:nilerr // deliberate: only the harness keys are ours to validate
	}
	if raw, ok := m[SessionProfileKey]; ok {
		if err := json.Unmarshal(raw, &profile); err != nil {
			return "", overrides, fmt.Errorf("session metadata %s: %w", SessionProfileKey, err)
		}
	}
	if raw, ok := m[SessionOverridesKey]; ok {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields() // a misspelled knob must fail, not be ignored
		if err := dec.Decode(&overrides); err != nil {
			return "", overrides, fmt.Errorf("session metadata %s: %w", SessionOverridesKey, err)
		}
	}
	return strings.TrimSpace(profile), overrides, nil
}

// resolveHarness resolves the harness profile for one turn. An unknown or
// invalid profile is an error; it is never silently replaced by a default.
func (s *chatServiceImpl) resolveHarness(ctx context.Context, session *store.Session, constraints chat.AgentConstraints, model string) (*harnessprofile.Resolved, error) {
	var settings harnessSettings
	if s.store != nil {
		settings = s.store
	}
	return ResolveHarness(ctx, s.harnessProfiles, settings, session, constraints, model)
}

// harnessSettings is the slice of the store the resolution reads.
type harnessSettings interface {
	GetUserSettings(ctx context.Context) (*store.UserSettings, error)
}

// ResolveHarness resolves the harness profile a session's next turn would run
// under: the session's selection, the agent's constraints, the user settings
// and the model. reg nil means the built-ins; settings nil means no app layer.
// The chat loop and the diagnostics endpoint both resolve through here.
func ResolveHarness(ctx context.Context, reg *harnessprofile.Registry, settings harnessSettings, session *store.Session, constraints chat.AgentConstraints, model string) (*harnessprofile.Resolved, error) {
	if reg == nil {
		reg = builtinOnlyRegistry()
	}
	profile, launch, err := SessionHarnessSelection(session.Metadata)
	if err != nil {
		return nil, err
	}
	in := harnessprofile.Inputs{
		Profile: profile,
		Model:   model,
		Agent:   agentLayer(constraints),
		Launch:  launch,
	}
	if settings != nil {
		if us, usErr := settings.GetUserSettings(ctx); usErr == nil && us != nil {
			if us.ToolPerTurnCap > 0 {
				perToolCap := us.ToolPerTurnCap
				in.AppSettings.Harness.PerToolCap = &perToolCap
			}
			// The tool-output ceiling's original ext setting is an app-settings
			// layer; 0 still disables the ceiling.
			if b, ok := extSettingBytes(us.ExtSettings["tool_turn_ceiling_bytes"]); ok {
				in.AppSettings.Limits.ToolOutputBytes = &b
			}
		}
	}
	res, err := reg.Resolve(in)
	if err != nil {
		return nil, fmt.Errorf("harness profile: %w", err)
	}
	return res, nil
}

// agentLayer turns an agent's stored constraints into the per-agent layer. A
// zero field states nothing, as before. An idle timeout applies to whichever
// window (interactive or subagent) governs the run.
func agentLayer(c chat.AgentConstraints) harnessprofile.Layer {
	var l harnessprofile.Layer
	if c.HardCeiling > 0 {
		v := c.HardCeiling
		l.Harness.HardCeiling = &v
	}
	if c.ConsecutiveFailCap > 0 {
		v := c.ConsecutiveFailCap
		l.Harness.ConsecutiveFailCap = &v
	}
	if c.RunawayFailCap > 0 {
		v := c.RunawayFailCap
		l.Harness.RunawayFailCap = &v
	}
	if c.IdleTimeoutSeconds > 0 {
		ms := int64(c.IdleTimeoutSeconds) * 1000
		l.Limits.IdleTimeoutMs = &ms
		sub := ms
		l.Harness.SubagentIdleTimeoutMs = &sub
	}
	return l
}

// applyHarness installs a resolved profile on a loop state.
func applyHarness(ls *loopState, r *harnessprofile.Resolved, subagent bool) {
	if r == nil {
		return
	}
	ls.harness = r
	v := r.Values
	ls.limits.hardCeiling = v.HardCeiling
	ls.limits.consecutiveFailCap = v.ConsecutiveFailCap
	ls.limits.runawayFailCap = v.RunawayFailCap
	ls.limits.idleTimeout = v.EffectiveIdleTimeout(subagent)
	ls.limits.defaultPerToolCap = v.PerToolCap
	ls.remainingTokens = -1
	ls.refreshTurnCeiling()
}

// refreshTurnCeiling recomputes the cumulative tool-output ceiling from the
// profile, the model's window and the context still available. It is called
// when the profile is applied and again each iteration, after the context budget
// is enforced.
func (ls *loopState) refreshTurnCeiling() {
	if ls == nil || ls.harness == nil {
		return
	}
	ls.turnResultCeiling = ls.harness.Values.TurnCeiling(ls.windowTokens, ls.remainingTokens)
}

// setRemainingFromBreakdown measures the remaining context from the iteration's
// token breakdown. With no breakdown there is no measurement, and the remaining
// context is unknown rather than whatever an earlier iteration left behind.
func (ls *loopState) setRemainingFromBreakdown(b *chat.TokenBreakdown) {
	if b == nil {
		ls.setRemainingContext(0, 0)
		return
	}
	ls.setRemainingContext(b.Ceiling, b.Total)
}

// consumeRemainingContext takes the bytes just delivered to the model out of
// the remaining context, and refreshes the ceiling. The next iteration's
// setRemainingContext replaces the estimate with a measurement.
func (ls *loopState) consumeRemainingContext(deliveredBytes int) {
	if ls == nil || ls.remainingTokens < 0 || deliveredBytes <= 0 {
		return
	}
	ls.remainingTokens = max(0, ls.remainingTokens-(deliveredBytes+3)/4)
	ls.refreshTurnCeiling()
}

// setRemainingContext records how many tokens of the loop's context ceiling are
// still free, and refreshes the ceiling. A ceiling of zero or less (unknown
// window) leaves the remaining context unknown.
func (ls *loopState) setRemainingContext(ceilingTokens, usedTokens int) {
	if ls == nil {
		return
	}
	ls.remainingTokens = -1
	if ceilingTokens > 0 {
		ls.remainingTokens = max(0, ceilingTokens-usedTokens)
	}
	ls.refreshTurnCeiling()
}

// previewBudget is the model-aware result-preview budget under the run's
// profile; with no profile it is truncate.BudgetForModel.
func (ls *loopState) previewBudget(model string) int {
	if ls == nil || ls.harness == nil {
		return truncate.BudgetForModel(model)
	}
	v := ls.harness.Values
	budget := truncate.BudgetForModelWith(model, v.PreviewPct, v.PreviewMinBytes, v.PreviewMaxBytes)
	// One result never exceeds what the remaining context can take, whatever the
	// cumulative ceiling is (or whether one is set at all).
	if limit := v.RemainingCap(ls.remainingTokens); limit >= 0 && budget > limit {
		budget = limit
	}
	return budget
}

// compactPreviewBudget is the step-down preview size once a turn's cumulative
// tool output passes its ceiling.
func (ls *loopState) compactPreviewBudget() int {
	if ls == nil || ls.harness == nil {
		return CompactPreviewBudgetBytes
	}
	v := ls.harness.Values
	compact := v.CompactPreviewBytes
	// The step-down size is a result too: a configured compact preview larger
	// than the context can take is limited like any other.
	if limit := v.RemainingCap(ls.remainingTokens); limit >= 0 && compact > limit {
		compact = limit
	}
	return compact
}

// ValidateHarnessSelection checks the profile name and overrides carried in a
// session's metadata, and the harness environment overrides, against reg (nil
// means the built-ins), so a session is rejected at creation instead of failing
// every turn. Overrides are validated by resolving them. With nothing selected
// the default profile is still resolved, so a bad environment value is caught.
func ValidateHarnessSelection(reg *harnessprofile.Registry, metadata string) error {
	profile, overrides, err := SessionHarnessSelection(metadata)
	if err != nil {
		return err
	}
	if reg == nil {
		reg = builtinOnlyRegistry()
	}
	// The process environment is resolved too: a NANITE_HARNESS_* value that
	// does not parse fails every turn, so it is refused here rather than
	// discovered on the first message.
	_, err = reg.Resolve(harnessprofile.Inputs{Profile: profile, Launch: overrides})
	return err
}

// MergeHarnessSelection returns metadata with the harness profile and
// overrides set. Existing unrelated keys are kept.
func MergeHarnessSelection(metadata, profile string, overrides map[string]any) (string, error) {
	m := map[string]any{}
	if strings.TrimSpace(metadata) != "" {
		if err := json.Unmarshal([]byte(metadata), &m); err != nil {
			return "", fmt.Errorf("session metadata is not a JSON object: %w", err)
		}
	}
	if profile != "" {
		m[SessionProfileKey] = profile
	}
	if len(overrides) > 0 {
		m[SessionOverridesKey] = overrides
	}
	out, err := json.Marshal(m)
	return string(out), err
}

// extSettingBytes reads a non-negative byte count from a user-settings ext
// value, which arrives as a JSON number (float64) or an int.
func extSettingBytes(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		if n >= 0 {
			return int64(n), true
		}
	case int:
		if n >= 0 {
			return int64(n), true
		}
	case int64:
		if n >= 0 {
			return n, true
		}
	}
	return 0, false
}
