package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/substrate/mesh/agentdef"
)

var ErrLogicalAgentProvisionInput = errors.New("logical provisioning requires an explicit name, slug and definition pin")

// ProvisionGeneralChatRequest creates a retained logical profile, not a
// cognitive session or actor enrollment. No grant, identity or settings map
// is accepted from this request.
type ProvisionGeneralChatRequest struct {
	Name           string          `json:"name"`
	Slug           string          `json:"slug"`
	DefinitionRef  DefinitionRef   `json:"definition_ref"`
	ModelSelection *ModelSelection `json:"model_selection,omitempty"`
}

type ProvisionGeneralChatResult struct {
	Agent         *AgentConfigResult `json:"agent"`
	DefinitionRef DefinitionRef      `json:"definition_ref"`
}

// ProvisionGeneralChat resolves actual agentdef content and creates an
// editable logical profile through the existing atomic configuration writer.
// The definition pin in settings records creation provenance; it is not an
// authority token or a promise that later profile edits track that definition.
func (s *AgentConfigService) ProvisionGeneralChat(ctx context.Context, host *CognitiveViews, req ProvisionGeneralChatRequest) (*ProvisionGeneralChatResult, error) {
	if strings.TrimSpace(req.Name) == "" || len(req.Name) > 256 || len(req.Slug) > 128 {
		return nil, ErrLogicalAgentProvisionInput
	}
	if err := req.DefinitionRef.Validate(); err != nil {
		return nil, ErrLogicalAgentProvisionInput
	}
	if host == nil || host.Resolver == nil || host.Models == nil {
		return nil, ErrUnsupportedDefinition
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return nil, contextErr
	}
	verified, err := host.Resolver.Resolve(ctx, req.DefinitionRef)
	if err != nil {
		return nil, err
	}
	if verified.Ref != req.DefinitionRef || verified.Definition == nil || verified.Definition.DefinitionID != req.DefinitionRef.DefinitionID || verified.Definition.Revision != req.DefinitionRef.Revision {
		return nil, ErrDefinitionDigestMismatch
	}
	digest, err := agentdef.Digest(verified.Definition)
	if err != nil {
		return nil, err
	}
	if digest != req.DefinitionRef.SemanticDigest {
		return nil, ErrDefinitionDigestMismatch
	}
	cfg, err := MapChatDefinition(verified)
	if err != nil {
		return nil, err
	}
	// Retained profiles do not apply the native view's read-only posture. Refuse
	// that semantic rather than storing an ignored policy label as enforcement.
	if cfg.PermissionProfile != "default" {
		return nil, ErrUnsupportedDefinition
	}
	model, err := host.Models.AuthorizeModel(ctx, req.DefinitionRef, req.ModelSelection)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(model.Provider) == "" || strings.TrimSpace(model.Model) == "" {
		return nil, ErrUnsupportedModel
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return nil, contextErr
	}
	settings, err := json.Marshal(struct {
		DefinitionRef DefinitionRef `json:"provisioned_definition_ref"`
	}{req.DefinitionRef})
	if err != nil {
		return nil, err
	}
	profile := &store.AgentProfile{Name: req.Name, Slug: req.Slug, SystemPrompt: cfg.Instructions, DefaultProvider: model.Provider, DefaultModel: model.Model, Settings: string(settings), Tools: "[]", RoleTools: "[]", RoleSkills: "[]", MCPServers: "[]", ToolPermissions: "{}", CanExecute: false, Durable: false}
	saved, err := s.CreateWithAssignments(ctx, profile, nil, AgentAssignments{})
	if err != nil {
		return nil, err
	}
	return &ProvisionGeneralChatResult{Agent: saved, DefinitionRef: req.DefinitionRef}, nil
}
