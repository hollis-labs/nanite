package intent

import (
	"context"
	"regexp"
	"sort"
	"strings"
)

// DefaultRulesThreshold is the minimum category score for RulesClassifier to
// report hydrate=true on its own. See plan §D3 Layer 1.
const DefaultRulesThreshold = 0.6

// rule is a compiled keyword → category weight entry.
type rule struct {
	pattern  *regexp.Regexp
	category string
	weight   float64
}

// RulesClassifier scores categories via keyword matches against the user turn.
// Each category's score is the sum of matched rules' weights, clamped to 1.0.
// A category is hydrated when its score crosses Threshold.
type RulesClassifier struct {
	rules     []rule
	Threshold float64
}

// NewRulesClassifier returns a RulesClassifier with the default keyword set
// tuned for the category taxonomy in internal/tool (core-io, search, http,
// code-exec, agent, session, context, mode, mcp, other).
func NewRulesClassifier() *RulesClassifier {
	return &RulesClassifier{
		rules:     defaultRules(),
		Threshold: DefaultRulesThreshold,
	}
}

// WithRules replaces the rule set. For tests.
func (c *RulesClassifier) WithRules(rules []rule) *RulesClassifier {
	c.rules = rules
	return c
}

// Classify scores each available category by summing matched rule weights,
// then returns hydrate=true for any category whose score ≥ Threshold.
func (c *RulesClassifier) Classify(ctx context.Context, in Input) (Result, error) {
	text := strings.ToLower(in.UserTurn)
	available := toSet(in.AvailableCategories)

	scores := make(map[string]float64)
	matches := make(map[string][]string)
	for _, r := range c.rules {
		if _, ok := available[r.category]; !ok {
			continue
		}
		if loc := r.pattern.FindStringIndex(text); loc != nil {
			scores[r.category] += r.weight
			matches[r.category] = append(matches[r.category], r.pattern.String())
		}
	}

	var picked []string
	var confidence float64
	for cat, score := range scores {
		if score > 1.0 {
			score = 1.0
		}
		if score >= c.Threshold {
			picked = append(picked, cat)
			if score > confidence {
				confidence = score
			}
		} else if score > confidence {
			confidence = score
		}
	}
	sort.Strings(picked)

	if len(picked) == 0 {
		return Result{
			Hydrate:    false,
			Confidence: confidence,
			Source:     SourceRules,
			Reasoning:  "No keyword match crossed the hydration threshold.",
		}, nil
	}

	return Result{
		Hydrate:    true,
		Categories: picked,
		Confidence: confidence,
		Source:     SourceRules,
		Reasoning:  describeRuleMatch(picked, matches),
	}, nil
}

func describeRuleMatch(cats []string, matches map[string][]string) string {
	if len(cats) == 0 {
		return "No category crossed threshold."
	}
	parts := make([]string, 0, len(cats))
	for _, c := range cats {
		ms := matches[c]
		sort.Strings(ms)
		// Regex strings may look noisy; strip the common boundary anchors.
		// strings.Trim would munch stray '\' / 'b' chars, not the literal \b
		// substring — Copilot review #3095049844.
		pretty := make([]string, 0, len(ms))
		for _, m := range ms {
			clean := strings.TrimPrefix(m, `\b`)
			clean = strings.TrimSuffix(clean, `\b`)
			pretty = append(pretty, clean)
		}
		parts = append(parts, c+": "+strings.Join(pretty, ", "))
	}
	return "Keyword match — " + strings.Join(parts, "; ")
}

// defaultRules returns the baseline keyword set. Patterns use word boundaries
// so partial matches in long words don't trip the classifier.
func defaultRules() []rule {
	return []rule{
		// search
		mustRule(`\bsearch\b`, "search", 0.7),
		mustRule(`\bfind\b`, "search", 0.5),
		mustRule(`\blook ?up\b`, "search", 0.7),
		mustRule(`\bquery\b`, "search", 0.4),

		// code-exec
		mustRule(`\brun\b`, "code-exec", 0.7),
		mustRule(`\bexecute\b`, "code-exec", 0.8),
		mustRule(`\bexec\b`, "code-exec", 0.7),
		mustRule(`\bbash\b`, "code-exec", 0.8),
		mustRule(`\bshell\b`, "code-exec", 0.7),
		mustRule(`\bcommand\b`, "code-exec", 0.4),

		// core-io (file read/write)
		mustRule(`\bread\b`, "core-io", 0.6),
		mustRule(`\bshow\b`, "core-io", 0.4),
		mustRule(`\bcat\b`, "core-io", 0.6),
		mustRule(`\bview\b`, "core-io", 0.4),
		mustRule(`\bopen\b`, "core-io", 0.4),
		mustRule(`\bwrite\b`, "core-io", 0.6),
		mustRule(`\bsave\b`, "core-io", 0.5),
		mustRule(`\bedit\b`, "core-io", 0.7),
		mustRule(`\bmodify\b`, "core-io", 0.5),
		mustRule(`\bfile\b`, "core-io", 0.3),

		// http
		mustRule(`\bfetch\b`, "http", 0.7),
		mustRule(`\bcurl\b`, "http", 0.8),
		mustRule(`\bhttp(s)?\b`, "http", 0.6),
		// Text is lowercased before matching (see Classify), so these must be
		// lowercase too — Copilot review #3095049742.
		mustRule(`\bget\b|\bpost\b|\bput\b|\bdelete\b`, "http", 0.7),
		mustRule(`\burl\b`, "http", 0.3),

		// agent
		mustRule(`\bagent\b`, "agent", 0.5),
		mustRule(`\bdelegate\b`, "agent", 0.6),
		mustRule(`\bspawn\b`, "agent", 0.4),

		// session
		mustRule(`\bsessions?\b`, "session", 0.6),
		mustRule(`\bresume\b`, "session", 0.5),
		mustRule(`\bhistory\b`, "session", 0.4),

		// context
		mustRule(`\brecall\b`, "context", 0.7),
		mustRule(`\bremember\b`, "context", 0.5),
		mustRule(`\bmemory\b`, "context", 0.5),
		mustRule(`\bknowledge\b`, "context", 0.5),

		// mode
		mustRule(`\bmode\b`, "mode", 0.6),
		mustRule(`\bswitch\b`, "mode", 0.3),
	}
}

func mustRule(pat string, cat string, w float64) rule {
	re, err := regexp.Compile(pat)
	if err != nil {
		panic("intent: bad built-in rule pattern: " + pat)
	}
	return rule{pattern: re, category: cat, weight: w}
}

func toSet(xs []string) map[string]struct{} {
	out := make(map[string]struct{}, len(xs))
	for _, x := range xs {
		out[x] = struct{}{}
	}
	return out
}
