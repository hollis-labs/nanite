package enrichment

import (
	"context"
	"fmt"
	"strings"
)

// ComposeOverrideBlock returns a markdown block suitable for appending to the
// system prompt when the listed tools are loaded for a turn. Tools without
// enrichment are skipped. Returns empty string when no tool has enrichment,
// the tools list is empty, or enricher is nil.
//
// Shape (when non-empty):
//
//	## Tool Overrides
//
//	Overrides apply to the specific tool named. Where an override conflicts
//	with a general rule, the override wins for that tool.
//
//	- **tool_a**: <compact hint summary>
//	- **tool_b**: <compact hint summary>
//
// Each per-tool line targets roughly 40 tokens. Preconditions, AntiPatterns,
// and OutputShape are joined into one line; ChainsWith is appended at the end
// only if present.
func ComposeOverrideBlock(ctx context.Context, toolNames []string, enr Enricher) (string, error) {
	if enr == nil || len(toolNames) == 0 {
		return "", nil
	}

	type enriched struct {
		name  string
		hints Hints
	}
	var rows []enriched
	for _, name := range toolNames {
		h, ok, err := enr.LookupByToolName(ctx, name)
		if err != nil {
			return "", fmt.Errorf("compose override block: %w", err)
		}
		if !ok || h.IsEmpty() {
			continue
		}
		rows = append(rows, enriched{name: name, hints: h})
	}
	if len(rows) == 0 {
		return "", nil
	}

	var b strings.Builder
	b.WriteString("## Tool Overrides\n\n")
	b.WriteString("Overrides apply to the specific tool named. Where an override conflicts with a general rule, the override wins for that tool.\n\n")
	for _, r := range rows {
		b.WriteString("- **")
		b.WriteString(r.name)
		b.WriteString("**: ")
		b.WriteString(summarizeHints(r.hints))
		b.WriteString("\n")
	}
	return b.String(), nil
}

// summarizeHints compacts a Hints struct to a one-line summary targeting ~40 tokens.
// Order: OutputShape → AntiPatterns → Preconditions → ChainsWith.
// Semicolons separate sections; commas separate items within a section.
func summarizeHints(h Hints) string {
	var parts []string
	if h.OutputShape != "" {
		parts = append(parts, h.OutputShape)
	}
	if len(h.AntiPatterns) > 0 {
		parts = append(parts, "anti-patterns: "+strings.Join(h.AntiPatterns, ", "))
	}
	if len(h.Preconditions) > 0 {
		parts = append(parts, "preconditions: "+strings.Join(h.Preconditions, ", "))
	}
	if len(h.ChainsWith) > 0 {
		parts = append(parts, "chains with: "+strings.Join(h.ChainsWith, ", "))
	}
	return strings.Join(parts, "; ")
}
