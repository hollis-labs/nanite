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
	reg := s.harnessProfiles
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
	if s.store != nil {
		if us, usErr := s.store.GetUserSettings(ctx); usErr == nil && us != nil && us.ToolPerTurnCap > 0 {
			perToolCap := us.ToolPerTurnCap
			in.AppSettings.Harness.PerToolCap = &perToolCap
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
}

// previewBudget is the model-aware result-preview budget under the run's
// profile; with no profile it is truncate.BudgetForModel.
func (ls *loopState) previewBudget(model string) int {
	if ls == nil || ls.harness == nil {
		return truncate.BudgetForModel(model)
	}
	v := ls.harness.Values
	return truncate.BudgetForModelWith(model, v.PreviewPct, v.PreviewMinBytes, v.PreviewMaxBytes)
}

// compactPreviewBudget is the step-down preview size once a turn's cumulative
// tool output passes its ceiling.
func (ls *loopState) compactPreviewBudget() int {
	if ls == nil || ls.harness == nil {
		return CompactPreviewBudgetBytes
	}
	return ls.harness.Values.CompactPreviewBytes
}

// ValidateHarnessSelection checks the profile name and overrides carried in a
// session's metadata against reg (nil means the built-ins), so a session is
// rejected at creation instead of failing every turn. Overrides are validated
// by resolving them.
func ValidateHarnessSelection(reg *harnessprofile.Registry, metadata string) error {
	profile, overrides, err := SessionHarnessSelection(metadata)
	if err != nil {
		return err
	}
	if profile == "" && overrides == (harnessprofile.Layer{}) {
		return nil
	}
	if reg == nil {
		reg = builtinOnlyRegistry()
	}
	_, err = reg.Resolve(harnessprofile.Inputs{Profile: profile, Launch: overrides, Getenv: func(string) (string, bool) { return "", false }})
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
