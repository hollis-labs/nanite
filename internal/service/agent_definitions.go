package service

import (
	"context"
	"errors"

	mesh "github.com/hollis-labs/libs/mesh/core"
	"github.com/hollis-labs/nanite/internal/store"
)

// AgentDefinitions separates explicit immutable authoring from mutable host
// execution inputs. Neither operation enrolls actors or grants tools/skills.
type AgentDefinitions struct {
	Store  *store.Store
	Models ModelAuthorizer
}

func (s *AgentDefinitions) Install(ctx context.Context, data []byte, resources []store.DefinitionResource) (mesh.DefinitionRef, error) {
	return s.Store.InstallAgentDefinition(ctx, data, resources)
}
func (s *AgentDefinitions) List(ctx context.Context) ([]store.DefinitionArtifact, error) {
	return s.Store.ListAgentDefinitionArtifacts(ctx)
}
func (s *AgentDefinitions) ListHosts(ctx context.Context) ([]store.AgentHostSettings, error) {
	return s.Store.ListAgentHostSettings(ctx)
}
func (s *AgentDefinitions) GetHost(ctx context.Context, id string) (store.AgentHostSettings, error) {
	return s.Store.GetAgentHostSettings(ctx, id)
}
func (s *AgentDefinitions) DeleteHost(ctx context.Context, id, revision string) error {
	return s.Store.DeleteAgentHostSettings(ctx, id, revision)
}
func (s *AgentDefinitions) CreateHost(ctx context.Context, h store.AgentHostSettings) (store.AgentHostSettings, error) {
	if err := s.validateHostModel(ctx, h); err != nil {
		return h, err
	}
	h.Source = "operator"
	h.PluginID = ""
	return s.Store.CreateAgentHostSettings(ctx, h)
}
func (s *AgentDefinitions) UpdateHost(ctx context.Context, h store.AgentHostSettings, expected string) (store.AgentHostSettings, error) {
	if err := s.validateHostModel(ctx, h); err != nil {
		return h, err
	}
	return s.Store.UpdateAgentHostSettings(ctx, h, expected)
}

// DisableHost turns a host off without re-validating its model or definition,
// which may have gone stale; disabling must always be possible. It reads the
// current revision and swaps on it, so a concurrent edit surfaces as a
// revision conflict for the caller to retry.
func (s *AgentDefinitions) DisableHost(ctx context.Context, id string) (store.AgentHostSettings, error) {
	h, err := s.Store.GetAgentHostSettings(ctx, id)
	if err != nil {
		return h, err
	}
	return s.Store.DisableAgentHostSettings(ctx, h.ID, h.Revision)
}
func (s *AgentDefinitions) validateHostModel(ctx context.Context, h store.AgentHostSettings) error {
	if err := h.Settings.Validate(); err != nil {
		return err
	}
	if _, err := s.Store.GetAgentDefinitionArtifact(ctx, h.DefinitionRef); err != nil {
		return err
	}
	if h.Settings.Runtime == "api" {
		if s.Models == nil {
			return unsupportedModelCause(CauseModelAuthorizerMissing, "no model authorizer configured on this server")
		}
		model, err := s.Models.AuthorizeModel(ctx, DefinitionRefFromMesh(h.DefinitionRef), &ModelSelection{Provider: h.Settings.Provider, Model: h.Settings.Model})
		if err != nil {
			return err
		}
		if model.Provider != h.Settings.Provider || model.Model != h.Settings.Model {
			return unsupportedModelCause(CauseModelSelectionMismatch, "authorized selection %s/%s differs from host settings %s/%s", model.Provider, model.Model, h.Settings.Provider, h.Settings.Model)
		}
	}
	if h.Settings.Runtime == "cli" && (h.Settings.Provider == "" || h.Settings.Model == "") {
		return errors.New("CLI host settings require explicit provider and model")
	}
	return nil
}
