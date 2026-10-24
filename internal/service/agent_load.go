package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
	// AppDefaultMode and Labels feed the default mode rule when host.yaml names
	// none (ResolveAgentMode). Zero values give the conservative default, pinned.
	AppDefaultMode string
	Labels         DeployLabels
	// Attended says an operator ran this load from a shell (`agent load`, `agent run`)
	// and is present to see the result. Then the conservative fallback of the
	// default rule (pinned because nothing says dev) does not pin the agent or
	// bar a directory inside its own directories, so editing and running again
	// keeps working; an explicit mode, the application default and a dev label
	// still apply. An unattended reload never sets it.
	Attended bool
}

// AgentLoadResponse is the outcome of a load and the body to start a session.
type AgentLoadResponse struct {
	Path           string          `json:"path"`
	Agent          string          `json:"agent"`
	Mode           string          `json:"mode,omitempty"`
	ModeReason     string          `json:"mode_reason,omitempty"`
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
	// SkillsAdded are skills carried forward by grant_same_as: only those whose
	// pinned digest is unchanged in this revision.
	SkillsAdded []string `json:"skills_added,omitempty"`
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
	settings := store.NativeHostSettings{Version: "1", Runtime: dir.Host.Runtime, Provider: dir.Host.Provider, Model: dir.Host.Model, Directories: dir.Host.Directories, HarnessProfile: dir.Host.HarnessProfile, MCPServers: dir.Host.MCPServers, Mode: string(dir.Host.Mode), AgentDir: dir.Path}
	if err := settings.Validate(); err != nil {
		return out, fmt.Errorf("%w: host.yaml: %w", ErrAgentSetupInvalid, err)
	}
	mode, modeWhy, fallback := resolveAgentMode(string(dir.Host.Mode), opts.AppDefaultMode, opts.Labels)
	if fallback && opts.Attended {
		mode, modeWhy = "unpinned", "attended load: nothing says pinned, so each load publishes (set mode in host.yaml to choose)"
	}
	if mode == "live" {
		// A live agent is republished by a reload, so it must not be able to
		// rewrite the definition the reload reads.
		if err := agentdir.CheckOwnDirectories(dir.Path, dir.Host.Directories); err != nil {
			return out, setupInvalid("%v; move the agent directory outside the agent's directories or set mode: pinned in host.yaml", err)
		}
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
		Pinned: mode == "pinned", Force: opts.Force, GrantFrom: opts.GrantFrom, GrantedBy: grantedBy, Reason: opts.Reason, DryRun: opts.DryRun,
	})
	if err != nil {
		if errors.Is(err, store.ErrAgentLoadPinned) {
			return out, fmt.Errorf("%w; mode is pinned (%s)", err, modeWhy)
		}
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
		Path: dir.Path, Agent: agent, Mode: mode, ModeReason: modeWhy, DryRun: opts.DryRun, Changed: res.HostCreated,
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
	out.SkillsAdded = res.SkillsAdded
	delivered := map[string]bool{}
	if granted, gerr := s.Store.GrantedSkillDigests(ctx, res.HostSettings.ID, res.HostSettings.Revision); gerr == nil {
		for _, sk := range dir.Parsed.Requirements.Skills {
			if granted[sk.Name] != "" && granted[sk.Name] == sk.Content.Digest {
				delivered[sk.Name] = true
			}
		}
	}
	for _, name := range res.SkillsAdded {
		delivered[name] = true
	}
	out.Fields = agentpolicy.ExecutionReportWith(dir.Parsed, delivered)
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

// Reload re-reads the agent directory a loaded host row came from and publishes
// its current state, as `agent load` does. It is the server-side event behind
// "live": an operator (maintain scope) calls it, never a session create. The
// directory is the one the earlier load recorded on the host row, not a path
// from the request, and the result must still belong to the same agent. A pinned
// agent reloads only with opts.Force. It returns the body to start a session on
// the new row; sessions already running keep the row they started under.
func (s *AgentLoadService) Reload(ctx context.Context, hostIDOrSlug string, opts AgentLoadOptions, grantedBy string) (AgentLoadResponse, error) {
	view, err := s.Store.GetAgentView(ctx, hostIDOrSlug)
	if err != nil {
		return AgentLoadResponse{}, err
	}
	host := view.HostSettings
	if !strings.HasPrefix(host.Source, store.AgentLoadSource) {
		return AgentLoadResponse{}, setupInvalid("host settings %s were not loaded from an agent directory, so there is nothing to reload", host.Slug)
	}
	if host.Settings.AgentDir == "" {
		return AgentLoadResponse{}, setupInvalid("host settings %s do not name their agent directory; load the directory again with `nanite agent load`", host.Slug)
	}
	dir, err := agentdir.Load(host.Settings.AgentDir)
	if err != nil {
		return AgentLoadResponse{}, setupInvalid("%v", err)
	}
	agent := dir.Host.Slug
	if agent == "" {
		agent = store.AgentSetupSlug(dir.Parsed.DefinitionID)
	}
	if store.AgentLoadSource+agent != host.Source {
		return AgentLoadResponse{}, setupInvalid("the directory now names agent %q, but host settings %s belong to %q; load it as its own agent", agent, host.Slug, strings.TrimPrefix(host.Source, store.AgentLoadSource))
	}
	return s.Load(ctx, dir, opts, grantedBy)
}
