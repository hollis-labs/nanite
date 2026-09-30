package harnessprofile

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed builtin/*.yaml
var builtinFS embed.FS

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// Profile is one named profile as authored.
type Profile struct {
	Name        string `json:"name" yaml:"name"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
	// Extends names a profile whose values this one starts from.
	Extends string `json:"extends,omitempty" yaml:"extends,omitempty"`
	Layer   `yaml:",inline"`
	// Models are per-model blocks keyed by a path.Match pattern over the model
	// id, such as "claude-opus-*". They sit above the profile's own values.
	Models map[string]Layer `json:"models,omitempty" yaml:"models,omitempty"`
}

// ParseProfile parses and validates one profile document. Unknown keys are an
// error, so a misspelled knob fails loudly instead of being ignored.
func ParseProfile(data []byte) (*Profile, error) {
	var p Profile
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("parse profile: %w", err)
	}
	if err := p.validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

func (p *Profile) validate() error {
	if !nameRE.MatchString(p.Name) {
		return fmt.Errorf("profile name %q: must match %s", p.Name, nameRE)
	}
	if p.Extends != "" && !nameRE.MatchString(p.Extends) {
		return fmt.Errorf("profile %q: extends %q is not a valid name", p.Name, p.Extends)
	}
	if p.Extends == p.Name {
		return fmt.Errorf("profile %q extends itself", p.Name)
	}
	if err := validateLayer(p.Layer); err != nil {
		return fmt.Errorf("profile %q: %w", p.Name, err)
	}
	for pattern, l := range p.Models {
		if _, err := path.Match(pattern, ""); err != nil {
			return fmt.Errorf("profile %q: model pattern %q: %w", p.Name, pattern, err)
		}
		if err := validateLayer(l); err != nil {
			return fmt.Errorf("profile %q model %q: %w", p.Name, pattern, err)
		}
	}
	return nil
}

// validateLayer rejects values that are not meaningful at all (negative, or a
// fraction outside (0,1]). Values that are meaningful but beyond what the host
// allows are not errors here; resolution clamps them and records it.
func validateLayer(l Layer) error {
	var errs []error
	neg := func(name string, bad bool) {
		if bad {
			errs = append(errs, fmt.Errorf("%s must not be negative", name))
		}
	}
	lim, h := l.Limits, l.Harness
	neg("limits.max_duration_ms", lim.MaxDurationMs != nil && *lim.MaxDurationMs < 0)
	neg("limits.idle_timeout_ms", lim.IdleTimeoutMs != nil && *lim.IdleTimeoutMs < 0)
	neg("limits.cost_budget", lim.CostBudget != nil && *lim.CostBudget < 0)
	neg("limits.token_budget", lim.TokenBudget != nil && *lim.TokenBudget < 0)
	neg("limits.max_turns", lim.MaxTurns != nil && *lim.MaxTurns < 0)
	neg("limits.max_retries", lim.MaxRetries != nil && *lim.MaxRetries < 0)
	neg("limits.tool_output_bytes", lim.ToolOutputBytes != nil && *lim.ToolOutputBytes < 0)
	neg("harness.subagent_idle_timeout_ms", h.SubagentIdleTimeoutMs != nil && *h.SubagentIdleTimeoutMs < 0)
	neg("harness.hard_ceiling", h.HardCeiling != nil && *h.HardCeiling < 0)
	neg("harness.consecutive_fail_cap", h.ConsecutiveFailCap != nil && *h.ConsecutiveFailCap < 0)
	neg("harness.runaway_fail_cap", h.RunawayFailCap != nil && *h.RunawayFailCap < 0)
	neg("harness.per_tool_cap", h.PerToolCap != nil && *h.PerToolCap < 0)
	neg("harness.compact_preview_bytes", h.CompactPreviewBytes != nil && *h.CompactPreviewBytes < 0)
	neg("harness.preview_min_bytes", h.PreviewMinBytes != nil && *h.PreviewMinBytes < 0)
	neg("harness.preview_max_bytes", h.PreviewMaxBytes != nil && *h.PreviewMaxBytes < 0)
	if h.PreviewPct != nil && (*h.PreviewPct <= 0 || *h.PreviewPct > 1) {
		errs = append(errs, errors.New("harness.preview_pct must be in (0, 1]"))
	}
	neg("harness.tool_output_min_bytes", h.ToolOutputMinBytes != nil && *h.ToolOutputMinBytes < 0)
	neg("harness.tool_output_max_bytes", h.ToolOutputMaxBytes != nil && *h.ToolOutputMaxBytes < 0)
	neg("harness.tool_output_remaining_floor_bytes", h.ToolOutputRemainingFloor != nil && *h.ToolOutputRemainingFloor < 0)
	if h.ToolOutputPct != nil && (*h.ToolOutputPct <= 0 || *h.ToolOutputPct > 1) {
		errs = append(errs, errors.New("harness.tool_output_pct must be in (0, 1]"))
	}
	if h.ToolOutputRemainingShare != nil && (*h.ToolOutputRemainingShare <= 0 || *h.ToolOutputRemainingShare > 1) {
		errs = append(errs, errors.New("harness.tool_output_remaining_share must be in (0, 1]"))
	}
	return errors.Join(errs...)
}

// modelPatterns returns the profile's model patterns matching model, least
// specific first so a more specific pattern is applied later and wins.
func (p *Profile) modelPatterns(model string) []string {
	if model == "" {
		return nil
	}
	var out []string
	for pattern := range p.Models {
		if ok, _ := path.Match(pattern, model); ok {
			out = append(out, pattern)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		li, lj := literalLen(out[i]), literalLen(out[j])
		if li != lj {
			return li < lj
		}
		return out[i] < out[j]
	})
	return out
}

func literalLen(pattern string) int {
	return len(pattern) - strings.Count(pattern, "*") - strings.Count(pattern, "?")
}

// digestChain hashes the full definition of a profile chain.
func digestChain(chain []*Profile) string {
	h := sha256.New()
	for _, p := range chain {
		raw, _ := json.Marshal(p) // map keys are sorted, so this is canonical
		h.Write(raw)
		h.Write([]byte{'\n'})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}
