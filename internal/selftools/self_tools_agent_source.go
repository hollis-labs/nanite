package selftools

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/store"
)

// self_tools_agent_source.go — the C1 agent-source resolver endpoint
// (S5 platform-reshape, locked decision D2).
//
// # Why this tool exists
//
// The directory registry holds resolver HANDLES, never agent bodies (D2):
// system_prompt / tools / modes never live in the directory. Nanite
// registers an `agent-source` contract whose ResolverHandle points back
// at this tool. When another consumer needs to compose a Nanite agent it
// calls back through the handle — protocol http, the loopback
// /api/tools/call endpoint, operation `agent_source_resolve` — and this
// tool composes the agent from Nanite's own `agent_profiles` SoT.
//
// This is the directory-facing READ side only. Nanite's own self-
// resolution (agentProfileResolver.GetOrDefault / launcher.defaultProfiles)
// stays store-backed and never round-trips through this handle — D2 is
// about serving agents to OTHER consumers, not Nanite resolving itself.

// agentSourceResolveToolName is the operation name carried on the
// registered ResolverHandle. The startup registration (cmd/nanite) and
// the dispatch switch must agree on this exact string.
const agentSourceResolveToolName = "agent_source_resolve"

// agentSourceResolveToolDefinition is the tool surfaced to MCP callers.
// It is intentionally narrow: it composes one agent from the SoT and
// returns an agentlaunch.AgentSpec-equivalent payload. It does NOT
// create, update, or delete — agent_create / agent_update own that.
func agentSourceResolveToolDefinition() mcp.Tool {
	return mcp.Tool{
		Name: agentSourceResolveToolName,
		Description: "Resolve a Nanite agent profile into a launch-ready agent spec. " +
			"This is the agent-source resolver endpoint other launch consumers call back " +
			"into after discovering Nanite's `agent-source` handle in the directory registry.\n\n" +
			"**When to use:** When an external consumer needs to compose a Nanite-owned " +
			"agent (system prompt, tools, modes) for a launch. The directory holds only the " +
			"handle; this tool returns the actual composed body.\n\n" +
			"**Required context:** `agent` — the agent id or slug to resolve.\n\n" +
			"**Output shape:** A JSON object {id, name, slug, system_prompt, default_model, " +
			"default_provider, tools, modes, agent_spec} where `agent_spec` is the " +
			"agentlaunch.AgentSpec-compatible identity projection.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"agent": map[string]any{
					"type":        "string",
					"description": "Agent id or slug to resolve from the agent_profiles source of truth.",
				},
			},
			"required": []string{"agent"},
		},
	}
}

// agentSpecPayload is the agentlaunch.AgentSpec-equivalent identity
// projection embedded under `agent_spec` in the resolve result. The
// field shape mirrors agentlaunch.AgentSpec (id / name / labels) so a
// caller can decode it straight into that type without a Nanite import.
type agentSpecPayload struct {
	ID     string            `json:"id"`
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels,omitempty"`
}

// agentSourceResolveResult is the full body the resolver returns. It
// carries the composed agent (the D2 "content" the directory must never
// hold) plus the agentlaunch-compatible identity projection.
type agentSourceResolveResult struct {
	ID              string           `json:"id"`
	Name            string           `json:"name"`
	Slug            string           `json:"slug"`
	Description     string           `json:"description,omitempty"`
	SystemPrompt    string           `json:"system_prompt"`
	DefaultModel    string           `json:"default_model,omitempty"`
	DefaultProvider string           `json:"default_provider,omitempty"`
	Tools           string           `json:"tools,omitempty"`
	Modes           string           `json:"modes,omitempty"`
	MCPServers      string           `json:"mcp_servers,omitempty"`
	AgentSpec       agentSpecPayload `json:"agent_spec"`
}

// callAgentSourceResolve composes a Nanite agent from the agent_profiles
// SoT and returns it in an agentlaunch-compatible shape. It accepts an
// agent id OR slug — a caller holding a directory handle only knows the
// registered name, which is the slug.
func (st *SelfToolsTransport) callAgentSourceResolve(args map[string]any) (*mcp.ToolResult, error) {
	if st == nil || st.Store == nil {
		return mcp.ErrorResult("agent_source_resolve: store not available"), nil
	}
	ref := strings.TrimSpace(strArg(args, "agent", ""))
	if ref == "" {
		return mcp.ErrorResult("agent_source_resolve: `agent` (id or slug) is required"), nil
	}

	agent := resolveAgentByRef(st.Store, ref)
	if agent == nil {
		return mcp.ErrorResult(fmt.Sprintf("agent_source_resolve: no agent profile found for %q", ref)), nil
	}

	out := agentSourceResolveResult{
		ID:              agent.ID,
		Name:            agent.Name,
		Slug:            agent.Slug,
		Description:     agent.Description,
		SystemPrompt:    agent.SystemPrompt,
		DefaultModel:    agent.DefaultModel,
		DefaultProvider: agent.DefaultProvider,
		Tools:           agent.Tools,
		Modes:           agent.Modes,
		MCPServers:      agent.MCPServers,
		AgentSpec: agentSpecPayload{
			ID:   agentSpecID(agent),
			Name: agent.Name,
		},
	}

	body, err := json.Marshal(out)
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("agent_source_resolve: marshal result: %v", err)), nil
	}
	return mcp.TextResult(string(body)), nil
}

// resolveAgentByRef looks an agent up by slug first (the directory
// registers an agent under its slug), then falls back to id. A failed
// lookup on either path returns nil rather than an error so the caller
// emits a single clean not-found message.
func resolveAgentByRef(s *store.Store, ref string) *store.AgentProfile {
	if a, err := s.GetAgentBySlug(ref); err == nil && a != nil {
		return a
	}
	if a, err := s.GetAgent(ref); err == nil && a != nil {
		return a
	}
	return nil
}

// agentSpecID picks the stable agentlaunch.AgentSpec.ID for an agent.
// The slug is the directory-facing identity (it is the name an
// agent-source record registers under), so it is preferred; the row id
// is the fallback for an agent with no slug.
func agentSpecID(a *store.AgentProfile) string {
	if a.Slug != "" {
		return a.Slug
	}
	return a.ID
}
