package service

import (
	"context"
	"fmt"
	"github.com/hollis-labs/nanite/internal/store"
	"strings"
)

// persistTestActor supplies an ALREADY host-authorized binding to a private
// database. Production has no actor issuer port; this fixture does not claim
// enrollment or convert retained rows. The tested reads use real fresh FKs.
func persistTestActor(ctx context.Context, st *store.Store, p *store.AgentProfile) error {
	slug := strings.ToLower(p.Slug)
	if p.SystemPrompt == "" {
		p.SystemPrompt = "Private prior authored instructions."
	}
	artifact := []byte(fmt.Sprintf("---\nschema_version: \"2\"\ndefinition_id: def:private-%s\nrevision: \"1\"\nname: private-fixture\ndescription: Private already authorized actor fixture.\nbehavior:\n  purpose: Exercise host behavior.\nrequirements: {}\nharness_profile:\n  context: {}\n  permissions:\n    profile: default\ncontinuity:\n  mode: ephemeral\n---\n%s\n", slug, p.SystemPrompt))
	pin, err := st.InstallAgentDefinition(ctx, artifact, nil)
	if err != nil {
		return err
	}
	host, err := st.CreateAgentHostSettings(ctx, store.AgentHostSettings{Slug: slug, Title: p.Name, DefinitionRef: pin, Settings: store.NativeHostSettings{Version: "1", Runtime: "api", Provider: p.DefaultProvider, Model: p.DefaultModel}, Enabled: true, Source: "private-already-authorized-test"})
	if err != nil {
		return err
	}
	actor := "msg://agent/private-fixture/" + slug
	if _, err = st.DB.ExecContext(ctx, `INSERT INTO agent_actor_bindings(actor_uri,host_settings_id,binding_receipt,enabled) VALUES(?,?,?,1)`, actor, host.ID, "private-existing-host-binding"); err != nil {
		return err
	}
	projection, err := st.GetAgentForActor(ctx, actor)
	if err != nil {
		return err
	}
	*p = *projection
	return nil
}

// grantTestActorTool represents a prior host-issued grant; it deliberately does
// not call or soften the refused production grant issuer.
func grantTestActorTool(ctx context.Context, st *store.Store, actor, toolID, via string) error {
	_, err := st.DB.ExecContext(ctx, `INSERT INTO actor_granted_tools(agent_id,tool_id,granted_via,created_at) VALUES(?,?,?,datetime('now'))`, actor, toolID, via)
	return err
}
