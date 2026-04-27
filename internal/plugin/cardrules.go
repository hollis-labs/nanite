package plugin

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// cardTypeRE mirrors the plugin.schema.v1 pattern for card_type values.
// Shared with the envelope type constraint — both must be lowercase slug form.
var cardTypeRE = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// CardRuleEntry is a compiled card detection rule held in the host registry.
// Rules are evaluated in insertion order within each tier; built-in rules run
// first (tier=0), plugin rules run after (tier=1). Within the same tier
// insertion order is preserved per the load sequence.
type CardRuleEntry struct {
	// CardType is the envelope type emitted when this rule matches.
	CardType string
	// PluginID is the plugin that registered this rule. Empty for built-in rules.
	PluginID string
	// Description is optional documentation text.
	Description string
	// tier controls built-in (0) vs plugin (1) precedence.
	tier int
	// pattern is the compiled regex, or nil when outputSchema is used instead.
	pattern *regexp.Regexp
	// outputSchema is the compiled JSON Schema, or nil when pattern is used.
	outputSchema *jsonschema.Schema
}

// Matches reports whether entry matches the given output text.
// For regex rules the full text is tested. For schema rules the text is parsed
// as JSON first; if parsing fails, the rule is skipped (does not match).
func (e *CardRuleEntry) Matches(output string) bool {
	if e.pattern != nil {
		return e.pattern.MatchString(output)
	}
	if e.outputSchema != nil {
		var doc any
		if err := json.Unmarshal([]byte(output), &doc); err != nil {
			return false
		}
		return e.outputSchema.Validate(doc) == nil
	}
	return false
}

// cardRulesRegistry is the host-side Stage 1 extension registry. Built-in
// rules are registered at startup (tier=0); plugin rules are registered at
// plugin load time (tier=1). Detection runs built-in rules first; the first
// match across both tiers wins.
//
// The registry is embedded in Host. External callers access it through the
// Host methods RegisterCardRule, UnregisterPluginCardRules, DetectCardType,
// and GetCardRules.
type cardRulesRegistry struct {
	mu      sync.RWMutex
	entries []CardRuleEntry // ordered: tier-0 first, tier-1 appended at load time
}

// registerBuiltin adds a built-in (tier=0) card rule. Called at host startup
// before plugins load. Built-in rules cannot be displaced by plugins.
func (r *cardRulesRegistry) registerBuiltin(entry CardRuleEntry) {
	entry.tier = 0
	r.mu.Lock()
	r.entries = append(r.entries, entry)
	r.mu.Unlock()
}

// registerPlugin adds a plugin-owned (tier=1) card rule. Returns an error when:
//   - card_type is empty or violates the ^[a-z][a-z0-9-]*$ pattern,
//   - a built-in rule already owns this card_type (collision with tier-0),
//   - another plugin already registered this card_type (cross-plugin collision).
func (r *cardRulesRegistry) registerPlugin(entry CardRuleEntry) error {
	if entry.CardType == "" {
		return fmt.Errorf("card rule card_type is required")
	}
	if !cardTypeRE.MatchString(entry.CardType) {
		return fmt.Errorf("card_type %q must match %s", entry.CardType, cardTypeRE)
	}
	entry.tier = 1

	r.mu.Lock()
	defer r.mu.Unlock()

	// Collision check: built-in wins; cross-plugin collision is also refused.
	for _, existing := range r.entries {
		if existing.CardType != entry.CardType {
			continue
		}
		if existing.tier == 0 {
			return fmt.Errorf("card_type %q is owned by a built-in rule; plugins cannot override built-ins", entry.CardType)
		}
		if existing.PluginID != entry.PluginID {
			return fmt.Errorf("card_type %q already registered by plugin %q (caller: %q)", entry.CardType, existing.PluginID, entry.PluginID)
		}
		// Same plugin re-registering the same type on reload — allow (idempotent).
		// Replace the existing entry rather than appending a duplicate.
		for i := range r.entries {
			if r.entries[i].CardType == entry.CardType && r.entries[i].PluginID == entry.PluginID {
				r.entries[i] = entry
				return nil
			}
		}
	}

	r.entries = append(r.entries, entry)
	return nil
}

