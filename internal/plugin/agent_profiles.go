package plugin

// Phase 5 item 03 (TASKS/phase-5/03-wire-registers-agent-profiles.md) --
// wires registers.agent_profiles[] up to the role/agent composition model
// (architecture/01-agent-construction.md), replacing the deferred-skip stub
// that used to live in applyManifestRegistrations
// (internal/plugin/registrations.go). See that function's call site for the
// entrypoint into this file.
//
// ## The new AgentProfileRegistration shape -- design summary
//
// AgentProfileRegistration itself (config.go) is UNCHANGED in shape --
// still {ID, File}. The manifest-level entry only ever needed to be a
// pointer at a file; what changed is what that file is now required to
// contain: not the plugin's own old flat agent-profile format (persona/
// tools/mcp_servers stuffed directly at the top level -- the shape the now-
// cut giphy reference plugin's test fixtures gestured at, see
// config_manifest_v1_test.go's "agents/giphy.yaml" reference), but a
// PluginAgentProfileDocument -- a real role/agent split matching
// architecture/01-agent-construction.md's composition model field-for-
// field:
//
//   - role.system_prompt/name/class     -> a `roles` row (persona/behavior
//                                           template; the role -> agent ->
//                                           task cascade's broadest layer)
//   - agent.slug/name/class/tools/skills -> an `agent_profiles` row (still
//                                           `agent_profiles` under the hood
//                                           per decision log Section 6, not
//                                           renamed to `agents`) plus its
//                                           agent_tools/agent_known_skills grants
//   - agent.consumer_slug (or, if unset, the plugin's own id)
//                                        -> a `consumers` row, tagging
//                                           agent_profiles.consumer_id --
//                                           "a plugin-registered agent is a
//                                           natural consumer_id candidate"
//                                           per this task's own Context
//
// No legacy grandfathering (explicit instruction in both this task and
// architecture/01-agent-construction.md's "Plugin-provided agents ... must
// conform to this schema, no legacy grandfathering"): ParsePluginAgentProfileFile
// decodes with strict KnownFields(true), and Validate() requires the new
// shape's mandatory fields (role.slug/name/system_prompt, agent.slug/name).
// A file written in the old flat shape (no role:/agent: split) fails one of
// these checks with a clear, specific error identifying exactly what's
// missing -- it is never silently coerced into a partial composition.
//
// Scope (agent_projects) is deliberately NOT part of this v1 shape: unlike
// role/consumer, `projects` (internal/store/projects.go) has no slug or
// other stable natural key a plugin manifest could reference declaratively
// -- only an opaque DB-minted ID, which a plugin author can't know ahead of
// time. A plugin-registered agent is therefore unscoped (global) by
// default, same as Curator today. Real per-project scoping for a plugin
// agent can be added by an operator after the fact via the existing
// agent_projects API once this limitation matters to a real consumer.
//
// ## Ownership / re-load semantics
//
// Every load re-applies (upserts) each declared agent profile -- matching
// this task's own "construct/upsert" instruction and mirroring
// UpsertAgentBySlug's existing, documented purpose ("used by framework sync
// plugins ... to keep DB in sync with config"). This is deliberately
// different from the builtin/seed-profile "insert once, never re-overwritten"
// rule architecture/01-agent-construction.md's "What's cut" section
// describes for compiled-in profiles: a plugin's agent_profiles[] entry is
// the plugin's own live-synced config (like agentrc's agents), not a one-
// time seed a GUI customization should permanently detach from. A future
// "detach from plugin management" affordance (clearing PluginID via the
// GUI) is out of scope here and not needed for this task's Done-means.
//
// A slug collision with a NON-plugin-owned row (operator-created via the
// GUI/API, or owned by a *different* plugin) is refused with a clear error
// rather than silently adopted -- see applyPluginAgentProfile.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/hollis-labs/nanite/internal/store"
	"gopkg.in/yaml.v3"
)

