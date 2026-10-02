package pluginapi

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/hollis-labs/plugin-sdk/manifest"
	sdkprocess "github.com/hollis-labs/plugin-sdk/subprocess"
)

const CapabilityReflexSeed = "reflex.seed"
const MaxReflexSeeds = 64

// ReflexSeed contributes an opt-out-able reminder for one explicit agent slug.
// It grants no halt, dispatch, schedule, tool enforcement or class-wide rule.
// IDs are plugin-local; the host retains ownership and durable firing history.
type ReflexSeed struct {
	ID        string          `json:"id"`
	AgentSlug string          `json:"agent_slug"`
	Trigger   ReflexPredicate `json:"trigger"`
	Reminder  string          `json:"reminder"`
	Priority  int             `json:"priority,omitempty"`
}

// ReflexPredicate is a bounded subset of host predicates evaluated inside the
// host. Plugins receive neither message text nor raw steering state from it.
type ReflexPredicate struct {
	Kind    string            `json:"kind"`
	Clauses []ReflexPredicate `json:"clauses,omitempty"`
	Window  int               `json:"window,omitempty"`
	Pattern string            `json:"pattern,omitempty"`
	Scope   string            `json:"scope,omitempty"`
	Mode    string            `json:"mode,omitempty"`
	Op      string            `json:"op,omitempty"`
	Value   int               `json:"value,omitempty"`
}

var agentSlug = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

func (seed ReflexSeed) Validate() error {
	if !slug.MatchString(seed.ID) || len(seed.ID) > 64 || !agentSlug.MatchString(seed.AgentSlug) || strings.TrimSpace(seed.Reminder) == "" || !utf8.ValidString(seed.Reminder) || len(seed.Reminder) > 8192 || strings.ContainsRune(seed.Reminder, 0) || seed.Priority < -100 || seed.Priority > 100 {
		return fmt.Errorf("pluginapi: invalid reflex seed")
	}
	return seed.Trigger.validate(0)
}

func (predicate ReflexPredicate) validate(depth int) error {
	if depth > 8 {
		return fmt.Errorf("pluginapi: reflex predicate exceeds nesting limit")
	}
	if predicate.Kind == "AND" || predicate.Kind == "OR" {
		if len(predicate.Clauses) == 0 || len(predicate.Clauses) > 16 || predicate.Window != 0 || predicate.Pattern != "" || predicate.Scope != "" || predicate.Mode != "" || predicate.Op != "" || predicate.Value != 0 {
			return fmt.Errorf("pluginapi: invalid reflex combinator")
		}
		for _, clause := range predicate.Clauses {
			if err := clause.validate(depth + 1); err != nil {
				return err
			}
		}
		return nil
	}
	if len(predicate.Clauses) != 0 || predicate.Window < 1 || predicate.Window > 20 {
		return fmt.Errorf("pluginapi: invalid reflex predicate window or clauses")
	}
	switch predicate.Kind {
	case "regex_match_window", "user_regex_window", "text_regex_window", "tool_name_window":
		if predicate.Pattern == "" || len(predicate.Pattern) > 4096 || predicate.Op != "" || predicate.Value != 0 {
			return fmt.Errorf("pluginapi: invalid reflex regex predicate")
		}
		if _, err := regexp.Compile(predicate.Pattern); err != nil {
			return fmt.Errorf("pluginapi: invalid reflex regex")
		}
		if predicate.Kind == "text_regex_window" {
			if predicate.Scope != "user" && predicate.Scope != "assistant" && predicate.Scope != "all" {
				return fmt.Errorf("pluginapi: invalid reflex text scope")
			}
		} else if predicate.Scope != "" {
			return fmt.Errorf("pluginapi: unexpected reflex text scope")
		}
		if predicate.Kind == "tool_name_window" {
			if predicate.Mode != "any" && predicate.Mode != "none" {
				return fmt.Errorf("pluginapi: invalid reflex tool match mode")
			}
		} else if predicate.Mode != "" {
			return fmt.Errorf("pluginapi: unexpected reflex match mode")
		}
	case "tool_calls_window", "cache_read_window", "input_tokens_window":
		if predicate.Pattern != "" || predicate.Scope != "" || predicate.Mode != "" || predicate.Value < 0 || predicate.Value > 1000000000 || !slices.Contains([]string{"=", "==", "!=", "<", "<=", ">", ">="}, predicate.Op) {
			return fmt.Errorf("pluginapi: invalid reflex numeric predicate")
		}
	default:
		return fmt.Errorf("pluginapi: unsupported reflex predicate kind")
	}
	return nil
}

// ReflexScope is the exact seed/agent set reviewed with the bundle. It does not
// authorize dynamic seeds or runtime changes to targets and authority tiers.
type ReflexScope struct {
	SeedIDs    []string `json:"seed_ids"`
	AgentSlugs []string `json:"agent_slugs"`
}

func DecodeReflexScope(raw json.RawMessage) (ReflexScope, error) {
	var scope ReflexScope
	if len(raw) == 0 || len(raw) > maxQueryScopeBytes {
		return scope, fmt.Errorf("pluginapi: reflex scope is absent or oversized")
	}
	if err := manifest.DecodeExtension(raw, &scope); err != nil {
		return scope, err
	}
	if len(scope.SeedIDs) == 0 || len(scope.SeedIDs) > MaxReflexSeeds || len(scope.AgentSlugs) == 0 || len(scope.AgentSlugs) > 32 {
		return scope, fmt.Errorf("pluginapi: invalid reflex scope size")
	}
	seen := map[string]bool{}
	for _, id := range scope.SeedIDs {
		if !slug.MatchString(id) || len(id) > 64 || seen[id] {
			return ReflexScope{}, fmt.Errorf("pluginapi: invalid or duplicate seed ID")
		}
		seen[id] = true
	}
	seen = map[string]bool{}
	for _, name := range scope.AgentSlugs {
		if !agentSlug.MatchString(name) || seen[name] {
			return ReflexScope{}, fmt.Errorf("pluginapi: invalid or duplicate agent slug")
		}
		seen[name] = true
	}
	return scope, nil
}

func ReflexScopeFor(block Block, requests []sdkprocess.CapabilityRequest) (ReflexScope, error) {
	var scope ReflexScope
	found := false
	for _, request := range requests {
		if request.Name != CapabilityReflexSeed {
			continue
		}
		if found || request.Optional {
			return scope, fmt.Errorf("pluginapi: reflex.seed requires one non-optional capability")
		}
		var err error
		scope, err = DecodeReflexScope(request.Metadata)
		if err != nil {
			return ReflexScope{}, err
		}
		found = true
	}
	seeds := block.Registers.ReflexSeeds
	if len(seeds) == 0 {
		if found {
			return scope, fmt.Errorf("pluginapi: reflex scope has no declarations")
		}
		return scope, nil
	}
	if err := block.Validate(); err != nil {
		return ReflexScope{}, err
	}
	if !found || len(scope.SeedIDs) != len(seeds) {
		return scope, fmt.Errorf("pluginapi: reflex declarations require matching reviewed scope")
	}
	agents := map[string]bool{}
	for _, seed := range seeds {
		if !slices.Contains(scope.SeedIDs, seed.ID) || !slices.Contains(scope.AgentSlugs, seed.AgentSlug) {
			return scope, fmt.Errorf("pluginapi: reflex seed exceeds reviewed scope")
		}
		agents[seed.AgentSlug] = true
	}
	if len(agents) != len(scope.AgentSlugs) {
		return scope, fmt.Errorf("pluginapi: unused reflex scope target")
	}
	return scope, nil
}
