package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	"github.com/hollis-labs/nanite/internal/apiauth"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// adminMCPPath is the streamable-HTTP MCP endpoint for agent management. It
// sits under /api/admin, so the route rules already require the admin scope.
const adminMCPPath = "/api/admin/mcp"

// adminMCPMaxResponse caps what one tool call returns. Sessions and history
// page through limit/cursor; the other lists are small, and an oversize body is
// refused rather than silently cut.
const adminMCPMaxResponse = 256 << 10

// adminMCPDefaultLimit is the page size of list tools that take no limit.
const adminMCPDefaultLimit = 50

// adminMCPTool maps one MCP tool onto an existing HTTP route. Tools add no
// logic of their own: each call is dispatched through the API mux with the
// principal of the request that reached /api/admin/mcp, so the handlers'
// own validation, reason-required rules and error bodies apply. The inner
// call does not pass the auth middleware's per-route scope rules or the
// /api/admin Origin allowlist again; the outer request did, and the endpoint
// re-checks for an agents:manage principal itself (adminMCPPrincipal), and
// each tool checks any scope it needs beyond that (admin for grants).
type adminMCPTool struct {
	name, description string
	method, path      string   // path may hold {param} placeholders filled from arguments
	pathParams        []string // argument names consumed by the path
	queryParams       []string // argument names sent as query string
	body              bool     // remaining arguments are sent as the JSON body
	defaultLimit      bool     // list tool: send limit=50 when the caller gives none
	properties        map[string]any
	required          []string
	// scope is what the tool needs beyond agents:manage (which reaching the
	// endpoint already needs): admin for grants, operate for sessions and turns.
	scope apiauth.Scope
}

func prop(typ, desc string) map[string]any { return map[string]any{"type": typ, "description": desc} }

const adminCredentialNote = " Needs a credential with the agents:manage scope (or the operator token); agent credentials are refused."

const grantCredentialNote = " Needs an admin credential (the operator token): an agents:manage credential is refused."

const sessionCredentialNote = " Needs agents:manage and operate (or the operator token); agent credentials are refused."

var reasonProp = prop("string", "Why this change is made (required, at most 500 characters); recorded with the change.")

