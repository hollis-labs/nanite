package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/hollis-labs/libs/mesh/core/agentdef"
	"github.com/hollis-labs/nanite/internal/agentdir"
	"github.com/hollis-labs/nanite/internal/agentpolicy"
	"github.com/hollis-labs/nanite/internal/store"
)

// AgentLoadService publishes an agent directory as a definition and a host
// settings row. It is a reload: an explicit operator action, never a side
// effect of creating a session.
type AgentLoadService struct {
	Store *store.Store
}

// AgentLoadOptions are the operator's choices for one load.
type AgentLoadOptions struct {
	// Force publishes a new revision of a pinned agent.
	Force bool
	// DryRun reports what would change and writes nothing.
	DryRun bool
	// GrantFrom names host settings (id or slug) whose tool grants are re-granted
	// to the new revision. Empty means the new revision has no authority.
	GrantFrom string
	Reason    string
}

// AgentLoadResponse is the outcome of a load and the body to start a session.
type AgentLoadResponse struct {
	Path           string          `json:"path"`
	Agent          string          `json:"agent"`
	Mode           string          `json:"mode,omitempty"`
	DryRun         bool            `json:"dry_run,omitempty"`
	Changed        bool            `json:"changed"`
	DefinitionRef  DefinitionRef   `json:"definition_ref"`
	HostSettings   HostSettingsRef `json:"host_settings"`
	HostSettingsID string          `json:"host_settings_id"`
	Slug           string          `json:"slug"`
	Previous       string          `json:"previous,omitempty"`
	Diff           []string        `json:"diff"`
	AuthorityID    string          `json:"authority_id,omitempty"`
	Tools          []string        `json:"tools"`
	// Notes are things the operator should know: skills present but not
	// installed, tools that need granting before the agent can launch.
	Notes []string `json:"notes,omitempty"`
	// Fields says what a launch does with each field agent.md declares:
	// applied, recorded, unmet (launches without it) or refused.
	Fields         []agentpolicy.FieldReport `json:"fields,omitempty"`
	Requests       store.AgentRequests       `json:"requests"`
	SessionRequest map[string]any            `json:"session_request"`
}

