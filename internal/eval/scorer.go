//go:build eval

package eval

import (
	"fmt"
	"strings"
)

// Score evaluates a model response against a scenario's rubric and returns a
// Result. Scoring is binary (1.0 / 0.0); all criteria must pass for a pass.
func Score(s Scenario, response string) Result {
	lower := strings.ToLower(response)
	res := Result{
		ScenarioID: s.ID,
		Benchmark:  s.Benchmark,
		Response:   truncate(response, 300),
	}

	var failures []string

	// --- AskedClarification ---
	if s.Rubric.AskedClarification != nil {
		want := *s.Rubric.AskedClarification
		got := looksLikeClarification(lower)
		if want && !got {
			failures = append(failures, "expected a clarifying question but response did not ask one")
		} else if !want && got {
			failures = append(failures, "expected no clarifying question but response appeared to ask one")
		}
	}

	// --- CalledTool ---
	if s.Rubric.CalledTool != "" {
		if !strings.Contains(lower, strings.ToLower(s.Rubric.CalledTool)) {
			failures = append(failures, fmt.Sprintf("expected tool %q to be called (not found in response)", s.Rubric.CalledTool))
		}
	}

	// --- AvoidedTools ---
	for _, tool := range s.Rubric.AvoidedTools {
		if strings.Contains(lower, strings.ToLower(tool)) {
			failures = append(failures, fmt.Sprintf("tool %q should not be called (found in response)", tool))
		}
	}

	// --- ContainsAny ---
	if len(s.Rubric.ContainsAny) > 0 {
		found := false
		for _, phrase := range s.Rubric.ContainsAny {
			if strings.Contains(lower, strings.ToLower(phrase)) {
				found = true
				break
			}
		}
		if !found {
			failures = append(failures, fmt.Sprintf("expected response to contain one of %v", s.Rubric.ContainsAny))
		}
	}

	// --- DoesNotContain ---
	for _, phrase := range s.Rubric.DoesNotContain {
		if strings.Contains(lower, strings.ToLower(phrase)) {
			failures = append(failures, fmt.Sprintf("response must not contain %q", phrase))
		}
	}

	if len(failures) == 0 {
		res.Passed = true
		res.Score = 1.0
	} else {
		res.Reason = strings.Join(failures, "; ")
	}
	return res
}

// looksLikeClarification heuristically detects whether a response contains a
// clarifying question. Looks for question marks near common clarification
// phrases. This is intentionally conservative — false negatives (missed
// questions) are preferred over false positives (hallucinated questions).
func looksLikeClarification(lower string) bool {
	markers := []string{
		"could you clarify",
		"can you clarify",
		"what do you mean",
		"which one",
		"which item",
		"what would you like",
		"what should i",
		"what are you referring to",
		"please clarify",
		"please specify",
		"could you specify",
		"can you specify",
		"what exactly",
		"did you mean",
	}
	for _, m := range markers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	// Fallback: contains a question mark and "what"/"which"/"who"/"when"/"where"
	if strings.Contains(lower, "?") {
		for _, qword := range []string{"what", "which", "who ", "when", "where"} {
			if strings.Contains(lower, qword) {
				return true
			}
		}
	}
	return false
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