var adminMCPTools = []adminMCPTool{
	{name: "agent_setup", method: "POST", path: "/api/agent-setup", body: true,
		description: "Create or update a Nanite agent in one call: definition, host settings and tool grants. Same input and rules as POST /api/agent-setup. Give an archetype or a full definition artifact; provider selects API or CLI runtime; tools lists native tool grants (naming any needs an admin credential; with agents:manage send an empty list and an admin grants tools afterwards); directories are absolute paths on the machine Nanite runs on." + adminCredentialNote,
		properties: map[string]any{
			"artifact":    prop("string", "Full agent definition artifact (JSON text); alternative to archetype."),
			"archetype":   prop("string", "Name of a built-in archetype to start from."),
			"resources":   map[string]any{"type": "array", "description": "Authored resources: objects with uri and content.", "items": map[string]any{"type": "object"}},
			"slug":        prop("string", "Host settings slug."),
			"title":       prop("string", "Display title."),
			"provider":    prop("string", "Provider or CLI runtime, e.g. an API provider id or a CLI agent kind."),
			"model":       prop("string", "Model id."),
			"runtime":     prop("string", "Runtime kind."),
			"directories": map[string]any{"type": "array", "description": "Absolute directories the agent may use, on the Nanite machine.", "items": map[string]any{"type": "string"}},
			"tools":       map[string]any{"type": "array", "description": "Native tools to grant.", "items": map[string]any{"type": "string"}},
			"reason":      reasonProp,
		}, required: []string{"provider", "reason"}},
	{name: "agent_list", method: "GET", path: "/api/native-agents", queryParams: []string{"limit", "offset"}, defaultLimit: true,
		description: "List Nanite native agents (definition, host settings, grants), at most limit per call (default 50)." + adminCredentialNote,
		properties:  map[string]any{"limit": prop("integer", "Page size, 1-500 (default 50)."), "offset": prop("integer", "Items to skip; a full page means there may be more.")}},
	{name: "agent_get", method: "GET", path: "/api/native-agents/{id}", pathParams: []string{"id"},
		description: "Show one Nanite native agent by id." + adminCredentialNote,
		properties:  map[string]any{"id": prop("string", "Agent id.")}, required: []string{"id"}},
	{name: "host_settings_list", method: "GET", path: "/api/agent-host-settings", queryParams: []string{"limit", "offset"}, defaultLimit: true,
		description: "List agent host settings (mutable execution inputs), at most limit per call (default 50)." + adminCredentialNote,
		properties:  map[string]any{"limit": prop("integer", "Page size, 1-500 (default 50)."), "offset": prop("integer", "Items to skip; a full page means there may be more.")}},
	{name: "host_settings_create", method: "POST", path: "/api/agent-host-settings", body: true,
		description: "Create host settings for an agent definition." + adminCredentialNote,
		properties: map[string]any{
			"slug": prop("string", "Unique slug."), "title": prop("string", "Display title."),
			"definition_ref": map[string]any{"type": "object", "description": "Pinned definition reference."},
			"settings":       map[string]any{"type": "object", "description": "Native host settings: provider, model, directories, runtime."},
			"enabled":        prop("boolean", "Whether the host is enabled."),
		}, required: []string{"slug", "definition_ref", "settings"}},
	{name: "host_settings_update", method: "PUT", path: "/api/agent-host-settings/{id}", pathParams: []string{"id"}, body: true,
		description: "Replace host settings (full document, not a patch) at an explicit revision." + adminCredentialNote,
		properties: map[string]any{
			"id": prop("string", "Host settings id."), "revision": prop("string", "Current revision, for conflict detection."),
			"slug": prop("string", "Slug."), "title": prop("string", "Title."),
			"definition_ref": map[string]any{"type": "object"}, "settings": map[string]any{"type": "object"},
			"enabled": prop("boolean", "Whether the host is enabled."),
		}, required: []string{"id", "slug", "definition_ref", "settings"}},
	{name: "authority_list", method: "GET", path: "/api/native-authority", queryParams: []string{"host_settings_id", "limit", "offset"}, defaultLimit: true,
		description: "List the native authorities (actors that hold tool grants) of one host settings record, at most limit per call (default 50)." + adminCredentialNote,
		properties:  map[string]any{"host_settings_id": prop("string", "Host settings id."), "limit": prop("integer", "Page size, 1-500 (default 50)."), "offset": prop("integer", "Items to skip.")},
		required:    []string{"host_settings_id"}},
	{name: "authority_get", method: "GET", path: "/api/native-authority/{id}", pathParams: []string{"id"},
		description: "Show one native authority and its grants." + adminCredentialNote,
		properties:  map[string]any{"id": prop("string", "Authority id.")}, required: []string{"id"}},
	{name: "tool_grant", method: "PUT", path: "/api/native-authority/{id}/tools/{tool}", pathParams: []string{"id", "tool"}, body: true,
		description: "Grant a native tool to an authority. Granting requires an admin credential." + grantCredentialNote,
		properties:  map[string]any{"id": prop("string", "Authority id."), "tool": prop("string", "Tool name."), "reason": reasonProp},
		required:    []string{"id", "tool", "reason"}, scope: apiauth.ScopeAdmin},
	{name: "tool_ungrant", method: "DELETE", path: "/api/native-authority/{id}/tools/{tool}", pathParams: []string{"id", "tool"}, body: true,
		description: "Withdraw one tool grant from an authority." + grantCredentialNote,
		properties:  map[string]any{"id": prop("string", "Authority id."), "tool": prop("string", "Tool name."), "reason": reasonProp},
		required:    []string{"id", "tool"}, scope: apiauth.ScopeAdmin},
	{name: "skill_grant", method: "PUT", path: "/api/native-authority/{id}/skills/{skill}", pathParams: []string{"id", "skill"}, body: true,
		description: "Grant a skill the authority's definition requests, at the digest the definition pins for it. Same rule as tool_grant: granting requires an admin credential." + grantCredentialNote,
		properties: map[string]any{"id": prop("string", "Authority id."), "skill": prop("string", "Requested skill name (lowercase words joined by hyphens)."),
			"digest": prop("string", "The skill's pinned content digest, sha256:<64 hex>, as in the definition's requirements.skills."), "reason": reasonProp},
		required: []string{"id", "skill", "digest", "reason"}, scope: apiauth.ScopeAdmin},
	{name: "skill_ungrant", method: "DELETE", path: "/api/native-authority/{id}/skills/{skill}", pathParams: []string{"id", "skill"}, body: true,
		description: "Withdraw one skill grant from an authority." + grantCredentialNote,
		properties:  map[string]any{"id": prop("string", "Authority id."), "skill": prop("string", "Skill name."), "reason": reasonProp},
		required:    []string{"id", "skill", "reason"}, scope: apiauth.ScopeAdmin},
	{name: "authority_revoke", method: "POST", path: "/api/native-authority/{id}/revoke", pathParams: []string{"id"}, body: true,
		description: "Revoke an authority and every grant it holds." + grantCredentialNote,
		properties:  map[string]any{"id": prop("string", "Authority id."), "reason": reasonProp}, required: []string{"id", "reason"}, scope: apiauth.ScopeAdmin},
	{name: "session_start", method: "POST", path: "/api/agent/v1/sessions", body: true,
		description: "Start an agent session from a pinned definition and optional host settings. work_root, when set, must be an absolute path on the machine Nanite runs on." + sessionCredentialNote,
		properties: map[string]any{
			"definition_ref":  map[string]any{"type": "object", "description": "Pinned definition reference."},
			"model_selection": map[string]any{"type": "object"},
			"host_settings":   map[string]any{"type": "object", "description": "Host settings reference: id and revision."},
			"project_id":      prop("string", "Project id."), "title": prop("string", "Session title."),
			"work_root": prop("string", "Absolute working directory on the Nanite machine."),
			"metadata":  map[string]any{"type": "object"},
		}, required: []string{"definition_ref"}, scope: apiauth.ScopeOperate},
	{name: "session_list", method: "GET", path: "/api/agent/v1/sessions", queryParams: []string{"limit", "cursor", "include_archived"},
		description: "List agent sessions, a page at a time (limit 1-200, default 50); pass next_cursor back as cursor for the next page." + sessionCredentialNote,
		properties: map[string]any{"limit": prop("integer", "Page size, 1-200."), "cursor": prop("string", "next_cursor from the previous page."),
			"include_archived": prop("string", "\"true\" to include archived sessions.")}, scope: apiauth.ScopeOperate},
	{name: "session_get", method: "GET", path: "/api/agent/v1/sessions/{id}", pathParams: []string{"id"},
		description: "Show one agent session." + sessionCredentialNote,
		properties:  map[string]any{"id": prop("string", "Session view id.")}, required: []string{"id"}, scope: apiauth.ScopeOperate},
	{name: "session_history", method: "GET", path: "/api/agent/v1/sessions/{id}/messages", pathParams: []string{"id"}, queryParams: []string{"limit", "older_cursor"},
		description: "Read a session's message history, newest page first; pass the returned older cursor back as older_cursor for earlier messages." + sessionCredentialNote,
		properties: map[string]any{"id": prop("string", "Session view id."), "limit": prop("integer", "Page size, 1-200."),
			"older_cursor": prop("string", "Cursor from the previous page.")}, required: []string{"id"}, scope: apiauth.ScopeOperate},
	{name: "session_stop", method: "POST", path: "/api/agent/v1/sessions/{id}/stop", pathParams: []string{"id"}, body: true,
		description: "Stop an agent session." + sessionCredentialNote,
		properties:  map[string]any{"id": prop("string", "Session view id.")}, required: []string{"id"}, scope: apiauth.ScopeOperate},
	{name: "turn_send", method: "POST", path: "/api/agent/v1/sessions/{id}/turns", pathParams: []string{"id"}, body: true,
		description: "Send a turn (a user message) to a session." + sessionCredentialNote,
		properties:  map[string]any{"id": prop("string", "Session view id."), "content": prop("string", "Message text.")}, required: []string{"id"}, scope: apiauth.ScopeOperate},
	{name: "turn_get", method: "GET", path: "/api/agent/v1/sessions/{id}/turns/{turnId}", pathParams: []string{"id", "turnId"},
		description: "Show a turn's state." + sessionCredentialNote,
		properties:  map[string]any{"id": prop("string", "Session view id."), "turnId": prop("string", "Turn id.")}, required: []string{"id", "turnId"}, scope: apiauth.ScopeOperate},
	{name: "turn_cancel", method: "POST", path: "/api/agent/v1/sessions/{id}/turns/{turnId}/cancel", pathParams: []string{"id", "turnId"}, body: true,
		description: "Cancel a running turn." + sessionCredentialNote,
		properties:  map[string]any{"id": prop("string", "Session view id."), "turnId": prop("string", "Turn id.")}, required: []string{"id", "turnId"}, scope: apiauth.ScopeOperate},
}

