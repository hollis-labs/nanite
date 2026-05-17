package chat

import (
	"encoding/json"
	"regexp"
	"strings"
	"sync"

	"github.com/hollis-labs/go-envelopes"
)

// envelopeRegistry is the shared go-envelopes Registry, set once at
// composition root via SetEnvelopeRegistry. P3 switches ValidateEnvelope
// to consult this registry directly; until then it's a reference-keeper
// alongside the existing registeredTypes bare-name index.
var (
	envelopeRegistryMu sync.RWMutex
	envelopeRegistry   *envelopes.Registry
)

// SetEnvelopeRegistry installs the shared registry used for envelope
// type lookup. Called once at startup from cmd/nanite/main.go after
// envelopes.LoadCore. Passing nil unsets — useful for tests that want
// to fall back to the legacy registeredTypes-only path.
func SetEnvelopeRegistry(r *envelopes.Registry) {
	envelopeRegistryMu.Lock()
	envelopeRegistry = r
	envelopeRegistryMu.Unlock()
}

// EnvelopeRegistry returns the currently-installed registry (may be nil).
// Exported so other packages can reach the registry through chat without
// re-importing go-envelopes at every call site.
func EnvelopeRegistry() *envelopes.Registry {
	envelopeRegistryMu.RLock()
	defer envelopeRegistryMu.RUnlock()
	return envelopeRegistry
}

// EnvelopeError describes a validation failure for an envelope block.
type EnvelopeError struct {
	Raw    string `json:"raw"`    // the raw text from the fenced block
	Reason string `json:"reason"` // "invalid_json", "missing_kind", "missing_version", "unregistered_type"
}

// registeredTypes is the set of envelope types the frontend can render.
// Populated at startup from config/envelopes.yaml via InitCoreTypes;
// plugins add entries at runtime via RegisterEnvelopeType.
// Protected by registeredTypesMu for concurrent access.
var (
	registeredTypesMu sync.RWMutex
	registeredTypes   = map[string]bool{}
)

// RegisterEnvelopeType adds a new envelope type to the registry at runtime.
// Plugins call this to register their envelope types so they pass validation.
func RegisterEnvelopeType(envelopeType string) {
	registeredTypesMu.Lock()
	registeredTypes[envelopeType] = true
	registeredTypesMu.Unlock()
}

// UnregisterEnvelopeType removes an envelope type from the registry. Used by
// the plugin host during UnloadPlugin to drop plugin-owned envelope types so
// stale validation entries don't survive a hot-unload. Safe to call for a
// type that was never registered (no-op).
func UnregisterEnvelopeType(envelopeType string) {
	registeredTypesMu.Lock()
	delete(registeredTypes, envelopeType)
	registeredTypesMu.Unlock()
}

// InitCoreTypes populates the registry with core envelope types loaded from
// the config/envelopes.yaml manifest. Called once at startup before plugins load.
func InitCoreTypes(types []string) {
	registeredTypesMu.Lock()
	for _, t := range types {
		registeredTypes[t] = true
	}
	registeredTypesMu.Unlock()
}

// ValidateEnvelope checks required fields on a parsed envelope.
// Returns nil if valid, or an EnvelopeError describing the issue.
func ValidateEnvelope(env Envelope, raw string) *EnvelopeError {
	if env.Kind == "" {
		return &EnvelopeError{Raw: raw, Reason: "missing_kind"}
	}
	if env.Version < 1 {
		return &EnvelopeError{Raw: raw, Reason: "missing_version"}
	}
	registeredTypesMu.RLock()
	registered := registeredTypes[env.Type]
	registeredTypesMu.RUnlock()
	if env.Type != "" && !registered {
		return &EnvelopeError{Raw: raw, Reason: "unregistered_type"}
	}
	return nil
}

// Envelope represents a structured envelope block embedded in assistant messages.
type Envelope struct {
	Kind     string `json:"kind"`
	Version  int    `json:"version"`
	Type     string `json:"type"`         // standard, nanite, custom
	ID       string `json:"id,omitempty"` // set by backend after CreateEnvelopeInstance
	Title    string `json:"title,omitempty"`
	Subtitle string `json:"subtitle,omitempty"`
	// Target is a visibility hint — the optional panel ID to OPEN when this
	// envelope arrives (J8 v1 — CW-20260426-0006). It does NOT control where
	// the envelope renders; it only signals "open this drawer". Known v1
	// values: "bottom_chat_drawer", "work", "workflows". Plugin-declared
	// drawers may introduce additional IDs. Omit to skip the visibility
	// signal entirely. Independent of RenderTarget — both can be set so an
	// envelope can render in one slot while opening a separate drawer.
	Target string `json:"target,omitempty"`
	// RenderTarget is the placement hint — the optional panel ID where the
	// envelope should RENDER (A2 — CW-20260428-0008). When set and the FE
	// dismiss-machine permits, the FE pushes the envelope into the named
	// panel's inbox slot and renders a stub in the chat transcript instead
	// of the full envelope. Empty = inline render in chat (the historical
	// default). The runtime stamps this from the per-type schema's
	// `default_render_target` when the agent did not provide one; explicit
	// agent override always wins. See docs/panels/envelope-render-target.md
	// for the full contract.
	RenderTarget string `json:"render_target,omitempty"`
	// RenderTargetBlocked carries a short reason code ("untrusted_plugin_panel",
	// "unknown_panel") when the backend rejected an explicit RenderTarget at
	// the tool boundary (e.g. an untrusted agent attempted to route to a
	// plugin panel). RenderTarget is dropped in that case and the envelope
	// falls back to inline; the FE surfaces this hint as a small debug pill
	// so the agent's blocked intent is visible.
	RenderTargetBlocked string `json:"render_target_blocked,omitempty"`
	// Mode is the optional mode/status signal carried alongside the envelope
	// (J8 v1 — CW-20260426-0006). When set, the frontend resolves the mode
	// against a preset map (see ui/src/lib/panel-modes.ts) and opens the
	// associated panels using the agent_opened state. v1 vocabulary: "planning"
	// → opens [work, workflows]. Empty/unknown modes are no-ops on the FE.
	// Mode is independent of Target — both can be set on the same envelope so
	// a card can route to one drawer while signaling a broader workspace mode.
	Mode string `json:"mode,omitempty"`
	// DisplayClass classifies whether the standalone plugin-envelope lane may
	// render this card. "content" stays attached to its turn/drawer; "alert"
	// and "action-required" are eligible for the transcript-level lane.
	DisplayClass string         `json:"display_class,omitempty"`
	Proposals    []Proposal     `json:"proposals,omitempty"`
	Questions    []Question     `json:"questions,omitempty"`
	Status       *Status        `json:"status,omitempty"`
	Data         map[string]any `json:"data,omitempty"` // custom payload for plugin envelopes
}

