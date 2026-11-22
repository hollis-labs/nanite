package selftools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/store"
	mesh "github.com/hollis-labs/substrate/mesh"
)

const agentSourceResolveToolName = "agent_source_resolve"

func agentSourceResolveToolDefinition() mcp.Tool {
	return mcp.Tool{Name: agentSourceResolveToolName, Description: "Read a Nanite host configuration and its pinned immutable definition. The local host ID and slug are not actor addresses; this does not provide a launch grant or actor enrollment.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"agent": map[string]any{"type": "string", "description": "Local host settings ID or slug."}}, "required": []string{"agent"}}}
}

type agentSourceResolveResult struct {
	ID              string             `json:"id"`
	Slug            string             `json:"slug"`
	DefinitionRef   mesh.DefinitionRef `json:"definition_ref"`
	Artifact        string             `json:"artifact"`
	HostSettingsRef struct {
		ID       string `json:"id"`
		Revision string `json:"revision"`
	} `json:"host_settings_ref"`
}

type pinnedAgentSourceReader interface {
	GetAgentHostSettings(context.Context, string) (store.AgentHostSettings, error)
	GetAgentDefinitionArtifact(context.Context, mesh.DefinitionRef) (store.DefinitionArtifact, error)
}

func (at *AgentProfileTools) callAgentSourceResolve(args map[string]any) (*mcp.ToolResult, error) {
	if at == nil || at.Store == nil {
		return mcp.ErrorResult("agent_source_resolve: store not available"), nil
	}
	ref := strings.TrimSpace(strArg(args, "agent", ""))
	if ref == "" {
		return mcp.ErrorResult("agent_source_resolve: `agent` (host ID or slug) is required"), nil
	}
	reader, ok := at.Store.(pinnedAgentSourceReader)
	if !ok {
		return mcp.ErrorResult("agent_source_resolve: pinned definition reader unavailable"), nil
	}
	profile := resolveAgentByRef(at.Store, ref)
	if profile == nil {
		return mcp.ErrorResult(fmt.Sprintf("agent_source_resolve: no host settings found for %q", ref)), nil
	}
	host, err := reader.GetAgentHostSettings(context.Background(), profile.ID)
	if err != nil {
		return mcp.ErrorResult("agent_source_resolve: host settings unavailable"), nil //nolint:nilerr // MCP application failures use IsError payloads, not transport failure.
	}
	artifact, err := reader.GetAgentDefinitionArtifact(context.Background(), host.DefinitionRef)
	if err != nil {
		return mcp.ErrorResult("agent_source_resolve: verified definition unavailable"), nil //nolint:nilerr // MCP application failures use IsError payloads, not transport failure.
	}
	out := agentSourceResolveResult{ID: host.ID, Slug: host.Slug, DefinitionRef: artifact.Ref, Artifact: string(artifact.Data)}
	out.HostSettingsRef.ID = host.ID
	out.HostSettingsRef.Revision = host.Revision
	body, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	return mcp.TextResult(string(body)), nil
}

func resolveAgentByRef(s AgentProfileStore, ref string) *store.AgentProfile {
	if a, err := s.GetAgentBySlug(context.Background(), ref); err == nil && a != nil {
		return a
	}
	if a, err := s.GetAgent(context.Background(), ref); err == nil && a != nil {
		return a
	}
	return nil
}