// adminMCPServer builds the tool set. dispatch runs one request through the
// API mux; each call carries the caller's request context, so the credential
// that reached /api/admin/mcp is the one every wrapped route sees.
func adminMCPServer(a *API, dispatch http.Handler, carrier *http.Request) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "nanite-admin", Version: "1"}, nil)
	for _, t := range adminMCPTools {
		props := t.properties
		if props == nil {
			props = map[string]any{}
		}
		schema := map[string]any{"type": "object", "properties": props}
		if len(t.required) > 0 {
			schema["required"] = t.required
		}
		srv.AddTool(&mcp.Tool{Name: t.name, Description: t.description, InputSchema: schema},
			func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return t.call(ctx, dispatch, carrier, req.Params.Arguments)
			})
	}
	srv.AddTool(&mcp.Tool{Name: "host_settings_retire",
		Description: "Disable an agent's host settings (enabled=false) so no new session starts from them. This is a disable-only write: it skips the model and definition re-validation that host_settings_update does, so it works even when the model is no longer allowed. It does NOT stop live sessions (use session_stop) or remove tool grants (use authority_revoke or tool_ungrant)." + adminCredentialNote,
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"id": prop("string", "Host settings id."), "reason": reasonProp}, "required": []string{"id", "reason"}}},
		func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return a.retireHost(ctx, carrier, req.Params.Arguments)
		})
	return srv
}