// Proposal represents a proposed action within an envelope.
type Proposal struct {
	Type    string         `json:"type"` // create_task, update_sprint, etc.
	Payload map[string]any `json:"payload"`
	Schema  map[string]any `json:"schema,omitempty"`
}

// Question represents an interactive question within an envelope.
type Question struct {
	Prompt       string   `json:"prompt"`
	Type         string   `json:"type"` // text, textarea, select, radio, checkbox
	Options      []string `json:"options,omitempty"`
	Required     bool     `json:"required"`
	Default      string   `json:"default,omitempty"`
	Description  string   `json:"description,omitempty"`
	DisplayStyle string   `json:"display_style,omitempty"`
}

// Status represents a progress status within an envelope.
type Status struct {
	Phase    string  `json:"phase"`
	Progress float64 `json:"progress"`
}

// buildKBEnvelope transforms a search_kb JSON result into a nanite-envelope JSON string.
// The search result is {"results":[...],"query":"...","total_results":N}.
// The envelope wraps it as {"kind":"envelope","version":1,"type":"kb-result","data":{...}}.
func BuildKBEnvelope(searchResult string) string {
	// The search result may have a trailing [SYSTEM: ...] instruction — strip it.
	jsonEnd := strings.LastIndex(searchResult, "}")
	if jsonEnd < 0 {
		return ""
	}
	jsonPart := searchResult[:jsonEnd+1]

	// Parse the search result to sanitize body fields.
	// Body content may contain triple backticks (markdown code fences)
	// which break the nanite-envelope fence delimiter.
	var parsed map[string]any
	if err := json.Unmarshal([]byte(jsonPart), &parsed); err != nil {
		return ""
	}

	// Sanitize body fields — replace ``` with ~~~ in article bodies
	// so they don't break the envelope fence.
	if results, ok := parsed["results"].([]any); ok {
		for _, r := range results {
			if article, ok := r.(map[string]any); ok {
				if body, ok := article["body"].(string); ok {
					article["body"] = strings.ReplaceAll(body, "```", "~~~")
				}
			}
		}
	}

	env := map[string]any{
		"kind":    "envelope",
		"version": 1,
		"type":    "kb-result",
		"data":    parsed,
	}
	data, err := json.Marshal(env)
	if err != nil {
		return ""
	}
	return string(data)
}

// envelopePattern matches fenced code blocks with volon-envelope or nanite-envelope language tags.
var envelopePattern = regexp.MustCompile("(?s)```(?:volon-envelope|nanite-envelope)\\s*\n(.*?)```")

// ParseEnvelopes extracts envelope blocks from assistant message content.
// Returns the parsed envelopes, the remaining text with envelope blocks removed,
// and any validation errors for malformed or invalid blocks.
func ParseEnvelopes(content string) ([]Envelope, string, []EnvelopeError) {
	matches := envelopePattern.FindAllStringSubmatchIndex(content, -1)
	if len(matches) == 0 {
		return nil, content, nil
	}

	var envelopes []Envelope
	var errors []EnvelopeError
	var cleaned strings.Builder
	lastEnd := 0

	for _, match := range matches {
		// match[0]:match[1] is the full match, match[2]:match[3] is the captured JSON group.
		cleaned.WriteString(content[lastEnd:match[0]])
		lastEnd = match[1]

		jsonContent := strings.TrimSpace(content[match[2]:match[3]])
		var env Envelope
		if err := json.Unmarshal([]byte(jsonContent), &env); err != nil {
			errors = append(errors, EnvelopeError{Raw: jsonContent, Reason: "invalid_json"})
			continue
		}
		if verr := ValidateEnvelope(env, jsonContent); verr != nil {
			errors = append(errors, *verr)
			// Still include the envelope if it parsed — only invalid_json is fatal.
			if verr.Reason != "invalid_json" {
				envelopes = append(envelopes, env)
			}
			continue
		}
		envelopes = append(envelopes, env)
	}
	cleaned.WriteString(content[lastEnd:])

	// Trim any extra whitespace left by removal.
	cleanedStr := strings.TrimSpace(cleaned.String())

	return envelopes, cleanedStr, errors
}
