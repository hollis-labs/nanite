package reactions

import (
	"encoding/json"
	"fmt"
)

// renderCardConfig is selftool_reactions.config's shape for a render_card
// row — {envelope_type, template} per docs/engineering/architecture/
// 11-harness-reactive-self-tools.md's worked-example section.
type renderCardConfig struct {
	EnvelopeType string         `json:"envelope_type"`
	Template     map[string]any `json:"template"`
}

// ResolveRenderCard substitutes payload's fields into configJSON's
// template (via substituteTemplate) and marshals the result into the
// envelope wire shape (`kind`, `version`, `type`, `data` — see
// docs/engineering/GLOSSARY.md's "Envelope" entry)
// internal/selftools/self_tools_transport.go's buildShowEnvelope already
// produces for card_show. Matching that existing shape byte-for-byte
// (rather than inventing a new one) is what lets
// TASKS/harness-reactive-self-tools/04-render-card-construction.md's
// marker-embedding helper hand this straight to the three existing,
// already marker-agnostic ENVELOPE_DATA consumers with zero changes to
// any of them.
//
// Deliberately pure: config + payload in, JSON out, no side effects, no
// store/HTTP dependency — 04 (or a test) can call this directly with
// nothing else from this package, per this task's "What to do" item 3.
// Does not build or embed the `<!--ENVELOPE_DATA:...-->` marker itself —
// that's 04's job, done by the calling self-tool handler.
func ResolveRenderCard(configJSON string, payload map[string]any) (json.RawMessage, error) {
	var cfg renderCardConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return nil, fmt.Errorf("render_card: parse config: %w", err)
	}
	if cfg.EnvelopeType == "" {
		return nil, fmt.Errorf("render_card: config missing envelope_type")
	}

	resolvedTemplate := substituteTemplate(cfg.Template, payload)
	data, ok := resolvedTemplate.(map[string]any)
	if !ok {
		// Unreachable in practice — substituteTemplate's map[string]any
		// case always returns a map[string]any for a map[string]any
		// input (renderCardConfig.Template is typed map[string]any, so
		// json.Unmarshal never hands it anything else). Defensive only,
		// so a resolved-but-typed-oddly template still produces valid
		// envelope JSON instead of a nil panic downstream.
		data = map[string]any{}
	}

	env := map[string]any{
		"kind":    "envelope",
		"version": 1,
		"type":    cfg.EnvelopeType,
		"data":    data,
	}

	out, err := json.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("render_card: marshal resolved envelope: %w", err)
	}
	return out, nil
}