func (t adminMCPTool) call(ctx context.Context, h http.Handler, carrier *http.Request, raw json.RawMessage) (*mcp.CallToolResult, error) {
	args := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return mcpToolError("arguments must be a JSON object"), nil
		}
	}
	if args == nil {
		args = map[string]any{}
	}
	p, ok := adminMCPPrincipal(ctx)
	if !ok {
		return mcpToolError("an agents:manage credential is required"), nil
	}
	if t.scope != "" && !p.Has(t.scope) {
		return mcpToolError("this tool needs the " + string(t.scope) + " scope"), nil
	}
	// Reasons stay in the domain audit; the route row names the tool and target.
	var targets []string
	for _, k := range []string{"id", "tool", "skill", "turnId", "slug"} {
		if v, ok := args[k].(string); ok && v != "" {
			targets = append(targets, v)
		}
	}
	target := strings.Join(targets, " ")
	path := t.path
	for _, p := range t.pathParams {
		v, _ := args[p].(string)
		if !adminMCPSafeID(v) {
			return mcpToolError(p + " is required and must be a single path segment"), nil
		}
		path = strings.ReplaceAll(path, "{"+p+"}", url.PathEscape(v))
		delete(args, p)
	}
	q := url.Values{}
	if t.defaultLimit {
		if _, ok := args["limit"]; !ok {
			args["limit"] = adminMCPDefaultLimit
		}
	}
	for _, name := range t.queryParams {
		if v, ok := args[name]; ok {
			q.Set(name, fmt.Sprint(v))
			delete(args, name)
		}
	}
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var body []byte
	if t.body {
		body, _ = json.Marshal(args)
	}
	status, out := adminDispatch(ctx, h, carrier, t.method, path, body)
	if t.method != http.MethodGet {
		switch t.name {
		case "agent_setup":
			if target == "" {
				var res struct {
					Slug string `json:"slug"`
				}
				_ = json.Unmarshal(out, &res)
				target = res.Slug
			}
		case "session_start":
			// Name the definition asked for and the session it produced.
			var req struct {
				Ref struct {
					ID string `json:"id"`
				} `json:"definition_ref"`
			}
			var res struct {
				View string `json:"session_view_id"`
			}
			_ = json.Unmarshal(body, &req)
			_ = json.Unmarshal(out, &res)
			target = strings.TrimSpace(req.Ref.ID + " " + res.View)
		}
		adminMCPAudit(ctx, "mcp:"+t.name+" "+target, status)
	}
	return mcpToolResult(status, out), nil
}