// PluginAgentProfileRole is the `role:` block of a plugin agent-profile
// file -- maps onto internal/store.Role, the broadest layer of the role ->
// agent -> task cascade (internal/service/role_cascade.go).
type PluginAgentProfileRole struct {
	Slug         string `yaml:"slug"`
	Name         string `yaml:"name"`
	SystemPrompt string `yaml:"system_prompt"`
	// Class mirrors roles.default_class / agent_profiles.class's four-value
	// enum. Optional -- empty means this role supplies no class default.
	Class string `yaml:"class,omitempty"`
}

// PluginAgentProfileAgent is the `agent:` block of a plugin agent-profile
// file -- maps onto internal/store.AgentProfile (the composition record)
// plus its agent_tools/agent_known_skills grants.
type PluginAgentProfileAgent struct {
	Slug        string `yaml:"slug"`
	Name        string `yaml:"name"`
	Description string `yaml:"description,omitempty"`
	// Class/ActivationMode/RuntimeKind mirror agent_profiles' own enum
	// columns (see internal/store/agents.go's validateAgentMultiAgentFields).
	// All optional: an empty value here means "let applyMultiAgentDefaults
	// pick the composition's usual default," same as any other caller of
	// CreateAgent/UpdateAgent leaving the field unset.
	Class           string `yaml:"class,omitempty"`
	ActivationMode  string `yaml:"activation_mode,omitempty"`
	RuntimeKind     string `yaml:"runtime_kind,omitempty"`
	DefaultModel    string `yaml:"default_model,omitempty"`
	DefaultProvider string `yaml:"default_provider,omitempty"`
	// Protocol/Transport (TASKS/agent-host-acp/11-nanite-per-agent-protocol-
	// transport-config.md) mirror agent_profiles.protocol/transport (see
	// internal/store/agents.go's AgentProfile doc comment). Both optional --
	// empty means "use this provider's existing native protocol," same
	// default as every other agent_profiles row.
	Protocol  string `yaml:"protocol,omitempty"`
	Transport string `yaml:"transport,omitempty"`
	// SystemPromptOverride is the agent-composition-layer override of the
	// role's system_prompt (the cascade's middle tier). Almost always left
	// empty in practice -- an empty value here means the agent's effective
	// system prompt is the role's, resolved at runtime by
	// internal/service.ResolveAgentCascade / applyScalarCascade, not
	// duplicated onto this row at registration time.
	SystemPromptOverride string `yaml:"system_prompt_override,omitempty"`
	// Tools is a list of known_tools.name values this agent should be
	// granted (agent_tools, granted_via="plugin"). A name with no matching
	// known_tools row is logged and skipped, not a load failure -- the
	// catalog may not have synced that tool yet (see
	// internal/service/known_tools_sync.go).
	Tools []string `yaml:"tools,omitempty"`
	// Skills is a list of skills.slug values this agent should be assigned
	// (agent_known_skills). Same not-found-is-a-skip handling as Tools.
	Skills []string `yaml:"skills,omitempty"`
	// ConsumerSlug tags agent_profiles.consumer_id via a consumers row.
	// Empty means default to the plugin's own canonical id -- "a plugin-
	// registered agent is a natural consumer_id candidate" per this task's
	// Context. Set explicitly when a plugin represents an external system
	// with its own identity distinct from the plugin's own id (e.g. a
	// Loom-authored plugin tagging consumer_slug: loom rather than its own
	// plugin id).
	ConsumerSlug string `yaml:"consumer_slug,omitempty"`
}

// PluginAgentProfileDocument is the schema a plugin's
// registers.agent_profiles[].file YAML document must conform to as of
// Phase 5 item 03. See this file's package-level doc comment for the full
// design rationale, including the "no legacy grandfathering" rejection of
// the old flat agent-profile shape.
type PluginAgentProfileDocument struct {
	Role  PluginAgentProfileRole  `yaml:"role"`
	Agent PluginAgentProfileAgent `yaml:"agent"`
}