// removeByPlugin removes all card rules owned by pluginID and returns the
// number removed. Safe to call with an empty pluginID (no-op).
func (r *cardRulesRegistry) removeByPlugin(pluginID string) int {
	if pluginID == "" {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	kept := r.entries[:0]
	n := 0
	for _, e := range r.entries {
		if e.PluginID == pluginID {
			n++
		} else {
			kept = append(kept, e)
		}
	}
	r.entries = kept
	return n
}

// detect returns the first matching CardType for the given output text.
// Built-in rules (tier=0) are evaluated before plugin rules (tier=1).
// Returns "" when no rule matches.
func (r *cardRulesRegistry) detect(output string) string {
	r.mu.RLock()
	entries := append([]CardRuleEntry(nil), r.entries...) // snapshot
	r.mu.RUnlock()

	for _, e := range entries {
		if e.Matches(output) {
			return e.CardType
		}
	}
	return ""
}

// snapshot returns a copy of the current entry list.
func (r *cardRulesRegistry) snapshot() []CardRuleEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]CardRuleEntry, len(r.entries))
	copy(out, r.entries)
	return out
}

// ---- Host surface ----

// RegisterCardRule adds a plugin-owned card detection rule. Called by
// applyManifestRegistrations during plugin load. Returns an error when the
// rule is invalid (bad card_type, collision with a built-in or another plugin).
func (h *Host) RegisterCardRule(entry CardRuleEntry) error {
	if err := h.cardRules.registerPlugin(entry); err != nil {
		return err
	}
	h.logger.Info("registered card rule", "card_type", entry.CardType, "plugin", entry.PluginID)
	return nil
}

// UnregisterPluginCardRules removes all card rules owned by pluginID.
// Called by UnloadPlugin during the hot-unload sweep. Returns the number removed.
func (h *Host) UnregisterPluginCardRules(pluginID string) int {
	n := h.cardRules.removeByPlugin(pluginID)
	if n > 0 {
		h.logger.Debug("plugin unload: removed card rules", "plugin", pluginID, "count", n)
	}
	return n
}

// DetectCardType evaluates Stage 1 card detection rules against output and
// returns the first matching card_type. Built-in rules run before plugin rules.
// Returns "" when no rule matches (caller falls back to default pipeline).
func (h *Host) DetectCardType(output string) string {
	return h.cardRules.detect(output)
}

// GetCardRules returns a snapshot of all registered card rules (built-in +
// plugin). Consumed by the /api/plugins/registry endpoint and diagnostic tooling.
func (h *Host) GetCardRules() []CardRuleEntry {
	return h.cardRules.snapshot()
}

// compileCardRule compiles a CardRuleRegistration from a plugin manifest into
// a CardRuleEntry. Either pattern or output_schema is compiled; the other is nil.
// Returns an error when the pattern is invalid regex or when the schema file
// cannot be loaded.
func compileCardRule(reg CardRuleRegistration, pluginID, pluginDir string) (CardRuleEntry, error) {
	entry := CardRuleEntry{
		CardType:    reg.CardType,
		PluginID:    pluginID,
		Description: reg.Description,
	}

	if reg.Pattern != "" && reg.OutputSchema != "" {
		return CardRuleEntry{}, fmt.Errorf("card rule for %q: pattern and output_schema are mutually exclusive", reg.CardType)
	}
	if reg.Pattern == "" && reg.OutputSchema == "" {
		return CardRuleEntry{}, fmt.Errorf("card rule for %q: exactly one of pattern or output_schema is required", reg.CardType)
	}

	if reg.Pattern != "" {
		re, err := regexp.Compile(reg.Pattern)
		if err != nil {
			return CardRuleEntry{}, fmt.Errorf("card rule for %q: invalid regex %q: %w", reg.CardType, reg.Pattern, err)
		}
		entry.pattern = re
		return entry, nil
	}

	// OutputSchema path — resolve and compile the JSON Schema.
	if pluginDir == "" {
		// No on-disk dir available (e.g. builtin without plugin dir) — skip schema
		// compilation and leave outputSchema nil. The rule will never match, but
		// it is registered so GetCardRules reflects the declaration. This mirrors
		// the behavior for envelope schema files in applyManifestRegistrations.
		return entry, nil
	}
	schemaFile, err := resolvePluginAssetPath(pluginDir, reg.OutputSchema)
	if err != nil {
		return CardRuleEntry{}, fmt.Errorf("card rule for %q: schema path %q rejected: %w", reg.CardType, reg.OutputSchema, err)
	}
	c := jsonschema.NewCompiler()
	schema, err := c.Compile(schemaFile)
	if err != nil {
		return CardRuleEntry{}, fmt.Errorf("card rule for %q: compile schema %q: %w", reg.CardType, schemaFile, err)
	}
	entry.outputSchema = schema
	return entry, nil
}
