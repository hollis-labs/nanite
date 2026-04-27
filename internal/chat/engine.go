package chat

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hollis-labs/nanite/internal/toolclient"
	"github.com/hollis-labs/nanite/pkg/models"
)

// AgentConstraints holds parsed runtime constraints from AgentProfile.Constraints.
type AgentConstraints struct {
	MaxIterations  int `json:"max_iterations"`
	MaxTimeSeconds int `json:"max_time_seconds"`
	RetryBudget    int `json:"retry_budget"`

	// Phase 4 — Chat Loop Hardening.
	MaxTurns           int `json:"max_turns"`              // 0=default(25), -1=unlimited, >0=value
	HardCeiling        int `json:"hard_ceiling"`           // 0=default(100), absolute max turns
	// ConsecutiveFailCap is the soft-warning threshold. CW-20260417-0485:
	// reaching this count no longer terminates the loop — it only drives the
	// "critical"-level tool_warning SSE so the UI can warn the user that a
	// runaway is imminent. See RunawayFailCap for the terminal cap.
	ConsecutiveFailCap int `json:"consecutive_fail_cap"`   // 0=default(3), tool_warning turns critical after N consecutive failures
	// RunawayFailCap is the hard circuit-breaker. When consecutive tool
	// failures reach this count the loop emits a chat-loop-terminated
	// envelope and exits. CW-20260417-0485.
	RunawayFailCap     int `json:"runaway_fail_cap"`       // 0=default(10), hard terminate after N consecutive failures
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

// Delta phase constants for StreamEvent.Phase (F4 / CW-20260419-0029).
//
// Narration is inter-iteration prose the LLM emits while calling tools
// ("Let me look at X…"). Final is the post-end_turn text that becomes the
// assistant's answer. Old clients without phase awareness receive the field as
// omitempty so the change is additive.
//
// PhaseThinking (F3 / CW-20260420-0023) carries interleaved thinking blocks
// from the interleaved-thinking-2025-05-14 beta. These arrive between tool
// calls as signed think-block content. The FE routes them to the "Working…"
// strip and the post-stream collapse-pill.
const (
	PhaseNarration = "narration" // inter-iteration prose, between tool_use blocks
	PhaseFinal     = "final"     // post-end_turn text — the answer bubble
	PhaseThinking  = "thinking"  // F3: interleaved thinking block content (signed)
)

// StreamEvent is the event sent to SSE clients.
type StreamEvent struct {
	Type            string     `json:"type"`                        // stream_start, delta, replace_content, stream_end, error, tool_call, tool_result, status, circuit_open, session_takeover, tool_warning, plugin_envelope, message_received, subagent_run_status_changed
	Content         string     `json:"content,omitempty"`
	MessageID       string     `json:"message_id,omitempty"`
	AgentID         string     `json:"agent_id,omitempty"`
	Usage           *Usage     `json:"usage,omitempty"`
	Error           string     `json:"error,omitempty"`
	StructuredError *ChatError `json:"structured_error,omitempty"`
	Tool            string     `json:"tool,omitempty"`              // tool name for tool_call/tool_result
	ToolID          string     `json:"tool_id,omitempty"`           // tool_use_id
	Summary         string     `json:"summary,omitempty"`           // tool result summary
	Envelope        string     `json:"envelope,omitempty"`          // JSON envelope data for stream_end and plugin_envelope
	PluginID        string     `json:"plugin_id,omitempty"`         // emitting plugin id for plugin_envelope
	Data            string     `json:"data,omitempty"`              // JSON payload for tool_warning events
	Detail          string     `json:"detail,omitempty"`            // Short label for tool_call (e.g., command, path)

	// Phase classifies delta events by their narrative role (F4 / CW-20260419-0029).
	// "narration" — inter-iteration prose between tool_use blocks.
	// "final"     — post-end_turn text that forms the assistant's answer.
	// Empty for non-delta event types and for legacy streams that predate F4.
	// F3 will extend this with "thinking" for interleaved think-block content.
	Phase string `json:"phase,omitempty"`

	// EventID is a monotonically increasing sequence number per message stream,
	// assigned by StreamManager when the event is written to the ring buffer.
	// Frontends track the highest EventID seen and pass it back as `?from=<N>`
	// when reconnecting, so the server can replay events missed during the
	// disconnect. Zero means "not yet assigned" (e.g., synthetic events
	// surfaced outside the ring-buffer path).
	// CW-20260418-0100.
	EventID uint64 `json:"event_id,omitempty"`
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
// explicit provider set. Resolution order:
//
//  1. Exact lookup in the canonical registry (pkg/models). This covers
//     every seeded model including o4-mini, gemini-*, mistral-*, codestral,
//     and gateway-prefixed IDs — none of which the old prefix-based switch
//     handled correctly.
//  2. Gateway-prefix helper for bare prefixed IDs not yet registered.
//  3. DefaultProvider (anthropic) as the terminal fallback.
//
// See audit 2026-04-11 finding 02 for the misroutes this replaces.
func InferProvider(model string) string {
	if p := models.ProviderFor(model); p != "" {
		return p
	}
	if p, ok := models.ProviderHasPrefix(model); ok {
		return p
	}
	// Unregistered-model fallbacks. Keep OpenAI GPT family and
	// Ollama-style local names routable until the registry is expanded or
	// the operator registers the row explicitly. Mistral API models are
	// intentionally not prefix-matched here (see audit 02): Mistral API
	// IDs end with "-latest" and must be registered to resolve correctly.
	switch {
	case strings.HasPrefix(model, "gpt-"),
		strings.HasPrefix(model, "o1-"),
		strings.HasPrefix(model, "o3-"),
		strings.HasPrefix(model, "o4-"):
		return "openai"
	case strings.HasPrefix(model, "llama"),
		strings.HasPrefix(model, "gemma"),
		strings.HasPrefix(model, "mistral-7b"),
		strings.Contains(model, ":"): // "model:tag" is an ollama-ism
		return "ollama"
	}
	return models.DefaultProvider()
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
