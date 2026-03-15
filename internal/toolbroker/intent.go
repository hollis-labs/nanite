package toolbroker

import (
	"sort"
	"strings"

	"github.com/hollis-labs/conduit/internal/provider"
)

// MinIntentScore is the minimum keyword-overlap score a tool must reach
// to be considered relevant to an intent. Tools below this threshold are
// excluded from SelectByIntent results.
const MinIntentScore = 1

// SelectByIntent scores every registered tool against the intent string
// using simple keyword overlap (substring matches in tool name and
// description) and returns the top maxTools results sorted by relevance.
// If no tool scores above MinIntentScore, an empty slice is returned.
func (tb *ToolBroker) SelectByIntent(intent string, maxTools int) []provider.ToolDefinition {
	allTools := tb.ListTools()
	if len(allTools) == 0 || intent == "" {
		return nil
	}

	// Tokenise intent into lowercase words (3+ chars, no stop words).
	intentWords := tokeniseIntent(intent)
	if len(intentWords) == 0 {
		return nil
	}

	type scored struct {
		tool  provider.ToolDefinition
		score int
	}

	var candidates []scored
	for _, t := range allTools {
		s := scoreToolAgainstIntent(t, intentWords)
		if s >= MinIntentScore {
			candidates = append(candidates, scored{tool: t, score: s})
		}
	}

	if len(candidates) == 0 {
		return nil
	}

	// Sort descending by score.
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].score > candidates[j].score
	})

	if maxTools > 0 && len(candidates) > maxTools {
		candidates = candidates[:maxTools]
	}

	result := make([]provider.ToolDefinition, len(candidates))
	for i, c := range candidates {
		result[i] = c.tool
	}
	return result
}

// scoreToolAgainstIntent counts how many intent words appear as substrings
// in the tool's name or description (case-insensitive).
func scoreToolAgainstIntent(t provider.ToolDefinition, intentWords []string) int {
	nameLower := strings.ToLower(t.Name)
	descLower := strings.ToLower(t.Description)

	score := 0
	for _, w := range intentWords {
		if strings.Contains(nameLower, w) {
			score += 2 // name match is weighted higher
		}
		if strings.Contains(descLower, w) {
			score++
		}
	}
	return score
}

// tokeniseIntent splits an intent string into lowercase words of 3+ chars,
// filtering common stop words.
func tokeniseIntent(intent string) []string {
	raw := strings.ToLower(intent)
	for _, ch := range []string{",", ".", "!", "?", ";", ":", "'", "\"", "(", ")", "[", "]", "{", "}"} {
		raw = strings.ReplaceAll(raw, ch, " ")
	}

	var words []string
	seen := make(map[string]bool)
	for _, w := range strings.Fields(raw) {
		if len(w) < 3 {
			continue
		}
		if intentScoreStopWords[w] {
			continue
		}
		if seen[w] {
			continue
		}
		seen[w] = true
		words = append(words, w)
	}
	return words
}

// intentScoreStopWords used by tokeniseIntent for intent scoring.
var intentScoreStopWords = map[string]bool{
	"the": true, "and": true, "for": true, "are": true, "but": true,
	"not": true, "you": true, "all": true, "can": true, "her": true,
	"was": true, "one": true, "our": true, "out": true, "has": true,
	"its": true, "let": true, "get": true, "make": true, "like": true,
	"just": true, "want": true, "need": true, "from": true, "with": true,
	"this": true, "that": true, "have": true, "will": true, "what": true,
	"when": true, "how": true, "who": true, "which": true, "where": true,
	"why": true, "been": true, "being": true, "would": true, "could": true,
	"should": true, "about": true, "into": true, "some": true, "any": true,
}
