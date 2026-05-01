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
// CW-20260501-0013: failure-footer enrichment. The footer now inlines the
// verbatim error reason (from ToolCallRef.ErrorReason) per failed tool. This
// raises the salience of the actual failure cause (e.g. "memory service not
// configured") at the spot the model is most likely to misread, addressing
// the H2 surface that produced the c121 "I don't have access to a memory
// recall tool" hallucination — without adding any system-prompt rules
// (lens-compliant per docs/architecture/agent-context-architecture.md).
//
// Toggleable via NANITE_HARNESS_FAILURE_FOOTER env var (default ON). Set to
// "0", "false", "off", or "no" (case-insensitive) to disable.
const failureFooterEnvVar = "NANITE_HARNESS_FAILURE_FOOTER"

// Cap constants for inlined error reasons (CW-20260501-0013).
//
//   - perErrorReasonCap: per-error truncation (UTF-8-safe). Truncated content
//     gets a trailing ellipsis. 200 picked because "memory service not
//     configured", "query is required", "additionalProperties not allowed:
//     description", and similar real-world reasons all fit comfortably under
//     it, while pathological tool errors that dump JSON or stack traces stay
//     bounded.
//   - totalFooterCap: budget for the entire footer string. 600 lets a
//     three-error turn list each tool with a healthy reason slice. Once we
//     hit the cap, remaining errors collapse to a count-only suffix instead
//     of being dropped silently.
const (
	perErrorReasonCap = 200
	totalFooterCap    = 600
)

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
// stable across turns with identical error sets. The first encounter's
// ErrorReason wins (subsequent failures of the same tool with different
// reasons collapse — they're already counted in the total but only one
// reason is shown).
//
// CW-20260501-0013: each listed tool now carries its inlined verbatim error
// reason, capped per perErrorReasonCap and the whole footer per totalFooterCap.
// When the cap is hit, remaining errors are summarized as "+N more" rather
// than dropped silently.
func maybeAppendFailureFooter(text string, refs []chat.ToolCallRef) string {
	if !failureFooterEnabled() {
		return text
	}

	// Step 1: collect unique error tool names + first-seen reason in
	// first-encounter order.
	type errorEntry struct {
		name   string
		reason string
	}
	var entries []errorEntry
	seen := make(map[string]bool)
	for _, ref := range refs {
		if ref.Status != "error" {
			continue
		}
		if seen[ref.Name] {
			continue
		}
		seen[ref.Name] = true
		entries = append(entries, errorEntry{name: ref.Name, reason: ref.ErrorReason})
	}
	if len(entries) == 0 {
		return text
	}

	// Step 2: if text already contains an honesty marker, no-op.
	lower := strings.ToLower(text)
	for _, marker := range honestyMarkers {
		if strings.Contains(lower, marker) {
			return text
		}
	}

	// Step 3: build the footer. Total error-call count is the number of
	// error-status refs (NOT len(entries)) so repeat failures of the same
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

	// Per-entry rendering: `tool_name` — "reason" (reason omitted entirely if
	// empty so the footer stays readable for older paths or wrapper code that
	// doesn't populate ErrorReason).
	prefix := fmt.Sprintf("_(harness note: %d %s this turn %s: ", errorCallCount, noun, verb)
	suffix := ". See the tool_calls array for the full list.)_"

	rendered := make([]string, 0, len(entries))
	dropped := 0
	for i, e := range entries {
		piece := renderEntry(e.name, e.reason)
		// Greedy total-budget enforcement: build the footer incrementally; if
		// adding this entry would push us over totalFooterCap (incl. prefix,
		// suffix, joiners, and the eventual "+N more" tail), stop and record
		// the rest as dropped.
		joinerLen := 0
		if len(rendered) > 0 {
			joinerLen = len(", ")
		}
		// Reserve space for ", +X more" tail in case more entries remain after
		// this one. We don't know X yet, so estimate generously (10 chars).
		tailReserve := 0
		remaining := len(entries) - i - 1
		if remaining > 0 {
			tailReserve = len(", +99 more") // upper bound on the tail string
		}
		projected := len(prefix) + lenJoined(rendered, ", ") + joinerLen + len(piece) + tailReserve + len(suffix)
		if projected > totalFooterCap && len(rendered) > 0 {
			dropped = len(entries) - i
			break
		}
		rendered = append(rendered, piece)
	}

	body := strings.Join(rendered, ", ")
	if dropped > 0 {
		body = body + fmt.Sprintf(", +%d more", dropped)
	}
	footer := prefix + body + suffix

	// Separate the footer from the prior text with a blank line so it renders
	// cleanly in markdown regardless of whether the prior text ends in a
	// trailing newline.
	if text == "" {
		return footer
	}
	trimmed := strings.TrimRight(text, "\n")
	return trimmed + "\n\n" + footer
}

// renderEntry formats one failed-tool entry for the footer. Reason is
// length-capped (UTF-8-safe via runes) and double-quoted; if the reason is
// empty the entry collapses to just the backticked tool name.
func renderEntry(name, reason string) string {
	reason = strings.TrimSpace(reason)
	// Collapse internal whitespace runs (incl. newlines) to single spaces so
	// multiline error messages don't blow up the footer's vertical layout.
	if reason != "" {
		reason = strings.Join(strings.Fields(reason), " ")
	}
	if reason == "" {
		return "`" + name + "`"
	}
	reason = truncateRunes(reason, perErrorReasonCap)
	return "`" + name + "` — \"" + reason + "\""
}

// truncateRunes returns s if its rune-length is <= max; otherwise it returns
// the first (max-1) runes followed by an ellipsis. UTF-8-safe.
func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max == 1 {
		return "…"
	}
	return string(runes[:max-1]) + "…"
}

// lenJoined returns the byte length of strings.Join(parts, sep) without
// allocating the joined string.
func lenJoined(parts []string, sep string) int {
	if len(parts) == 0 {
		return 0
	}
	n := len(sep) * (len(parts) - 1)
	for _, p := range parts {
		n += len(p)
	}
	return n
}
