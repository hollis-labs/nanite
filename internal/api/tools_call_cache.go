package api

import (
	"context"
	"log/slog"
	"strings"

	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/tool"
	"github.com/hollis-labs/nanite/internal/truncate"
)

// CW-20260929-0011 / D-38: a CLI-launched agent's self-tool calls reach the
// harness only through POST /api/tools/call, so they never pass the chat
// executor's result-cache + model-aware truncation. This adapter applies
// exactly that pair — nothing else. The per-tool cap, cumulative turn ceiling
// and runaway detection protect Nanite's own model-driving loop; the CLI
// already runs its own.

// maxCallIDLen bounds a caller-supplied call id.
const maxCallIDLen = 128

// notCallIDRune reports whether r is outside the characters a call id may use.
func notCallIDRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return false
	}
	return r != '-' && r != '_' && r != '.' && r != ':'
}

// selfToolCallID is the id a forwarded call is recorded under: the caller's own
// when it is a short run of ID characters, otherwise a fresh ULID. Callers are
// other processes on this machine, but the value ends up in a stored row, so it
// is checked rather than trusted.
func selfToolCallID(supplied string) string {
	if n := len(supplied); n > 0 && n <= maxCallIDLen && strings.IndexFunc(supplied, notCallIDRune) < 0 {
		return supplied
	}
	return tool.NewCallID()
}

// toolCallID is the tool_call_id stored for this call: "self-tool:<name>:<id>",
// unique per call. The argument row and any cached-result row of one call carry
// the same value. A request that did not go through handleSelfToolCall gets a
// fresh id.
func (r selfToolCallRequest) toolCallID() string {
	id := r.callID
	if id == "" {
		id = tool.NewCallID()
	}
	return "self-tool:" + r.Name + ":" + id
}

// sessionModel returns the model recorded on the session, or "" when the
// session is unknown or has none. "" makes truncate.BudgetForModel fall back
// to its floor budget, which is the conservative choice.
func (a *API) sessionModel(ctx context.Context, sessionID string) string {
	if sessionID == "" || a.Services == nil || a.Services.Store == nil {
		return ""
	}
	sess, err := a.Services.Store.GetSession(ctx, sessionID)
	if err != nil || sess == nil {
		return ""
	}
	return sess.Model
}

// cacheableSelfToolCall reports whether a forwarded call's result may be
// routed through the result cache. The caller must be able to retrieve what is
// cached (cacheRetrieval), the cache must be scoped to a session so one
// session cannot read another's results, and cache-navigation and discovery
// tools stay inline exactly as on the chat path.
func (a *API) cacheableSelfToolCall(req selfToolCallRequest) bool {
	return req.CacheRetrieval && req.SessionID != "" &&
		a.Services != nil && a.Services.ResultCache != nil &&
		!tool.IsCacheExemptTool(req.Name)
}

// presentSelfToolResult replaces an over-budget text result with the cache's
// bounded preview plus a recovery pointer, using the same budget the chat path
// derives from the session's model. Error results pass through verbatim
// (their reason is load-bearing), as do non-text blocks. On any cache failure
// the original result is returned unchanged: this path never makes a call fail.
func (a *API) presentSelfToolResult(ctx context.Context, req selfToolCallRequest, result *mcp.ToolResult) *mcp.ToolResult {
	if result == nil || result.IsError || !a.cacheableSelfToolCall(req) {
		return result
	}
	var texts []string
	first := -1
	for i, block := range result.Content {
		if block.Type == "text" {
			if first < 0 {
				first = i
			}
			texts = append(texts, block.Text)
		}
	}
	if first < 0 {
		return result
	}
	body := strings.Join(texts, "\n")
	budget := truncate.BudgetForModel(a.sessionModel(ctx, req.SessionID))
	if len(body) <= budget {
		return result
	}
	view, err := a.Services.ResultCache.PresentResult(req.SessionID, req.toolCallID(), req.Name, body, budget)
	if err != nil {
		slog.Warn("tools/call: result cache store error", "tool", req.Name, "err", err)
		return result
	}
	out := &mcp.ToolResult{IsError: result.IsError}
	for i, block := range result.Content {
		switch {
		case block.Type != "text":
			out.Content = append(out.Content, block)
		case i == first:
			out.Content = append(out.Content, mcp.ToolContent{Type: "text", Text: view.Content})
		}
	}
	return out
}

// serveCacheNavigation handles fetch_tool_result / search_tool_result for a
// forwarded call. ok is false when name is not a cache-navigation tool.
func (a *API) serveCacheNavigation(ctx context.Context, req selfToolCallRequest) (result *mcp.ToolResult, ok bool) {
	if !tool.IsResultCacheTool(req.Name) {
		return nil, false
	}
	if a.Services == nil || a.Services.ResultCache == nil || req.SessionID == "" {
		return &mcp.ToolResult{IsError: true, Content: []mcp.ToolContent{{Type: "text", Text: "Error: result cache not available"}}}, true
	}
	budget := truncate.BudgetForModel(a.sessionModel(ctx, req.SessionID))
	var text string
	var isErr bool
	if req.Name == "fetch_tool_result" {
		text, isErr = a.Services.ResultCache.FetchToolResult(req.SessionID, req.Args, budget)
	} else {
		text, isErr = a.Services.ResultCache.SearchToolResult(req.SessionID, req.Args, budget)
	}
	return &mcp.ToolResult{IsError: isErr, Content: []mcp.ToolContent{{Type: "text", Text: text}}}, true
}

// persistSelfToolArguments records the call's redacted, bounded arguments the
// way the chat executor does for its own calls (D-35), so a CLI agent's
// Nanite-tool calls are auditable too. Only dispatched calls are recorded, and
// only with a session to attribute them to. A failure is logged and never
// affects the call.
func (a *API) persistSelfToolArguments(req selfToolCallRequest) {
	if req.SessionID == "" || a.Services == nil || a.Services.ResultCache == nil {
		return
	}
	if _, err := a.Services.ResultCache.PersistArguments(req.SessionID, req.toolCallID(), req.Name, req.Args); err != nil {
		slog.Warn("tools/call: tool argument persist error", "tool", req.Name, "err", err)
	}
}
