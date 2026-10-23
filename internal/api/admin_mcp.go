package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// adminMCPPath is the streamable-HTTP MCP endpoint for agent management. It
// sits under /api/admin, so the route rules already require the admin scope.
const adminMCPPath = "/api/admin/mcp"

// adminMCPTool maps one MCP tool onto an existing HTTP route. Tools add no
// logic of their own: each call is dispatched through the API mux with the
// caller's credential, so validation, reason-required rules and error codes
// are exactly the HTTP ones.
type adminMCPTool struct {
	name, description string
	method, path      string   // path may hold {param} placeholders filled from arguments
	pathParams        []string // argument names consumed by the path
	queryParams       []string // argument names sent as query string
	body              bool     // remaining arguments are sent as the JSON body
	properties        map[string]any
	required          []string
}

func prop(typ, desc string) map[string]any { return map[string]any{"type": typ, "description": desc} }

const adminCredentialNote = " Needs an admin credential (the operator token); agent credentials are refused."

var reasonProp = prop("string", "Why this change is made (required, at most 500 characters); recorded with the change.")

var adminMCPTools = []adminMCPTool{
	{name: "agent_setup", method: "POST", path: "/api/agent-setup", body: true,
		description: "Create or update a Nanite agent in one call: definition, host settings and tool grants. Same input and rules as POST /api/agent-setup. Give an archetype or a full definition artifact; provider selects API or CLI runtime; tools lists native tool grants (granting needs an admin credential); directories are absolute paths on the machine Nanite runs on." + adminCredentialNote,
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
		}, required: []string{"provider", "tools", "reason"}},
	{name: "agent_list", method: "GET", path: "/api/native-agents",
		description: "List Nanite native agents (definition, host settings, grants)." + adminCredentialNote},
	{name: "agent_get", method: "GET", path: "/api/native-agents/{id}", pathParams: []string{"id"},
		description: "Show one Nanite native agent by id." + adminCredentialNote,
		properties:  map[string]any{"id": prop("string", "Agent id.")}, required: []string{"id"}},
	{name: "host_settings_list", method: "GET", path: "/api/agent-host-settings",
		description: "List agent host settings (mutable execution inputs)." + adminCredentialNote},
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
	{name: "authority_list", method: "GET", path: "/api/native-authority",
		description: "List native authorities (actors that hold tool grants)." + adminCredentialNote},
	{name: "authority_get", method: "GET", path: "/api/native-authority/{id}", pathParams: []string{"id"},
		description: "Show one native authority and its grants." + adminCredentialNote,
		properties:  map[string]any{"id": prop("string", "Authority id.")}, required: []string{"id"}},
	{name: "tool_grant", method: "PUT", path: "/api/native-authority/{id}/tools/{tool}", pathParams: []string{"id", "tool"}, body: true,
		description: "Grant a native tool to an authority. Granting requires an admin credential." + adminCredentialNote,
		properties:  map[string]any{"id": prop("string", "Authority id."), "tool": prop("string", "Tool name."), "reason": reasonProp},
		required:    []string{"id", "tool", "reason"}},
	{name: "tool_ungrant", method: "DELETE", path: "/api/native-authority/{id}/tools/{tool}", pathParams: []string{"id", "tool"}, body: true,
		description: "Withdraw one tool grant from an authority." + adminCredentialNote,
		properties:  map[string]any{"id": prop("string", "Authority id."), "tool": prop("string", "Tool name."), "reason": reasonProp},
		required:    []string{"id", "tool"}},
	{name: "authority_revoke", method: "POST", path: "/api/native-authority/{id}/revoke", pathParams: []string{"id"}, body: true,
		description: "Revoke an authority and every grant it holds." + adminCredentialNote,
		properties:  map[string]any{"id": prop("string", "Authority id."), "reason": reasonProp}, required: []string{"id", "reason"}},
	{name: "session_start", method: "POST", path: "/api/agent/v1/sessions", body: true,
		description: "Start an agent session from a pinned definition and optional host settings. work_root, when set, must be an absolute path on the machine Nanite runs on." + adminCredentialNote,
		properties: map[string]any{
			"definition_ref":  map[string]any{"type": "object", "description": "Pinned definition reference."},
			"model_selection": map[string]any{"type": "object"},
			"host_settings":   map[string]any{"type": "object", "description": "Host settings reference: id and revision."},
			"project_id":      prop("string", "Project id."), "title": prop("string", "Session title."),
			"work_root": prop("string", "Absolute working directory on the Nanite machine."),
			"metadata":  map[string]any{"type": "object"},
		}, required: []string{"definition_ref"}},
	{name: "session_list", method: "GET", path: "/api/agent/v1/sessions",
		description: "List agent sessions." + adminCredentialNote},
	{name: "session_get", method: "GET", path: "/api/agent/v1/sessions/{id}", pathParams: []string{"id"},
		description: "Show one agent session." + adminCredentialNote,
		properties:  map[string]any{"id": prop("string", "Session view id.")}, required: []string{"id"}},
	{name: "session_history", method: "GET", path: "/api/agent/v1/sessions/{id}/messages", pathParams: []string{"id"},
		description: "Read a session's message history." + adminCredentialNote,
		properties:  map[string]any{"id": prop("string", "Session view id.")}, required: []string{"id"}},
	{name: "session_stop", method: "POST", path: "/api/agent/v1/sessions/{id}/stop", pathParams: []string{"id"}, body: true,
		description: "Stop an agent session." + adminCredentialNote,
		properties:  map[string]any{"id": prop("string", "Session view id.")}, required: []string{"id"}},
	{name: "turn_send", method: "POST", path: "/api/agent/v1/sessions/{id}/turns", pathParams: []string{"id"}, body: true,
		description: "Send a turn (a user message) to a session." + adminCredentialNote,
		properties:  map[string]any{"id": prop("string", "Session view id."), "content": prop("string", "Message text.")}, required: []string{"id"}},
	{name: "turn_get", method: "GET", path: "/api/agent/v1/sessions/{id}/turns/{turnId}", pathParams: []string{"id", "turnId"},
		description: "Show a turn's state." + adminCredentialNote,
		properties:  map[string]any{"id": prop("string", "Session view id."), "turnId": prop("string", "Turn id.")}, required: []string{"id", "turnId"}},
	{name: "turn_cancel", method: "POST", path: "/api/agent/v1/sessions/{id}/turns/{turnId}/cancel", pathParams: []string{"id", "turnId"}, body: true,
		description: "Cancel a running turn." + adminCredentialNote,
		properties:  map[string]any{"id": prop("string", "Session view id."), "turnId": prop("string", "Turn id.")}, required: []string{"id", "turnId"}},
}