// ParsePluginAgentProfileFile reads and strictly decodes path as a
// PluginAgentProfileDocument. KnownFields(true) rejects any top-level key
// outside role:/agent: (including every field the old flat shape used to
// carry at the top level) with a specific "field X not found" error instead
// of silently ignoring it -- part of this task's "no legacy grandfathering"
// instruction.
func ParsePluginAgentProfileFile(path string) (*PluginAgentProfileDocument, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open agent profile file: %w", err)
	}
	defer func() {
		_ = f.Close() // Read-only file close is best-effort cleanup; read errors are handled separately.
	}()

	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	var doc PluginAgentProfileDocument
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf(
			"parse agent profile file %s: %w (must use the role/agent composition shape -- "+
				"see architecture/01-agent-construction.md; the old flat agent-profile shape is not supported)",
			path, err,
		)
	}
	return &doc, nil
}

// Validate enforces the new shape's required fields and enum-shaped values,
// producing a clear, specific rejection for a document that doesn't conform
// -- rather than constructing a partial/nonsensical composition.
func (d *PluginAgentProfileDocument) Validate() error {
	if d.Role.Slug == "" {
		return errors.New("role.slug is required (new role/agent composition shape -- see architecture/01-agent-construction.md; the old flat agent-profile shape is not supported)")
	}
	if d.Role.Name == "" {
		return errors.New("role.name is required")
	}
	if d.Role.SystemPrompt == "" {
		return errors.New("role.system_prompt is required")
	}
	if err := validateOptionalClass(d.Role.Class); err != nil {
		return fmt.Errorf("role.class: %w", err)
	}
	if d.Agent.Slug == "" {
		return errors.New("agent.slug is required")
	}
	if d.Agent.Name == "" {
		return errors.New("agent.name is required")
	}
	if err := validateOptionalClass(d.Agent.Class); err != nil {
		return fmt.Errorf("agent.class: %w", err)
	}
	switch d.Agent.ActivationMode {
	case "", "singleton", "fresh-per-wake", "concurrent":
	default:
		return fmt.Errorf("agent.activation_mode %q invalid: must be 'singleton', 'fresh-per-wake', or 'concurrent'", d.Agent.ActivationMode)
	}
	switch d.Agent.RuntimeKind {
	case "", "cli", "api":
	default:
		return fmt.Errorf("agent.runtime_kind %q invalid: must be 'cli' or 'api'", d.Agent.RuntimeKind)
	}
	switch d.Agent.Protocol {
	case "", "claude-stream-json", "codex-app-server", "opencode-native", "acp":
	default:
		return fmt.Errorf("agent.protocol %q invalid: must be '', 'claude-stream-json', 'codex-app-server', 'opencode-native', or 'acp'", d.Agent.Protocol)
	}
	switch d.Agent.Transport {
	case "", "stdio", "tcp":
	default:
		return fmt.Errorf("agent.transport %q invalid: must be '', 'stdio', or 'tcp'", d.Agent.Transport)
	}
	if d.Agent.Transport != "" && d.Agent.Protocol != "acp" {
		return fmt.Errorf("agent.transport %q is only valid when agent.protocol is 'acp' (got %q)", d.Agent.Transport, d.Agent.Protocol)
	}
	return nil
}

func validateOptionalClass(class string) error {
	switch class {
	case "", "advisor", "process", "template", "harness":
		return nil
	default:
		return fmt.Errorf("%q invalid: must be 'advisor', 'process', 'template', or 'harness'", class)
	}
}

