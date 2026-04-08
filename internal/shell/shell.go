// Package shell provides user-facing shell command execution for the chat
// interface. When a user types "!ls" or "!git status", the command is parsed,
// validated against a denylist, executed, and the output captured for injection
// into the conversation as a user message visible to the LLM.
package shell

import "strings"

// Mode controls how shell commands are approved before execution.
type Mode string

const (
	// ModeAsk requires explicit user confirmation for each command (default).
	ModeAsk Mode = "ask"
	// ModeSession auto-approves commands but the denylist remains active.
	ModeSession Mode = "session"
	// ModeYOLO disables the OS sandbox but keeps the denylist and env filtering active.
	ModeYOLO Mode = "yolo"
)

// ValidMode returns true if m is one of the three recognised modes.
func ValidMode(m string) bool {
	switch Mode(m) {
	case ModeAsk, ModeSession, ModeYOLO:
		return true
	}
	return false
}

// ParseCommand extracts the command string from a "!"-prefixed user message.
// Returns the raw command string and true if the message is a shell command,
// or ("", false) otherwise.
func ParseCommand(input string) (string, bool) {
	trimmed := strings.TrimSpace(input)
	if !strings.HasPrefix(trimmed, "!") {
		return "", false
	}
	cmd := strings.TrimSpace(trimmed[1:])
	if cmd == "" {
		return "", false
	}
	return cmd, true
}
