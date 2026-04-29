package service

import (
	"fmt"
	"os"
	"strings"

	"github.com/hollis-labs/nanite/internal/chat"
)

// CW-20260429-0026: harness-side fallback for honest failure surfacing.
//
// When the assistant turn's tool_calls array contains any Status:"error"
// entries AND the assistant text doesn't already mention any of a small list
// of "honesty markers" (case-insensitive), append a footer note that lists
// the failure count and unique tool names that errored.
//
// Soft-rule precedent (insufficient): CW-20260429-0021 (prompt nudge to
// surface failures). The model keeps ignoring the rule even though the data
// is right there in tool_calls[].Status. This is the deterministic fallback.
//
// Toggleable via NANITE_HARNESS_FAILURE_FOOTER env var (default ON). Set to
// "0", "false", "off", or "no" (case-insensitive) to disable.
const failureFooterEnvVar = "NANITE_HARNESS_FAILURE_FOOTER"

// honestyMarkers is the case-insensitive substring list. If the response text
// contains ANY of these, the model is presumed to have already acknowledged
// the failure(s) and the footer is suppressed (idempotent).
var honestyMarkers = []string{
	"fail",
	"error",
	"rejected",
	"retry",
	"first attempt",
	"couldn't",
	"didn't work",
	"schema validation",
	"additional propert",
}

// failureFooterEnabled reports whether the footer injection is on. Default ON;
// recognized off-values (case-insensitive): "0", "false", "off", "no".
func failureFooterEnabled() bool {
	raw := strings.TrimSpace(os.Getenv(failureFooterEnvVar))
	if raw == "" {
		return true
	}
	switch strings.ToLower(raw) {
	case "0", "false", "off", "no":
		return false
	}
	return true
}

// maybeAppendFailureFooter appends a harness footer note to text when the
// per-turn tool_calls array contains any Status:"error" entries AND the text
// doesn't already include an honesty marker. Returns text unchanged otherwise.
//
// Tool name de-duplication preserves first-encounter order so the footer is
// stable across turns with identical error sets.
func maybeAppendFailureFooter(text string, refs []chat.ToolCallRef) string {
	if !failureFooterEnabled() {
		return text
	}

	// Step 1: collect unique error tool names in first-encounter order.
	var errorNames []string
	seen := make(map[string]bool)
	for _, ref := range refs {
		if ref.Status != "error" {
			continue
		}
		if seen[ref.Name] {
			continue
		}
		seen[ref.Name] = true
		errorNames = append(errorNames, ref.Name)
	}
	if len(errorNames) == 0 {
		return text
	}

	// Step 2: if text already contains an honesty marker, no-op.
	lower := strings.ToLower(text)
	for _, marker := range honestyMarkers {
		if strings.Contains(lower, marker) {
			return text
		}
	}

	// Step 3: append the footer. Total error-call count is the number of
	// error-status refs (NOT len(errorNames)) so repeat failures of the same
	// tool are reflected in the count, while the name list stays unique.
	errorCallCount := 0
	for _, ref := range refs {
		if ref.Status == "error" {
			errorCallCount++
		}
	}

	noun := "tool call"
	verb := "returned an error"
	if errorCallCount != 1 {
		noun = "tool calls"
		verb = "returned errors"
	}

	quoted := make([]string, len(errorNames))
	for i, n := range errorNames {
		quoted[i] = "`" + n + "`"
	}
	footer := fmt.Sprintf(
		"_(harness note: %d %s this turn %s before success — %s. See the tool_calls array for the full list.)_",
		errorCallCount, noun, verb, strings.Join(quoted, ", "),
	)

	// Separate the footer from the prior text with a blank line so it renders
	// cleanly in markdown regardless of whether the prior text ends in a
	// trailing newline.
	if text == "" {
		return footer
	}
	trimmed := strings.TrimRight(text, "\n")
	return trimmed + "\n\n" + footer
}
