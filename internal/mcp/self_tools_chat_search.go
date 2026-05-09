package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// chatSearchSnippetRadius is the number of bytes (approximate) of context
// shown before and after a match in a snippet excerpt.
const chatSearchSnippetRadius = 80

// chatSearchDefaultLimit is the default max snippets returned.
const chatSearchDefaultLimit = 20

// chatSearchMaxLimit caps the limit arg so the tool can't blow memory.
const chatSearchMaxLimit = 100

// ChatSearchSnippet is one search hit returned by chat_search.
type ChatSearchSnippet struct {
	TurnID             string  `json:"turn_id"`
	Role               string  `json:"role"`
	Excerpt            string  `json:"excerpt"`
	Source             string  `json:"source"` // "active" or "summary"
	CompactionEventID  *string `json:"compaction_event_id,omitempty"`
}

// callChatSearch implements chat_search — search this session's
// conversation history including compacted (summarised) spans.
// Shape mirrors fetch_tool_result per D3. (P8B, CW-20260420-0026)
func (st *SelfToolsTransport) callChatSearch(ctx context.Context, args map[string]any) (*ToolResult, error) {
	query := strArg(args, "query", "")
	if query == "" {
		return errorResult("query is required"), nil
	}

	scope := strArg(args, "scope", "all")
	switch scope {
	case "active", "compacted", "all":
	default:
		return errorResult(fmt.Sprintf("scope must be 'active', 'compacted', or 'all'; got %q", scope)), nil
	}

	limit := intArgFull(args, "limit", chatSearchDefaultLimit)
	if limit <= 0 {
		limit = chatSearchDefaultLimit
	}
	if limit > chatSearchMaxLimit {
		limit = chatSearchMaxLimit
	}

	// Session ID from context (stamped by the chat tool executor).
	sessionID := SessionIDFromContext(ctx)
	// Also allow explicit override via args for callers that supply it directly.
	if sid := strArg(args, "session_id", ""); sid != "" {
		sessionID = sid
	}
	if sessionID == "" {
		return errorResult("no session_id in context — chat_search requires a session context"), nil
	}

	// Compile the query as a case-insensitive regexp. Fall back to a
	// literal substring match if the query is not a valid regexp.
	re, err := regexp.Compile("(?i)" + regexp.QuoteMeta(query))
	if err == nil {
		// Try as a raw regexp (without QuoteMeta).
		if raw, err2 := regexp.Compile("(?i)" + query); err2 == nil {
			re = raw
		}
	}

	// Pull all messages for the session. ListMessages is capped by its limit
	// arg — pass a high ceiling so we get everything; the session store query
	// itself returns DESC-then-reversed so the slice is chronological.
	const maxMessages = 10000
	msgs, err := st.Store.ListMessages(sessionID, maxMessages)
	if err != nil {
		return errorResult(fmt.Sprintf("list messages: %v", err)), nil
	}

	// Build the snippet list.
	var snippets []ChatSearchSnippet
	for _, m := range msgs {
		if len(snippets) >= limit {
			break
		}

		// scope filter
		isCompacted := m.IsCompacted
		if scope == "active" && isCompacted {
			continue
		}
		if scope == "compacted" && !isCompacted {
			continue
		}

		content := extractMessageText(m.Content)
		if content == "" {
			continue
		}

		loc := re.FindStringIndex(content)
		if loc == nil {
			continue
		}

		excerpt := buildExcerpt(content, loc[0], loc[1], chatSearchSnippetRadius)
		source := "active"
		if isCompacted {
			source = "summary"
		}

		snip := ChatSearchSnippet{
			TurnID:  m.ID,
			Role:    m.Role,
			Excerpt: excerpt,
			Source:  source,
		}
		snippets = append(snippets, snip)
	}

	if len(snippets) == 0 {
		return textResult(fmt.Sprintf("No matches found for %q in session %s (scope=%s).", query, sessionID, scope)), nil
	}

	out, err := json.Marshal(map[string]any{
		"query":    query,
		"scope":    scope,
		"count":    len(snippets),
		"snippets": snippets,
	})
	if err != nil {
		return errorResult(fmt.Sprintf("marshal results: %v", err)), nil
	}
	return textResult(string(out)), nil
}

// extractMessageText pulls the searchable text from a stored message Content
// field. Content may be a bare string or a JSON envelope like {"text":"..."}.
func extractMessageText(content string) string {
	if content == "" {
		return ""
	}
	// Try JSON object with a "text" key (assistant messages stored as
	// {"v":1,"text":"...","tier":"..."}).
	var payload struct {
		Text    string `json:"text"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(content), &payload); err == nil {
		if payload.Text != "" {
			return payload.Text
		}
		if payload.Content != "" {
			return payload.Content
		}
	}
	// Fall back to bare string.
	return content
}

// intArgFull is like intArg but also handles native Go int/int64/int32 types,
// which appear when test code passes map literals with untyped integer constants.
// The shared intArg only handles float64 and json.Number (the JSON decode path).
func intArgFull(args map[string]any, key string, def int) int {
	v, ok := args[key]
	if !ok {
		return def
	}
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case int32:
		return int(n)
	case float64:
		return int(n)
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return def
		}
		return int(i)
	default:
		return def
	}
}

// buildExcerpt returns a snippet of text centred on [matchStart, matchEnd)
// with up to radius bytes of context on each side. The match is wrapped in
// «…» markers so the caller can see where the hit is.
func buildExcerpt(text string, matchStart, matchEnd, radius int) string {
	// Clamp to valid rune boundaries.
	start := matchStart - radius
	if start < 0 {
		start = 0
	}
	end := matchEnd + radius
	if end > len(text) {
		end = len(text)
	}

	// Advance start to a valid rune boundary.
	for start > 0 && !utf8.RuneStart(text[start]) {
		start--
	}
	// Retract end to a valid rune boundary.
	for end < len(text) && !utf8.RuneStart(text[end]) {
		end++
	}

	prefix := ""
	if start > 0 {
		prefix = "…"
	}
	suffix := ""
	if end < len(text) {
		suffix = "…"
	}

	before := text[start:matchStart]
	match := text[matchStart:matchEnd]
	after := text[matchEnd:end]

	return fmt.Sprintf("%s%s«%s»%s%s",
		prefix,
		strings.ReplaceAll(before, "\n", " "),
		strings.ReplaceAll(match, "\n", " "),
		strings.ReplaceAll(after, "\n", " "),
		suffix,
	)
}