func (a *API) retireHost(ctx context.Context, _ *http.Request, raw json.RawMessage) (*mcp.CallToolResult, error) {
	if _, ok := adminMCPPrincipal(ctx); !ok {
		return mcpToolError("an agents:manage credential is required"), nil
	}
	var args struct {
		ID     string `json:"id"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(raw, &args); err != nil || !adminMCPSafeID(args.ID) {
		return mcpToolError("id is required and must be a single path segment"), nil
	}
	host, status, msg := a.retireHostSettings(ctx, args.ID, args.Reason, "mcp:host_settings_retire")
	if status != http.StatusOK {
		return mcpToolError(fmt.Sprintf("HTTP %d: %s", status, msg)), nil
	}
	out, _ := json.Marshal(host)
	return mcpToolResult(http.StatusOK, out), nil
}

// retireHostSettings is the disable-only retire shared by the MCP tool and
// POST /api/agent-host-settings/{id}/retire. It returns the HTTP status and, on
// failure, a message safe to show. The reason goes into the audit row: host
// settings have no audit table of their own.
func (a *API) retireHostSettings(ctx context.Context, id, reason, auditPrefix string) (store.AgentHostSettings, int, string) {
	var host store.AgentHostSettings
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > 500 {
		return host, http.StatusBadRequest, "a reason of at most 500 characters is required"
	}
	if a.Services == nil || a.Services.AgentDefinitions == nil {
		return host, http.StatusServiceUnavailable, "agent definition host unavailable"
	}
	var err error
	// The revision can move between the read and the write; read again and retry.
	for attempt := 0; attempt < 3; attempt++ {
		host, err = a.Services.AgentDefinitions.DisableHost(ctx, id)
		if !errors.Is(err, store.ErrAgentHostRevisionConflict) {
			break
		}
	}
	status, msg := http.StatusOK, ""
	switch {
	case errors.Is(err, sql.ErrNoRows):
		status, msg = http.StatusNotFound, "host settings not found"
	case errors.Is(err, store.ErrAgentHostRevisionConflict):
		status, msg = http.StatusConflict, "host settings kept changing; retry"
	case err != nil:
		status, msg = http.StatusInternalServerError, "retiring host settings failed"
	}
	adminMCPAudit(ctx, auditPrefix+" "+id+" reason="+reason, status)
	return host, status, msg
}

// adminDispatch runs one inner request. Only what the handlers need is copied
// from the carrier: no Authorization, Cookie, Accept or MCP session headers.
func adminDispatch(ctx context.Context, h http.Handler, carrier *http.Request, method, path string, body []byte) (int, []byte) {
	r := httptest.NewRequest(method, path, bytes.NewReader(body)).WithContext(ctx)
	r.Host = carrier.Host
	r.RemoteAddr = carrier.RemoteAddr
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec.Code, rec.Body.Bytes()
}

// adminMCPAudit records which tool ran and on what. The auth middleware only
// sees POST /api/admin/mcp, not the route a tool called.
func adminMCPAudit(ctx context.Context, route string, status int) {
	outcome := "ok"
	if status < 200 || status >= 300 {
		outcome = fmt.Sprintf("failed:%d", status)
	}
	apiauth.AuditFromContext(ctx)(http.MethodPost, route, outcome)
}

// adminMCPSafeID accepts one plain path segment. "." and ".." would be cleaned
// out of the path by the router and answered with a redirect.
func adminMCPSafeID(v string) bool {
	return strings.TrimSpace(v) != "" && v != "." && v != ".." && !strings.ContainsAny(v, "/\\")
}

// adminMCPPrincipal is the same test the agent-management handlers apply: a
// non-agent credential with the agents:manage scope. Individual tools can need
// more (adminMCPTool.scope).
func adminMCPPrincipal(ctx context.Context) (apiauth.Principal, bool) {
	p, ok := apiauth.FromContext(ctx)
	if !ok || !agentManager(p) {
		return p, false
	}
	return p, true
}

func mcpToolResult(status int, body []byte) *mcp.CallToolResult {
	text := strings.TrimSpace(string(body))
	if len(body) > adminMCPMaxResponse {
		return mcpToolError(fmt.Sprintf("HTTP %d: response is %d bytes, over the %d byte limit; narrow the request (limit, cursor)", status, len(body), adminMCPMaxResponse))
	}
	// Anything but 2xx is not a success here, redirects included.
	if status < 200 || status >= 300 {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("HTTP %d: %s", status, text)}}}
	}
	if text == "" {
		text = fmt.Sprintf("HTTP %d", status)
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

func mcpToolError(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: msg}}}
}

// registerAdminMCP serves the admin MCP endpoint. Stateless, one server per
// request: no session state to leak between credentials. The route is already
// admin-only in the auth middleware; it checks again here so the tools are
// never reachable if the route table changes, and each tool call checks once
// more.
func (a *API) registerAdminMCP(mux *http.ServeMux) {
	handler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		return adminMCPServer(a, mux, r)
	}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	mux.HandleFunc(adminMCPPath, func(w http.ResponseWriter, r *http.Request) {
		if _, ok := apiauth.FromContext(r.Context()); !ok {
			a.errorResp(w, http.StatusUnauthorized, "authentication is required")
			return
		}
		if _, ok := adminMCPPrincipal(r.Context()); !ok {
			a.errorResp(w, http.StatusForbidden, "the credential lacks the required scope")
			return
		}
		handler.ServeHTTP(w, r)
	})
}
