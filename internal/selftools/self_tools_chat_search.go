package selftools

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/store"
)

// chatSearchSnippetRadius is the number of bytes (approximate) of context
// shown before and after a match in a snippet excerpt.
const chatSearchSnippetRadius = 80

// chatSearchDefaultLimit is the default max snippets returned.
const chatSearchDefaultLimit = 20

// chatSearchMaxLimit caps the limit arg so the tool can't blow memory.
const chatSearchMaxLimit = 100

// chatGetDefaultLimit / chatGetMaxLimit bound chat_get's page size — chats can be
// large (thousands of messages) so the default is conservative and the ceiling
// keeps a single page from blowing the LLM context window. The S4a cache-and-
// pointer pattern still applies to results that overflow the per-tool ceiling.
const (
	chatGetDefaultLimit = 50
	chatGetMaxLimit     = 500
)

// ChatSearchSnippet is one search hit returned by chat_search. In cross-session
// mode the SessionID / ShortCode / SessionTitle fields disambiguate hits across
// chats; for same-session searches they are omitted (zero value) to keep the
// shape backwards-compatible with pre-CW-20260519-0063 callers.
type ChatSearchSnippet struct {
	TurnID            string  `json:"turn_id"`
	Role              string  `json:"role"`
	Excerpt           string  `json:"excerpt"`
	Source            string  `json:"source"` // "active" or "summary"
	CompactionEventID *string `json:"compaction_event_id,omitempty"`
	SessionID         string  `json:"session_id,omitempty"`
	ShortCode         string  `json:"short_code,omitempty"`
	SessionTitle      string  `json:"session_title,omitempty"`
}

// ChatMessageView is one message returned by chat_get. Mirrors the public
// shape of store.Message but trims internal fields the agent does not need
// and renames Content → Text after the same JSON-envelope unwrap chat_search
// performs (so the agent reads plain prose, not `{"v":1,"text":"…"}`).
type ChatMessageView struct {
	ID          string `json:"id"`
	Role        string `json:"role"`
	Text        string `json:"text"`
	IsCompacted bool   `json:"is_compacted,omitempty"`
	CreatedAt   string `json:"created_at"`
}

// resolveChatTarget normalises a short code or session UUID and looks the
// session up. Returns the resolved Session plus a friendly error result if
// the lookup failed. Workspace enforcement is the caller's responsibility.
//
// Accepted forms for short codes: `c248`, `C248`, `#c248`, `#C248` — leading
// `#` and uppercase are normalised away. UUIDs are passed through; the lookup
// distinguishes by SUBSTR(code, 0, 1)=='c' AND remainder-is-digits.
func resolveChatTarget(st *SelfToolsTransport, raw string) (*store.Session, *mcp.ToolResult) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, mcp.ErrorResult("chat target is required (short_code like c248, or session_id UUID)")
	}
	if st == nil || st.Store == nil {
		return nil, mcp.ErrorResult("internal: store unavailable")
	}

	// Normalise short codes: strip leading '#', lowercase.
	candidate := strings.TrimPrefix(raw, "#")
	candidate = strings.ToLower(candidate)

	if isShortCode(candidate) {
		sess, err := st.Store.GetSessionByShortCode(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, candidate)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, mcp.ErrorResult(fmt.Sprintf("no chat found with short_code %q (try the chat list — short codes look like c248)", raw))
			}
			return nil, mcp.ErrorResult(fmt.Sprintf("resolve short_code %q: %v", raw, err))
		}
		return sess, nil
	}

	// Treat as session UUID (or any other ID form).
	sess, err := st.Store.GetSession(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, raw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, mcp.ErrorResult(fmt.Sprintf("no chat found with session_id %q", raw))
		}
		return nil, mcp.ErrorResult(fmt.Sprintf("resolve session_id %q: %v", raw, err))
	}
	return sess, nil
}

