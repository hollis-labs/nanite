package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/hollis-labs/nanite/internal/agentpolicy"
	mesh "github.com/hollis-labs/substrate/mesh"
	"github.com/hollis-labs/substrate/mesh/agentdef"
)

// projectAgentHost constructs the compatibility-shaped read DTO from one fresh
// host row and pinned definition. The DTO is never an editable intrinsic record,
// actor, role cascade or implicit source of grants.
func (s *Store) projectAgentHost(ctx context.Context, h AgentHostSettings) (*AgentProfile, error) {
	a, err := s.GetAgentDefinitionArtifact(ctx, h.DefinitionRef)
	if err != nil {
		return nil, err
	}
	d, err := agentdef.Parse(a.Data, agentpolicy.Option())
	if err != nil {
		return nil, err
	}
	if operationErr := agentpolicy.ValidateExecution(d); operationErr != nil {
		return nil, operationErr
	}
	// The actor runtime port has no enforced read-only posture. A native view
	// may narrow permissions, but a compatibility DTO cannot imply enforcement.
	if d.HarnessProfile.Permissions.Profile != "default" {
		return nil, agentpolicy.ErrUnsupportedExecution
	}
	digest, err := agentdef.Digest(d)
	if err != nil {
		return nil, err
	}
	if digest != h.DefinitionRef.Digest {
		return nil, ErrDefinitionContent
	}
	p := agentpolicy.NativePolicy{}.Defaults()
	if e, ok := d.Extensions[agentpolicy.NativeNamespace]; ok {
		p, err = agentpolicy.DecodeNative(e)
		if err != nil {
			return nil, err
		}
		p = p.Defaults()
	}
	var bundle *agentpolicy.ReflexBundle
	if e, ok := d.Extensions[agentpolicy.ReflexNamespace]; ok {
		r, err := agentpolicy.DecodeReflex(e)
		if err != nil {
			return nil, err
		}
		body, err := s.GetAgentDefinitionResource(ctx, h.DefinitionRef, r.Bundle)
		if err != nil {
			return nil, err
		}
		b, err := agentpolicy.ParseReflexBundle(body, r.Bundle)
		if err != nil {
			return nil, err
		}
		for _, rule := range b.Rules {
			if rule.Action.Kind == "force_tool_choice" {
				return nil, ErrVerifiedActorRequired
			}
		}
		bundle = &b
	}
	parts := []string{d.Behavior.Purpose, d.Body}
	for _, refs := range [][]agentdef.Ref{d.Behavior.Instructions, d.Behavior.SOPs} {
		for _, ref := range refs {
			body, err := s.GetAgentDefinitionResource(ctx, h.DefinitionRef, ref)
			if err != nil {
				return nil, err
			}
			parts = append(parts, string(body))
		}
	}
	if d.Behavior.Completion != "" {
		parts = append(parts, "Completion: "+d.Behavior.Completion)
	}
	status := "active"
	if !h.Enabled {
		status = "disabled"
	}
	return &AgentProfile{ID: h.ID, Name: h.Title, Slug: h.Slug, Revision: h.Revision, Source: h.Source, PluginID: h.PluginID, Status: status, SystemPrompt: strings.Join(parts, "\n\n"), Class: p.Class, DefinitionPolicy: &p, DefinitionReflex: bundle, NativeHost: &h.Settings, RuntimeKind: h.Settings.Runtime, Protocol: h.Settings.Protocol, Transport: h.Settings.Transport, DefaultProvider: h.Settings.Provider, DefaultModel: h.Settings.Model, Tools: "[]", Settings: "{}", Constraints: "{}", Tags: "[]", Directories: "[]", MCPServers: "[]"}, nil
}

var ErrImmutableAgentProfile = errors.New("mutable agent profiles are retired; author a pinned definition and configure host settings")

// GetAgentForActor resolves only an existing enabled binding receipt. A caller's
// claimed URI or host UUID cannot create that binding. The host has no enrollment
// writer until a verified Tether binding port is actually adopted.
func (s *Store) GetAgentForActor(ctx context.Context, actorURI string) (*AgentProfile, error) {
	if err := (mesh.Actor{URN: mesh.URN(actorURI), Kind: mesh.ActorAgent}).Validate(); err != nil {
		return nil, ErrVerifiedActorRequired
	}
	var hostID, receipt string
	err := s.DB.QueryRowContext(ctx, `SELECT b.host_settings_id,b.binding_receipt FROM agent_actor_bindings b JOIN agent_host_settings h ON h.id=b.host_settings_id WHERE b.actor_uri=? AND b.enabled=1 AND h.enabled=1`, actorURI).Scan(&hostID, &receipt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrVerifiedActorRequired
	}
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(receipt) == "" {
		return nil, ErrVerifiedActorRequired
	}
	p, err := s.GetAgent(ctx, hostID)
	if err != nil {
		return nil, err
	}
	p.ID = actorURI
	return p, nil
}
