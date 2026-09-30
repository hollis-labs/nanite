package service

import (
	"context"
	"errors"

	"github.com/hollis-labs/nanite/internal/store"
)

// ── context resolvers ──
//
// agent_context_resolvers rows fill an agent's dynamic context slots at
// launch (a command's output or an HTTP response). The store validates a
// row's shape on every write.

// ErrContextResolverNotOwned reports a context resolver that exists but
// belongs to a different agent than the one addressed.
var ErrContextResolverNotOwned = errors.New("context resolver belongs to a different agent")

// ContextResolverPatch is an update to a context resolver. A nil field keeps
// the stored value.
type ContextResolverPatch struct {
	SlotName       *string
	Kind           *string
	Run            *string
	CWD            *string
	Timeout        *string
	URL            *string
	HeadersJSON    *string
	ResponseFormat *string
	JSONPath       *string
	Enabled        *bool
}

// ListContextResolvers returns every resolver bound to the agent, enabled or
// not.
func (s *AgentCapabilitiesService) ListContextResolvers(ctx context.Context, agentID string) ([]store.AgentContextResolver, error) {
	return s.store.ListAgentContextResolvers(ctx, agentID)
}

// OwnedContextResolver returns the resolver if it belongs to agentID. A
// missing resolver is store.ErrAgentContextResolverNotFound; one bound to a
// different agent is ErrContextResolverNotOwned.
func (s *AgentCapabilitiesService) OwnedContextResolver(ctx context.Context, agentID, id string) (*store.AgentContextResolver, error) {
	row, err := s.store.GetAgentContextResolver(ctx, id)
	if err != nil {
		return nil, err
	}
	if row.AgentID != agentID {
		return nil, ErrContextResolverNotOwned
	}
	return row, nil
}

// CreateContextResolver inserts a resolver and returns the stored row. A
// rejected insert is a *CapabilityWriteError; a failure to read the new row
// back is returned as is.
func (s *AgentCapabilitiesService) CreateContextResolver(ctx context.Context, row store.AgentContextResolver) (*store.AgentContextResolver, error) {
	id, err := s.store.InsertAgentContextResolver(ctx, row)
	if err != nil {
		return nil, &CapabilityWriteError{Err: err}
	}
	return s.store.GetAgentContextResolver(ctx, id)
}

// UpdateContextResolver applies patch to the stored row current (copy then
// overwrite, so every field the patch leaves nil keeps its value) and
// returns the row as stored afterwards. A resolver deleted in between is
// store.ErrAgentContextResolverNotFound; any other rejected write is a
// *CapabilityWriteError; a failure to read the row back is returned as is.
// Read, write and re-read are separate steps, not one transaction.
func (s *AgentCapabilitiesService) UpdateContextResolver(ctx context.Context, current *store.AgentContextResolver, patch ContextResolverPatch) (*store.AgentContextResolver, error) {
	updated := *current
	if patch.SlotName != nil {
		updated.SlotName = *patch.SlotName
	}
	if patch.Kind != nil {
		updated.Kind = *patch.Kind
	}
	if patch.Run != nil {
		updated.Run = *patch.Run
	}
	if patch.CWD != nil {
		updated.CWD = *patch.CWD
	}
	if patch.Timeout != nil {
		updated.Timeout = *patch.Timeout
	}
	if patch.URL != nil {
		updated.URL = *patch.URL
	}
	if patch.HeadersJSON != nil {
		updated.HeadersJSON = *patch.HeadersJSON
	}
	if patch.ResponseFormat != nil {
		updated.ResponseFormat = *patch.ResponseFormat
	}
	if patch.JSONPath != nil {
		updated.JSONPath = *patch.JSONPath
	}
	if patch.Enabled != nil {
		updated.Enabled = *patch.Enabled
	}

	if err := s.store.UpdateAgentContextResolver(ctx, updated); err != nil {
		if errors.Is(err, store.ErrAgentContextResolverNotFound) {
			return nil, err
		}
		return nil, &CapabilityWriteError{Err: err}
	}
	return s.store.GetAgentContextResolver(ctx, current.ID)
}

// DeleteContextResolver removes a resolver.
func (s *AgentCapabilitiesService) DeleteContextResolver(ctx context.Context, id string) error {
	return s.store.DeleteAgentContextResolver(ctx, id)
}