// Load reads the directory, checks that the native runtime could launch what it
// declares, and records it. Loading the same state twice changes nothing.
func (s *AgentLoadService) Load(ctx context.Context, dir *agentdir.Directory, opts AgentLoadOptions, grantedBy string) (AgentLoadResponse, error) {
	var out AgentLoadResponse
	settings := store.NativeHostSettings{Version: "1", Runtime: dir.Host.Runtime, Provider: dir.Host.Provider, Model: dir.Host.Model, Directories: dir.Host.Directories, HarnessProfile: dir.Host.HarnessProfile, MCPServers: dir.Host.MCPServers}
	if err := settings.Validate(); err != nil {
		return out, fmt.Errorf("%w: host.yaml: %w", ErrAgentSetupInvalid, err)
	}
	digest, err := agentdef.Digest(dir.Parsed)
	if err != nil {
		return out, err
	}
	byURI := map[string][]byte{}
	resources := make([]store.DefinitionResource, 0, len(dir.Resources))
	for _, r := range dir.Resources {
		byURI[r.URI] = r.Content
		resources = append(resources, store.DefinitionResource{URI: r.URI, Content: r.Content})
	}
	chatCfg, err := MapChatDefinition(VerifiedDefinition{
		Ref:        DefinitionRef{DefinitionID: dir.Parsed.DefinitionID, Revision: dir.Parsed.Revision, SemanticDigest: digest},
		Definition: dir.Parsed,
		ReadResource: func(_ context.Context, ref agentdef.Ref) ([]byte, error) {
			if body, ok := byURI[ref.URI]; ok {
				return body, nil
			}
			return nil, fmt.Errorf("resource %q was not supplied", ref.URI)
		},
	})
	if err != nil {
		return out, setupInvalid("this definition cannot run on native chat: %v", err)
	}
	if settings.Runtime == "cli" {
		if err = AdmitCLIHost(settings, chatCfg.PermissionProfile); err != nil {
			return out, setupInvalid("this agent cannot run as a CLI agent: %v", err)
		}
	}
	agent := dir.Host.Slug
	if agent == "" {
		agent = store.AgentSetupSlug(dir.Parsed.DefinitionID)
	}
	res, err := s.Store.LoadAgent(ctx, store.AgentLoad{
		Definition: dir.Definition, Resources: resources, Agent: agent, Title: dir.Host.Title, Settings: settings,
		Pinned: dir.Host.Mode == agentdir.ModePinned, Force: opts.Force, GrantFrom: opts.GrantFrom, GrantedBy: grantedBy, Reason: opts.Reason, DryRun: opts.DryRun,
	})
	if err != nil {
		if errors.Is(err, store.ErrDefinitionRevisionConflict) {
			return out, fmt.Errorf("%w: agent.md changed but revision %q of %s is already installed with different content; bump `revision` in agent.md", ErrAgentSetupInvalid, dir.Parsed.Revision, dir.Parsed.DefinitionID)
		}
		if errors.Is(err, store.ErrAgentSetupRequest) {
			return out, fmt.Errorf("%w: %w", ErrAgentSetupInvalid, err)
		}
		return out, err
	}
	ref := DefinitionRef{DefinitionID: res.DefinitionRef.ID, Revision: res.DefinitionRef.Revision, SemanticDigest: res.DefinitionRef.Digest}
	hostRef := HostSettingsRef{ID: res.HostSettings.ID, Revision: res.HostSettings.Revision}
	out = AgentLoadResponse{
		Path: dir.Path, Agent: agent, Mode: string(dir.Host.Mode), DryRun: opts.DryRun, Changed: res.HostCreated,
		DefinitionRef: ref, HostSettings: hostRef, HostSettingsID: res.HostSettings.ID, Slug: res.HostSettings.Slug,
		Diff: loadDiff(res), AuthorityID: res.AuthorityID, Tools: res.Tools,
		SessionRequest: map[string]any{
			"method": "POST", "path": "/api/agent/v1/sessions",
			"body": map[string]any{"definition_ref": ref, "host_settings": hostRef},
		},
	}
	if out.Tools == nil {
		out.Tools = []string{}
	}
	if res.Previous != nil {
		out.Previous = res.Previous.Slug
	}
	out.Fields = agentpolicy.ExecutionReport(dir.Parsed)
	for _, f := range out.Fields {
		if f.Disposition == agentpolicy.Unmet {
			out.Notes = append(out.Notes, fmt.Sprintf("%s: %s", f.Field, f.Detail))
		}
	}
	if unrequested := unrequestedSkills(dir); len(unrequested) > 0 {
		out.Notes = append(out.Notes, fmt.Sprintf("skills/ holds %v, which agent.md does not request; they are ignored", unrequested))
	}
	if out.Requests, err = s.Store.AgentRequestsFor(ctx, dir.Parsed, res.HostSettings); err != nil {
		return out, err
	}
	if pending := out.Requests.PendingTools(); len(pending) > 0 {
		out.Notes = append(out.Notes, fmt.Sprintf("requested tools not granted yet: %v", pending))
	}
	if res.AuthorityID == "" {
		out.Notes = append(out.Notes, "this revision has no tool authority; grant tools (or reload with --grant-same-as) before starting a session")
	}
	return out, nil
}

// loadDiff lists what differs from the agent's previous revision.
func loadDiff(res store.AgentLoadResult) []string {
	if !res.HostCreated {
		return []string{"unchanged: this state of the directory is already loaded"}
	}
	p := res.Previous
	if p == nil {
		return []string{"new agent"}
	}
	var d []string
	add := func(name, from, to string) {
		if from != to {
			d = append(d, fmt.Sprintf("%s: %q -> %q", name, from, to))
		}
	}
	if p.DefinitionRef != res.HostSettings.DefinitionRef {
		d = append(d, fmt.Sprintf("definition: %s revision %s -> %s", res.DefinitionRef.ID, p.DefinitionRef.Revision, res.DefinitionRef.Revision))
		if p.DefinitionRef.Revision == res.DefinitionRef.Revision {
			d[len(d)-1] += " (same revision label, changed content)"
		}
	}
	a, b := p.Settings, res.HostSettings.Settings
	add("runtime", a.Runtime, b.Runtime)
	add("provider", a.Provider, b.Provider)
	add("model", a.Model, b.Model)
	add("directories", fmt.Sprint(a.Directories), fmt.Sprint(b.Directories))
	if len(d) == 0 {
		d = []string{"title or other non-behavioral change"}
	}
	return d
}

// unrequestedSkills lists the skill folders agent.md does not request.
func unrequestedSkills(dir *agentdir.Directory) []string {
	asked := map[string]bool{}
	for _, sk := range dir.Parsed.Requirements.Skills {
		asked[sk.Name] = true
	}
	var out []string
	for _, n := range dir.Skills {
		if !asked[n] {
			out = append(out, n)
		}
	}
	return out
}
