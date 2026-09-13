package chat

import (
	"regexp"
	"strings"
)

// PromissoryPreambleRecoveryNudge is the synthetic system prompt injected into the turn
// context when an assistant turn finishes at iteration 0 with promissory preamble text
// and no tool calls, despite tools being available.
const PromissoryPreambleRecoveryNudge = "[System note: You announced your intent to inspect sources or take action, but completed your turn without invoking any tools. If an investigation, check, or tool call is needed, invoke the tool directly now. Do not reply with conversational filler or repeat your plan — proceed with the tool call.]"

var promissoryPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(?:i['’]ll|i\s+will)\s+(?:first\s+)?(?:treat\s+this\s+as|check|inspect|look\s+into|investigate|search|recall|read|verify|pull|fetch|examine|explore|review|find|query|run|execute|start\s+by|begin\s+by)\b`),
	regexp.MustCompile(`(?i)\b(?:let\s+me|let['’]s)\s+(?:first\s+)?(?:check|inspect|look\s+into|investigate|search|recall|read|verify|pull|fetch|examine|explore|review|find|query|run|execute|start\s+by|begin\s+by)\b`),
	regexp.MustCompile(`(?i)\b(?:i['’]m|i\s+am)\s+going\s+to\s+(?:first\s+)?(?:check|inspect|look\s+into|investigate|search|recall|read|verify|pull|fetch|examine|explore|review|find|query|run|execute|start\s+by|begin\s+by)\b`),
	regexp.MustCompile(`(?i)\b(?:first,?\s+(?:i['’]ll|i\s+will|let\s+me))\b`),
}

// IsPromissoryPreamble evaluates whether a message content string looks like an unfulfilled
// promissory preamble (an announcement of planned tool work or intent) rather than an actual
// answer, deliverable, or refusal.
func IsPromissoryPreamble(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}

	// If the text is very long (> 1000 chars), it is much more likely an actual substantive answer
	// or deep explanation rather than a short conversational stall preamble.
	if len(trimmed) > 1000 {
		return false
	}

	// If the message contains code blocks (```), it delivered actual code or data artifacts.
	if strings.Contains(trimmed, "```") {
		return false
	}

	// If the message ends with a question, it is a clarification or user inquiry.
	if strings.HasSuffix(trimmed, "?") {
		return false
	}

	for _, pat := range promissoryPatterns {
		if pat.MatchString(trimmed) {
			return true
		}
	}

	return false
}
