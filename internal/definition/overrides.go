package definition

import (
	"fmt"
	"maps"

	"github.com/hollis-labs/libs/agent/agentdir"
	rules "github.com/hollis-labs/libs/security/policy/permission"
	"gopkg.in/yaml.v3"

	"github.com/hollis-labs/nanite/internal/authored"
)

// Overrides are what a host (the CLI's flags, an embedder) sets over the authored files, applied
// to every turn's definition after it is loaded.
type Overrides struct {
	// Model replaces the model of the chat use (host.yaml runtime.model and providers.yaml's
	// chat selection).
	Model string
	// PermissionMode replaces the permission mode (default, accept-edits, plan, yolo).
	PermissionMode string
	// MCP servers are added to the authored ones; a server of the same name replaces the
	// authored one. With StrictMCP only these are used.
	MCP       agentdir.MCPConfig
	StrictMCP bool
	// Context entries are added after the authored context (the Context slot).
	Context []Context
	// Rules are permission rules evaluated with the authored ones (deny, then ask, then allow).
	Rules []rules.Rule
	// SkillDirs hold more skills (each a <name>/SKILL.md directory), offered like the global
	// ones: the agent directory's skill of the same name wins.
	SkillDirs []string
}

// Zero reports whether there is nothing to override.
func (o Overrides) Zero() bool {
	return o.Model == "" && o.PermissionMode == "" && len(o.MCP.Servers) == 0 && !o.StrictMCP && len(o.Context) == 0 && len(o.Rules) == 0 && len(o.SkillDirs) == 0
}

// Apply sets the overrides on a loaded definition.
func (o Overrides) Apply(def *Definition) error {
	if o.Zero() {
		return nil
	}
	if o.Model != "" {
		def.Runtime.Model = o.Model
		if sel, ok := def.Use[authored.UseChat]; ok {
			use := maps.Clone(def.Use)
			sel.Model = o.Model
			use[authored.UseChat] = sel
			def.Use = use
		}
	}
	if o.PermissionMode != "" {
		def.Snapshot.Profile.Permissions.Mode = o.PermissionMode
	}
	servers := map[string]agentdir.MCPServer{}
	if !o.StrictMCP {
		maps.Copy(servers, def.Snapshot.MCP.Servers)
	}
	maps.Copy(servers, o.MCP.Servers)
	def.Snapshot.MCP = agentdir.MCPConfig{Servers: servers}
	def.Context = append(append([]Context(nil), def.Context...), o.Context...)
	if len(o.SkillDirs) > 0 {
		all, global := map[string]agentdir.SkillRef{}, map[string]agentdir.SkillRef{}
		maps.Copy(all, def.Skills)
		maps.Copy(global, def.GlobalSkills)
		authoredSkills := map[string]bool{}
		for _, ref := range def.Snapshot.Skills {
			authoredSkills[ref.Name] = true
		}
		for _, dir := range o.SkillDirs {
			refs, err := agentdir.ListSkills(dir)
			if err != nil {
				return fmt.Errorf("skills in %s: %w", dir, err)
			}
			for _, ref := range refs {
				if !authoredSkills[ref.Name] {
					all[ref.Name], global[ref.Name] = ref, ref
				}
			}
		}
		def.Skills, def.GlobalSkills = all, global
	}
	if len(o.Rules) > 0 {
		var file rules.PermissionsFile
		if len(def.Rules) > 0 {
			if err := yaml.Unmarshal(def.Rules, &file); err != nil {
				return fmt.Errorf("permissions.yaml: %w", err)
			}
		}
		file.Permissions.Rules = append(append([]rules.Rule(nil), o.Rules...), file.Permissions.Rules...)
		if err := file.Permissions.Validate(); err != nil {
			return err
		}
		raw, err := yaml.Marshal(file)
		if err != nil {
			return err
		}
		def.Rules = raw
	}
	return nil
}