// adminMCPServer builds the tool set. dispatch runs one request through the
// API mux; each call carries the caller's request context, so the credential
// that reached /api/admin/mcp is the one every wrapped route sees.
func adminMCPServer(dispatch http.Handler, carrier *http.Request) *mcp.Server {
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
	// host_settings_retire reads the current host, then writes it back disabled.
	srv.AddTool(&mcp.Tool{Name: "host_settings_retire", Description: "Retire an agent by disabling its host settings (enabled=false at the current revision)." + adminCredentialNote,
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"id": prop("string", "Host settings id.")}, "required": []string{"id"}}},
		func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return retireHost(ctx, dispatch, carrier, req.Params.Arguments)
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
	path := t.path
	for _, p := range t.pathParams {
		v, _ := args[p].(string)
		if strings.TrimSpace(v) == "" {
			return mcpToolError(p + " is required"), nil
		}
		path = strings.ReplaceAll(path, "{"+p+"}", url.PathEscape(v))
		delete(args, p)
	}
	var body []byte
	if t.body {
		body, _ = json.Marshal(args)
	}
	status, out := adminDispatch(ctx, h, carrier, t.method, path, body)
	return mcpToolResult(status, out), nil
}

func retireHost(ctx context.Context, h http.Handler, carrier *http.Request, raw json.RawMessage) (*mcp.CallToolResult, error) {
	var args struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &args); err != nil || strings.TrimSpace(args.ID) == "" {
		return mcpToolError("id is required"), nil
	}
	status, out := adminDispatch(ctx, h, carrier, http.MethodGet, "/api/agent-host-settings", nil)
	if status >= 400 {
		return mcpToolResult(status, out), nil
	}
	var list []map[string]any
	if err := json.Unmarshal(out, &list); err != nil {
		var wrapped struct {
			Items []map[string]any `json:"items"`
		}
		if json.Unmarshal(out, &wrapped) != nil {
			return mcpToolError("unexpected host settings list shape"), nil
		}
		list = wrapped.Items
	}
	for _, host := range list {
		if id, _ := host["id"].(string); id != args.ID {
			continue
		}
		body := map[string]any{"slug": host["slug"], "title": host["title"], "definition_ref": host["definition_ref"], "settings": host["settings"], "enabled": false}
		if rev, ok := host["revision"].(string); ok {
			body["revision"] = rev
		}
		b, _ := json.Marshal(body)
		status, out = adminDispatch(ctx, h, carrier, http.MethodPut, "/api/agent-host-settings/"+url.PathEscape(args.ID), b)
		return mcpToolResult(status, out), nil
	}
	return mcpToolError("host settings not found"), nil
}

func adminDispatch(_ context.Context, h http.Handler, carrier *http.Request, method, path string, body []byte) (int, []byte) {
	r := httptest.NewRequest(method, path, bytes.NewReader(body)).WithContext(carrier.Context())
	r.Header = carrier.Header.Clone()
	r.RemoteAddr = carrier.RemoteAddr
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	} else {
		r.Header.Del("Content-Type")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec.Code, rec.Body.Bytes()
}

func mcpToolResult(status int, body []byte) *mcp.CallToolResult {
	text := strings.TrimSpace(string(body))
	if status >= 400 {
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
// request: no session state to leak between credentials.
func (a *API) registerAdminMCP(mux *http.ServeMux) {
	handler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		return adminMCPServer(mux, r)
	}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	mux.Handle(adminMCPPath, handler)
}
