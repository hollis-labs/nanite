package chat

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hollis-labs/nanite/internal/toolclient"
)

// AgentConstraints holds parsed runtime constraints from AgentProfile.Constraints.
type AgentConstraints struct {
	MaxIterations  int `json:"max_iterations"`
	MaxTimeSeconds int `json:"max_time_seconds"`
	RetryBudget    int `json:"retry_budget"`

	// Phase 4 — Chat Loop Hardening.
	MaxTurns           int `json:"max_turns"`              // 0=default(25), -1=unlimited, >0=value
	HardCeiling        int `json:"hard_ceiling"`           // 0=default(100), absolute max turns
	ConsecutiveFailCap int `json:"consecutive_fail_cap"`   // 0=default(3), pause after N consecutive failures
	IdleTimeoutSeconds int `json:"idle_timeout_seconds"`   // 0=default(900), seconds of inactivity before suspend
}

// ParseAgentConstraints parses the constraints JSON from an agent profile.
// Returns zero-value struct on empty/invalid input (no constraints enforced).
func ParseAgentConstraints(raw string) AgentConstraints {
	var c AgentConstraints
	if raw == "" || raw == "{}" {
		return c
	}
	json.Unmarshal([]byte(raw), &c)
	return c
}

// BuildToolCatalog formats tool summaries as a compact catalog string for
// injection into the system prompt during progressive discovery.
func BuildToolCatalog(summaries []toolclient.ToolSummary) string {
	if len(summaries) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("Available tools (use request_tools to get full details):\n")
	for _, s := range summaries {
		desc := s.Description
		if len(desc) > 80 {
			desc = desc[:80] + "..."
		}
		fmt.Fprintf(&sb, "- %s: %s\n", s.Name, desc)
	}
	return sb.String()
}

// ToolWarningPayload is the JSON payload for tool_warning SSE events.
type ToolWarningPayload struct {
	ToolName          string `json:"tool_name"`
	Error             string `json:"error"`
	Iteration         int    `json:"iteration"`
	ConsecutiveErrors int    `json:"consecutive_errors"`
	Level             string `json:"level"` // "warning" or "critical"
}

// StreamEvent is the event sent to SSE clients.
type StreamEvent struct {
	Type            string     `json:"type"`                        // stream_start, delta, stream_end, error, tool_call, tool_result, status, circuit_open, session_takeover, tool_warning
	Content         string     `json:"content,omitempty"`
	MessageID       string     `json:"message_id,omitempty"`
	AgentID         string     `json:"agent_id,omitempty"`
	Usage           *Usage     `json:"usage,omitempty"`
	Error           string     `json:"error,omitempty"`
	StructuredError *ChatError `json:"structured_error,omitempty"`
	Tool            string     `json:"tool,omitempty"`              // tool name for tool_call/tool_result
	ToolID          string     `json:"tool_id,omitempty"`           // tool_use_id
	Summary         string     `json:"summary,omitempty"`           // tool result summary
	Envelope        string     `json:"envelope,omitempty"`          // JSON envelope data for stream_end
	Data            string     `json:"data,omitempty"`              // JSON payload for tool_warning events
	Detail          string     `json:"detail,omitempty"`            // Short label for tool_call (e.g., command, path)
}

// Usage contains token usage for a completed response.
type Usage struct {
	InputTokens         int    `json:"input_tokens"`
	OutputTokens        int    `json:"output_tokens"`
	CacheCreationTokens int    `json:"cache_creation_tokens"`
	CacheReadTokens     int    `json:"cache_read_tokens"`
	StopReason          string `json:"stop_reason"`
}

// PresenceEvent is broadcast to all connected presence clients.
type PresenceEvent struct {
	Type      string `json:"type"`                 // stream_start, stream_end, tool_pending, tool_resolved
	SessionID string `json:"session_id"`
	AgentID   string `json:"agent_id,omitempty"`
	ToolName  string `json:"tool_name,omitempty"`
	Timestamp string `json:"timestamp"`
}

// IsCLIProvider returns true if the provider name is any CLI adapter variant
// (PTY bridge or subprocess bridge).
func IsCLIProvider(name string) bool {
	return name == "pty" || strings.HasPrefix(name, "pty-") || strings.HasPrefix(name, "sub-")
}

// IsPTYProvider returns true if the provider name is any PTY adapter variant.
func IsPTYProvider(name string) bool {
	return name == "pty" || strings.HasPrefix(name, "pty-")
}

// InferProvider maps a model name to a provider when the session has no
// explicit provider set.
func InferProvider(model string) string {
	switch {
	case model == "claude-cli":
		return "pty"
	case model == "codex-cli":
		return "pty-codex"
	case model == "gemini-cli":
		return "pty-gemini"
	case strings.HasPrefix(model, "gpt-") || strings.HasPrefix(model, "o1-") || strings.HasPrefix(model, "o3-"):
		return "openai"
	case strings.HasPrefix(model, "llama") || strings.HasPrefix(model, "mistral") || strings.HasPrefix(model, "gemma"):
		return "ollama"
	default:
		return "anthropic"
	}
}

// TruncateStr truncates a string to maxLen, appending "..." if truncated.
func TruncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// intentStopWords are common words filtered out during intent extraction.
var intentStopWords = map[string]bool{
	"a": true, "an": true, "the": true, "is": true, "are": true, "was": true,
	"were": true, "be": true, "been": true, "being": true, "have": true,
	"has": true, "had": true, "do": true, "does": true, "did": true,
	"will": true, "would": true, "could": true, "should": true, "may": true,
	"might": true, "shall": true, "can": true, "to": true, "of": true,
	"in": true, "for": true, "on": true, "with": true, "at": true,
	"by": true, "from": true, "as": true, "into": true, "about": true,
	"that": true, "this": true, "it": true, "its": true, "i": true,
	"me": true, "my": true, "we": true, "our": true, "you": true,
	"your": true, "he": true, "she": true, "they": true, "them": true,
	"and": true, "or": true, "but": true, "not": true, "no": true,
	"if": true, "then": true, "so": true, "just": true, "also": true,
	"very": true, "too": true, "some": true, "any": true, "all": true,
	"what": true, "how": true, "when": true, "where": true, "which": true,
	"who": true, "why": true, "please": true, "thanks": true, "hi": true,
	"hello": true, "hey": true, "like": true, "want": true, "need": true,
	"thing": true, "things": true, "make": true, "let": true, "get": true,
}

// ExtractIntent derives an intent string and keyword hints from a user message.
// It extracts meaningful words (skipping stop words and short tokens) and
// returns a short intent phrase plus up to 10 keyword hints.
func ExtractIntent(userMessage string) (intent string, hints []string) {
	msg := strings.ToLower(userMessage)
	for _, ch := range []string{",", ".", "!", "?", ";", ":", "'", "\"", "(", ")", "[", "]", "{", "}", "\n", "\t"} {
		msg = strings.ReplaceAll(msg, ch, " ")
	}

	words := strings.Fields(msg)
	seen := make(map[string]bool)
	var keywords []string

	for _, w := range words {
		if len(w) < 3 {
			continue
		}
		if intentStopWords[w] {
			continue
		}
		if seen[w] {
			continue
		}
		seen[w] = true
		keywords = append(keywords, w)
		if len(keywords) >= 10 {
			break
		}
	}

	if len(keywords) == 0 {
		return "general", nil
	}

	intentWords := keywords
	if len(intentWords) > 3 {
		intentWords = intentWords[:3]
	}
	intent = strings.Join(intentWords, " ")

	return intent, keywords
}
