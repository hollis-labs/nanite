package storetest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hollis-labs/nanite/internal/agentpolicy"
	"github.com/hollis-labs/nanite/internal/store"
)

// PriorAuthorizedActor supplies a prior host-authorized binding in a private
// test database. It is never a production issuer, converter or enrollment port.
func PriorAuthorizedActor(ctx context.Context, st *store.Store, p *store.AgentProfile) error {
	slug := strings.ToLower(p.Slug)
	if p.SystemPrompt == "" {
		p.SystemPrompt = "Private prior authored instructions."
	}
	artifact := []byte(fmt.Sprintf("---\nschema_version: \"2\"\ndefinition_id: def:private-%s\nrevision: \"1\"\nname: private-fixture\ndescription: Private already authorized actor fixture.\nbehavior:\n  purpose: Exercise host behavior.\nrequirements: {}\nharness_profile:\n  context: {}\n  permissions:\n    profile: default\ncontinuity:\n  mode: ephemeral\n---\n%s\n", slug, p.SystemPrompt))
	if p.DefinitionPolicy != nil {
		data, err := json.Marshal(p.DefinitionPolicy)
		if err != nil {
			return err
		}
		ext := fmt.Sprintf("extensions:\n  %s:\n    version: \"1\"\n    area: harness_profile\n    mandatory: true\n    data: %s\n", agentpolicy.NativeNamespace, data)
		artifact = []byte(strings.Replace(string(artifact), "continuity:\n", ext+"continuity:\n", 1))
	}
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

// PriorToolGrant supplies an already issued grant for a private prior binding.
func PriorToolGrant(ctx context.Context, st *store.Store, actor, toolID, via string) error {
	if _, err := st.GetAgentForActor(ctx, actor); err != nil {
		return err
	}
	_, err := st.DB.ExecContext(ctx, `INSERT OR IGNORE INTO actor_granted_tools(agent_id,tool_id,granted_via,created_at) VALUES(?,?,?,datetime('now'))`, actor, toolID, via)
	return err
}