// SweepPluginAgentProfiles removes every agent_profiles/roles row tagged
// PluginID == pluginID (h.store-backed; migration 122). This is the
// teardown half of Phase 5 item 03 -- exported as its own *Host method
// (rather than inlined into UnloadPlugin) because two independent call
// sites need the exact same DB-authoritative sweep:
//
//   - Host.UnloadPlugin, the live in-process path a running server's
//     disable/uninstall/reload API handlers ride on.
//   - cmd/nanite/plugin_cmd.go's CLI pluginUninstall/pluginDisable, which
//     never construct a Host wired to the actual running plugin -- their
//     buildMinimalHost helper only has a store (NewHostWithStore(s)), and
//     both commands apply their real effect via a subsequent `cerberus
//     restart` rather than a live UnloadPlugin call. Without an explicit
//     call to this method from those two commands, a CLI-driven
//     uninstall/disable would leave every plugin-owned agent_profiles/roles
//     row behind in the DB forever -- the next boot simply never loads the
//     plugin again, so UnloadPlugin (and therefore this sweep, if it were
//     only reachable from there) would never run for it.
//
// No-op if h.store is nil (mirrors every other store-backed cleanup step in
// UnloadPlugin -- absent store is fine, there's nothing to sweep).
//
// Reuses store.DeleteAgentByID's existing full-cascade cleanup
// (session_agents, agent_tools, agent_known_skills, agent_projects, reflexes,
// durable instances, etc. -- internal/store/agents.go's DeleteAgent) rather
// than re-deriving that sweep here. Roles are only removed once zero
// agent_profiles rows still reference them -- CountAgentsByRoleID guards
// the case where a plugin-owned role was reused (read-only, see
// resolveOrCreatePluginRole above) by an agent this plugin doesn't own.
func (h *Host) SweepPluginAgentProfiles(pluginID string) {
	h.mu.RLock()
	st := h.store
	h.mu.RUnlock()
	if st == nil {
		return
	}

	agents, err := st.ListAgentsByPluginID(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, pluginID)
	if err != nil {
		h.logger.Warn("plugin agent_profiles sweep: list plugin-owned agents failed", "plugin", pluginID, "error", err)
	} else {
		removed := 0
		for _, a := range agents {
			if err := st.DeleteAgentByID(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, a.ID); err != nil {
				h.logger.Warn("plugin agent_profiles sweep: delete plugin-owned agent failed",
					"plugin", pluginID, "agent", a.Slug, "error", err)
				continue
			}
			removed++
		}
		if removed > 0 {
			h.logger.Debug("plugin agent_profiles sweep: removed agent profiles", "plugin", pluginID, "count", removed)
		}
	}

	roles, err := st.ListRolesByPluginID(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, pluginID)
	if err != nil {
		h.logger.Warn("plugin agent_profiles sweep: list plugin-owned roles failed", "plugin", pluginID, "error", err)
		return
	}
	removed := 0
	for _, r := range roles {
		n, err := st.CountAgentsByRoleID(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, r.ID)
		if err != nil {
			h.logger.Warn("plugin agent_profiles sweep: count agents by role failed",
				"plugin", pluginID, "role", r.Slug, "error", err)
			continue
		}
		if n > 0 {
			// Still referenced by an agent outside this plugin's own sweep
			// above (e.g. an operator or a different plugin bound to a
			// reused, shared role) -- leave it in place.
			continue
		}
		if err := st.DeleteRole(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, r.ID); err != nil {
			h.logger.Warn("plugin agent_profiles sweep: delete plugin-owned role failed",
				"plugin", pluginID, "role", r.Slug, "error", err)
			continue
		}
		removed++
	}
	if removed > 0 {
		h.logger.Debug("plugin agent_profiles sweep: removed roles", "plugin", pluginID, "count", removed)
	}
}