// isShortCode reports whether s looks like a short code (`c<digits>`).
func isShortCode(s string) bool {
	if len(s) < 2 || s[0] != 'c' {
		return false
	}
	for i := 1; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// callChatSearch implements chat_search — search a chat's conversation
// history including compacted (summarized) spans. By default the search is
// scoped to the current session (back-compat with the pre-CW-20260519-0063
// shape). Passing a `target` arg (short code or session_id) redirects the
// search to that chat instead — workspace-scoped, read-only.
// Shape mirrors fetch_tool_result per D3. (P8B, CW-20260420-0026;
// cross-session extension CW-20260519-0063.)
func (st *SelfToolsTransport) callChatSearch(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	query := strArg(args, "query", "")
	if query == "" {
		return mcp.ErrorResult("query is required"), nil
	}

	scope := strArg(args, "scope", "all")
	switch scope {
	case "active", "compacted", "all":
	default:
		return mcp.ErrorResult(fmt.Sprintf("scope must be 'active', 'compacted', or 'all'; got %q", scope)), nil
	}

	limit := intArgFull(args, "limit", chatSearchDefaultLimit)
	if limit <= 0 {
		limit = chatSearchDefaultLimit
	}
	if limit > chatSearchMaxLimit {
		limit = chatSearchMaxLimit
	}

	// Resolve the target chat. Precedence:
	//   1. explicit `target` (short_code or session_id) — cross-session mode
	//   2. explicit `session_id` arg — back-compat alias for `target`
	//   3. session id from ctx — same-session search (default)
	targetArg := strArg(args, "target", "")
	// `isLegacyAlias` preserves the pre-PR-#213 permissive semantics of the
	// `session_id` arg: it used to be a raw passthrough — `sessionID = sid`
	// with no DB validation — so callers passing a deleted/archived id (or
	// any opaque string) got an empty result, not an error. The new `target`
	// arg is explicit and strict; only the legacy alias keeps the old
	// permissive fall-through.
	isLegacyAlias := false
	if targetArg == "" {
		if sid := strArg(args, "session_id", ""); sid != "" {
			targetArg = sid
			isLegacyAlias = true
		}
	}

	var (
		targetSess *store.Session
		crossMode  bool
		sessionID  string
	)
	if targetArg != "" {
		sess, errRes := resolveChatTarget(st, targetArg)
		if errRes != nil {
			if !isLegacyAlias {
				// `target` is the strict, explicit arg — surface resolution errors.
				return errRes, nil
			}
			// Back-compat: `session_id` legacy alias falls through to using
			// the raw string as the search scope (matches the pre-PR-#213
			// behavior).
			sessionID = targetArg
		} else {
			targetSess = sess
			sessionID = sess.ID
			// Only treat as cross-session when the resolved target differs from
			// the caller's current session — same-id `target` arg keeps the legacy
			// shape (no per-snippet session_id field).
			if ctxSess := mcp.SessionIDFromContext(ctx); ctxSess != "" && ctxSess != sess.ID {
				crossMode = true
			}
		}
	} else {
		sessionID = mcp.SessionIDFromContext(ctx)
		if sessionID == "" {
			return mcp.ErrorResult("no session_id in context — chat_search requires a session context, or pass `target` (short_code/session_id) to search a specific chat"), nil
		}
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
	msgs, err := st.Store.ListMessages(ctx, sessionID, maxMessages)
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("list messages: %v", err)), nil
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
		if crossMode && targetSess != nil {
			snip.SessionID = targetSess.ID
			snip.ShortCode = targetSess.ShortCode
			snip.SessionTitle = targetSess.Title
		}
		snippets = append(snippets, snip)
	}

	if len(snippets) == 0 {
		where := fmt.Sprintf("session %s", sessionID)
		if targetSess != nil {
			where = fmt.Sprintf("chat %s (%s)", targetSess.ShortCode, targetSess.ID)
		}
		return mcp.TextResult(fmt.Sprintf("No matches found for %q in %s (scope=%s).", query, where, scope)), nil
	}

	payload := map[string]any{
		"query":    query,
		"scope":    scope,
		"count":    len(snippets),
		"snippets": snippets,
	}
	if targetSess != nil {
		payload["session_id"] = targetSess.ID
		payload["short_code"] = targetSess.ShortCode
		if crossMode {
			payload["cross_session"] = true
		}
	}
	out, err := json.Marshal(payload)
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("marshal results: %v", err)), nil
	}
	return mcp.TextResult(string(out)), nil
}

// callChatGet implements chat_get — fetch the messages of a target chat by
// short_code or session_id, paginated. Workspace-scoped, read-only.
//
// Args:
//
//	target      string  (required) — short code (`c248`, `#c248`) or session UUID
//	limit       int     (optional, default 50, max 500)
//	offset      int     (optional, default 0)
//	include_compacted bool (optional, default true) — include summary blobs
//
// Return shape: { session_id, short_code, title, workspace_id, total,
//
//	has_more, messages: [{id, role, text, is_compacted, created_at}, ...] }
//
// CW-20260519-0063.
func (st *SelfToolsTransport) callChatGet(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	targetArg := strArg(args, "target", "")
	if targetArg == "" {
		// Accept `short_code` and `session_id` as aliases — the spec calls out
		// both, and accepting either keeps a future-friendly call site.
		targetArg = strArg(args, "short_code", "")
	}
	if targetArg == "" {
		targetArg = strArg(args, "session_id", "")
	}
	if targetArg == "" {
		return mcp.ErrorResult("target is required (short_code like c248, or session_id UUID)"), nil
	}

	sess, errRes := resolveChatTarget(st, targetArg)
	if errRes != nil {
		return errRes, nil
	}

	limit := intArgFull(args, "limit", chatGetDefaultLimit)
	if limit <= 0 {
		limit = chatGetDefaultLimit
	}
	if limit > chatGetMaxLimit {
		limit = chatGetMaxLimit
	}
	offset := intArgFull(args, "offset", 0)
	if offset < 0 {
		offset = 0
	}

	includeCompacted := true
	if v, ok := args["include_compacted"]; ok {
		if b, ok := v.(bool); ok {
			includeCompacted = b
		}
	}

	page, err := st.Store.ListMessagesPaginated(ctx, sess.ID, limit, offset)
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("list messages for %s: %v", sess.ShortCode, err)), nil
	}

	views := make([]ChatMessageView, 0, len(page.Messages))
	for _, m := range page.Messages {
		if !includeCompacted && m.IsCompacted {
			continue
		}
		views = append(views, ChatMessageView{
			ID:          m.ID,
			Role:        m.Role,
			Text:        extractMessageText(m.Content),
			IsCompacted: m.IsCompacted,
			CreatedAt:   m.CreatedAt,
		})
	}

	payload := map[string]any{
		"session_id": sess.ID,
		"short_code": sess.ShortCode,
		"title":      sess.Title,
		"total":      page.Total,
		"offset":     offset,
		"limit":      limit,
		"has_more":   page.HasMore,
		"count":      len(views),
		"messages":   views,
	}
	out, err := json.Marshal(payload)
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("marshal chat_get result: %v", err)), nil
	}
	return mcp.TextResult(string(out)), nil
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

// buildExcerpt returns a snippet of text centered on [matchStart, matchEnd)
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
