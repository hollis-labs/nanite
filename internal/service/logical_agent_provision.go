package service

import (
	"context"
	"errors"
	"strings"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/substrate/mesh/agentdef"
)

var ErrLogicalAgentProvisionInput = errors.New("logical provisioning requires an explicit name, slug and definition pin")

// ProvisionGeneralChatRequest creates fresh host settings, not a
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
// fresh host settings pinned to immutable content.
// The definition pin is an immutable reference; host execution inputs can change
// independently. This does not create an actor or replay profile grants.
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
	saved, err := s.store.CreateAgentHostSettings(ctx, store.AgentHostSettings{Slug: req.Slug, Title: req.Name, DefinitionRef: req.DefinitionRef.MeshRef(), Settings: store.NativeHostSettings{Version: "1", Runtime: "api", Provider: model.Provider, Model: model.Model}, Enabled: true, Source: "operator"})
	if err != nil {
		if errors.Is(err, store.ErrAgentHostSlugConflict) {
			return nil, ErrManagedSlugExists
		}
		return nil, err
	}
	projection, err := s.store.GetAgent(ctx, saved.ID)
	if err != nil {
		return nil, err
	}
	return &ProvisionGeneralChatResult{Agent: &AgentConfigResult{Profile: projection, Class: s.Classify(projection), Revision: saved.Revision}, DefinitionRef: req.DefinitionRef}, nil

}