// registerManifestAgentProfiles implements the registers.agent_profiles[]
// registration path -- the real B.4-follow-up replacement for the deferred-
// skip stub applyManifestRegistrations used to log. Parses and validates
// every declared entry's file, then constructs/upserts the corresponding
// roles/agent_profiles rows (and their agent_tools/agent_known_skills grants) via
// applyPluginAgentProfile.
//
// Degrades gracefully (logs + returns nil, does not fail plugin load) for
// two "nothing to register against" cases that mirror this file's sibling
// registerManifestMCPServers/registerManifestHTTPRoutes precedent:
//   - no store configured on the host (e.g. a CLI-only host, or a unit test
//     that never called SetStore) -- roles/agent_profiles are DB-only, so
//     there is nowhere to write.
//   - no on-disk plugin directory (pluginDir == "", the shape
//     LoadRegisteredBuiltins passes for a compiled-in builtin with an
//     embedded plugin.yaml but no plugins/<id>/ directory) -- there is no
//     file for `file:` to resolve against.
//
// Every other failure (missing id/file, duplicate id, duplicate agent slug
// across entries, a file that fails to parse/validate, a slug collision
// with a non-plugin-owned row) is a hard error that fails the plugin's
// load, consistent with every other manifest registration category in this
// file.
func registerManifestAgentProfiles(host *Host, pluginID string, entries []AgentProfileRegistration, pluginDir string) error {
	if len(entries) == 0 {
		return nil
	}

	host.mu.RLock()
	st := host.store
	host.mu.RUnlock()
	if st == nil {
		host.logger.Warn("manifest agent_profiles: no store configured -- skipping",
			"plugin", pluginID, "count", len(entries))
		return nil
	}
	if pluginDir == "" {
		host.logger.Warn("manifest agent_profiles: no on-disk plugin dir -- skipping "+
			"(a compiled-in builtin with no plugins/<id>/ directory cannot ship an agent_profiles file)",
			"plugin", pluginID, "count", len(entries))
		return nil
	}

	ctx := context.Background()
	seenID := make(map[string]bool, len(entries))
	seenSlug := make(map[string]bool, len(entries))
	for i, entry := range entries {
		if entry.ID == "" || entry.File == "" {
			return fmt.Errorf("plugin %q: agent_profiles[%d] requires id and file", pluginID, i)
		}
		if seenID[entry.ID] {
			return fmt.Errorf("plugin %q: duplicate agent_profiles id %q", pluginID, entry.ID)
		}
		seenID[entry.ID] = true

		filePath, err := resolvePluginAssetPath(pluginDir, entry.File)
		if err != nil {
			return fmt.Errorf("plugin %q: agent_profiles[%d] (%s): %w", pluginID, i, entry.ID, err)
		}
		doc, err := ParsePluginAgentProfileFile(filePath)
		if err != nil {
			return fmt.Errorf("plugin %q: agent_profiles[%d] (%s): %w", pluginID, i, entry.ID, err)
		}
		if err := doc.Validate(); err != nil {
			return fmt.Errorf("plugin %q: agent_profiles[%d] (%s): %w", pluginID, i, entry.ID, err)
		}
		if seenSlug[doc.Agent.Slug] {
			return fmt.Errorf("plugin %q: duplicate agent slug %q across agent_profiles entries", pluginID, doc.Agent.Slug)
		}
		seenSlug[doc.Agent.Slug] = true

		if err := applyPluginAgentProfile(ctx, host, st, pluginID, doc); err != nil {
			return fmt.Errorf("plugin %q: agent_profiles[%d] (%s): %w", pluginID, i, entry.ID, err)
		}
		host.logger.Info("registered plugin agent profile",
			"plugin", pluginID, "registration_id", entry.ID, "agent_slug", doc.Agent.Slug, "role_slug", doc.Role.Slug)
	}
	return nil
}

// applyPluginAgentProfile constructs/upserts the roles + agent_profiles
// rows (plus agent_tools/agent_known_skills grants) a single parsed document
// describes. See registerManifestAgentProfiles for the entrypoint and this
// file's package doc comment for the overall design.
func applyPluginAgentProfile(ctx context.Context, host *Host, st *store.Store, pluginID string, doc *PluginAgentProfileDocument) error {
	role, err := resolveOrCreatePluginRole(st, pluginID, doc.Role)
	if err != nil {
		return fmt.Errorf("role %q: %w", doc.Role.Slug, err)
	}

	consumerSlug := doc.Agent.ConsumerSlug
	if consumerSlug == "" {
		consumerSlug = pluginID
	}
	consumer, err := resolveOrCreateConsumer(st, consumerSlug)
	if err != nil {
		return fmt.Errorf("consumer %q: %w", consumerSlug, err)
	}

	// GetAgentBySlug wraps sql.ErrNoRows into a generic error rather than
	// returning (nil, nil) on a miss (unlike GetRoleBySlug/GetConsumerBySlug/
	// GetSkillBySlug above) -- mirror UpsertAgentBySlug's own established
	// handling (internal/store/agents.go) of treating any lookup error here
	// as "no existing row," since UpsertAgentBySlug below re-derives the
	// same lookup internally and would otherwise mask a real DB error as a
	// silent create anyway.
	existing, lookupErr := st.GetAgentBySlug(ctx, doc.Agent.Slug)
	if lookupErr != nil {
		existing = nil
	}
	if existing != nil && existing.PluginID != pluginID {
		owner := existing.PluginID
		if owner == "" {
			owner = "operator (not plugin-owned)"
		}
		return fmt.Errorf(
			"agent slug %q already exists, owned by %q -- refusing to overwrite a non-plugin-owned or different-plugin-owned agent",
			doc.Agent.Slug, owner,
		)
	}

	// Start from the existing row (when one exists) rather than a blank
	// struct, then overwrite only the fields this registration is
	// authoritative for. UpdateAgent (unlike CreateAgent) does not default
	// empty-string fields back to their column defaults ("[]"/"{}"/"active"/
	// etc.) -- it writes exactly what the struct holds. A freshly-zeroed
	// struct passed through UpsertAgentBySlug's UPDATE path would therefore
	// blank out every field this registration doesn't set (modes, tools
	// legacy column, tags, status, capabilities_json, icon, ...) on the
	// SECOND and every subsequent load. Copying existing first preserves
	// all of that -- mirroring, and generalizing, UpsertAgentBySlug's own
	// existing precedent of selectively preserving ID/AgentHash/Version/URN
	// across a resync.
	var agentRow store.AgentProfile
	if existing != nil {
		agentRow = *existing
	}
	agentRow.Name = doc.Agent.Name
	agentRow.Slug = doc.Agent.Slug
	agentRow.Description = doc.Agent.Description
	agentRow.SystemPrompt = doc.Agent.SystemPromptOverride
	agentRow.Class = doc.Agent.Class
	agentRow.ActivationMode = doc.Agent.ActivationMode
	agentRow.RuntimeKind = doc.Agent.RuntimeKind
	agentRow.Protocol = doc.Agent.Protocol
	agentRow.Transport = doc.Agent.Transport
	agentRow.DefaultModel = doc.Agent.DefaultModel
	agentRow.DefaultProvider = doc.Agent.DefaultProvider
	agentRow.RoleID = role.ID
	agentRow.ConsumerID = consumer.ID
	agentRow.PluginID = pluginID
	agentRow.Source = "plugin"
	if agentRow.Status == "" {
		// Only true on first create (existing == nil) -- CreateAgent
		// would default this anyway, but set it explicitly for clarity
		// and so a direct struct inspection before insert is never blank.
		agentRow.Status = "active"
	}
	if err := st.UpsertAgentBySlug(ctx, &agentRow); err != nil {
		return fmt.Errorf("upsert agent %q: %w", doc.Agent.Slug, err)
	}

	for _, toolName := range doc.Agent.Tools {
		kt, err := st.GetKnownToolByName(ctx, toolName)
		if err != nil {
			if errors.Is(err, store.ErrKnownToolNotFound) {
				host.logger.Warn("plugin agent_profiles: declared tool not found in known_tools -- skipping grant",
					"plugin", pluginID, "agent", agentRow.Slug, "tool", toolName)
				continue
			}
			return fmt.Errorf("look up known tool %q: %w", toolName, err)
		}
		if err := st.GrantAgentTool(ctx, agentRow.ID, kt.ID, "plugin"); err != nil {
			return fmt.Errorf("grant tool %q to agent %q: %w", toolName, agentRow.Slug, err)
		}
	}

	for _, skillSlug := range doc.Agent.Skills {
		sk, err := st.GetSkillBySlug(ctx, skillSlug)
		if err != nil {
			return fmt.Errorf("look up skill %q: %w", skillSlug, err)
		}
		if sk == nil {
			host.logger.Warn("plugin agent_profiles: declared skill not found -- skipping grant",
				"plugin", pluginID, "agent", agentRow.Slug, "skill", skillSlug)
			continue
		}
		if err := st.AssignSkillToAgent(ctx, agentRow.ID, sk.ID, "{}"); err != nil {
			return fmt.Errorf("assign skill %q to agent %q: %w", skillSlug, agentRow.Slug, err)
		}
	}

	return nil
}

// resolveOrCreatePluginRole resolves doc's role by slug. Three cases:
//  1. No existing row -- create one, tagged PluginID=pluginID.
//  2. An existing row already owned by this plugin -- re-sync its content
//     (name/system_prompt/class) from the current file, matching this
//     file's "construct/upsert" re-load semantics (see package doc comment).
//  3. An existing row owned by someone else (empty PluginID = operator-
//     created, or a different plugin's) -- reuse it READ-ONLY: bind the
//     agent to it without mutating its content or claiming ownership, so
//     plugin.Host.UnloadPlugin's sweep never deletes a role this plugin
//     didn't actually create.
func resolveOrCreatePluginRole(st *store.Store, pluginID string, r PluginAgentProfileRole) (*store.Role, error) {
	existing, err := st.GetRoleBySlug(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, r.Slug)
	if err != nil {
		return nil, fmt.Errorf("look up role by slug: %w", err)
	}
	if existing != nil {
		if existing.PluginID != pluginID {
			return existing, nil
		}
		existing.Name = r.Name
		existing.SystemPrompt = r.SystemPrompt
		existing.DefaultClass = r.Class
		if err := st.UpdateRole(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, existing); err != nil {
			return nil, fmt.Errorf("update role: %w", err)
		}
		return existing, nil
	}
	role := &store.Role{
		Slug:         r.Slug,
		Name:         r.Name,
		SystemPrompt: r.SystemPrompt,
		DefaultClass: r.Class,
		PluginID:     pluginID,
	}
	if err := st.CreateRole(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, role); err != nil {
		return nil, fmt.Errorf("create role: %w", err)
	}
	return role, nil
}

// resolveOrCreateConsumer resolves (or creates) a consumers row by slug.
// Consumer rows are deliberately NOT plugin.Host.UnloadPlugin-swept: per
// architecture/01-agent-construction.md, a consumer is a lightweight,
// durable ownership tag (the existing Loom seed row, migration 112, is
// never torn down either) -- reused across plugin reinstalls/reloads
// rather than churned, and safe to leave behind after an unload (an unused
// consumer row is inert; DeleteConsumer is still available via the
// existing consumers API for an operator who wants to clean it up).
func resolveOrCreateConsumer(st *store.Store, slug string) (*store.Consumer, error) {
	existing, err := st.GetConsumerBySlug(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, slug)
	if err != nil {
		return nil, fmt.Errorf("look up consumer by slug: %w", err)
	}
	if existing != nil {
		return existing, nil
	}
	c := &store.Consumer{
		Slug: slug,
		Name: humanizeSlug(slug),
	}
	if err := st.CreateConsumer(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, c); err != nil {
		return nil, fmt.Errorf("create consumer: %w", err)
	}
	return c, nil
}

// humanizeSlug turns a hyphen/underscore-separated slug into a Title Case
// display name (e.g. "giphy-widgets" -> "Giphy Widgets") for a consumer row
// auto-created from a plugin id, which has no separate human-readable name
// to draw from.
func humanizeSlug(slug string) string {
	parts := strings.FieldsFunc(slug, func(r rune) bool { return r == '-' || r == '_' })
	if len(parts) == 0 {
		return slug
	}
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}
