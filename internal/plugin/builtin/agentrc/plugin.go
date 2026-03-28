// Package agentrc syncs agent definitions from .agentrc/config.yaml into the
// database as source="agentrc" AgentProfile records. On load it reads the
// project-level config, resolves roles from ~/.agentrc/roles/, composes system
// prompts, and upserts agents. Agents removed from the config are disabled.
package agentrc

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	hostplugin "github.com/hollis-labs/conduit/internal/plugin"
	"github.com/hollis-labs/conduit/internal/store"
	"github.com/hollis-labs/fragments-engine/plugin"
	"gopkg.in/yaml.v3"
)

func init() {
	hostplugin.RegisterPlugin("agentrc-sync", func() plugin.Plugin { return New() })
}

// agentrcConfig is the structure of .agentrc/config.yaml.
type agentrcConfig struct {
	Version string                    `yaml:"agentrc_version"`
	Agents  map[string]agentrcAgent   `yaml:"agents"`
}

// agentrcAgent is one agent entry in the project config.
type agentrcAgent struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Roles       []string `yaml:"roles"`
	Skills      []string `yaml:"skills"`
	Context     string   `yaml:"context"`
}

// globalConfig is the structure of ~/.agentrc/config.yaml (roles section only).
type globalConfig struct {
	Roles map[string]roleEntry `yaml:"roles"`
}

// roleEntry maps a role name to its file path and metadata.
type roleEntry struct {
	File        string `yaml:"file"`
	Type        string `yaml:"type"`
	Description string `yaml:"description"`
}

// Plugin syncs agentrc agent definitions into the database.
type Plugin struct {
	host   plugin.Host
	status plugin.PluginStatus
}

// New creates a new agentrc sync plugin instance.
func New() *Plugin {
	return &Plugin{}
}

func (p *Plugin) ID() string             { return "agentrc-sync" }
func (p *Plugin) Name() string           { return "AgentRC Sync" }
func (p *Plugin) Version() string        { return "0.1.0" }
func (p *Plugin) Description() string    { return "Syncs .agentrc/config.yaml agents into the database" }
func (p *Plugin) Dependencies() []string { return nil }

func (p *Plugin) Load(host plugin.Host) error {
	p.host = host
	logger := host.Logger()

	svc, err := host.GetService("store")
	if err != nil {
		return fmt.Errorf("agentrc-sync: store service unavailable: %w", err)
	}
	s, ok := svc.(*store.Store)
	if !ok {
		return fmt.Errorf("agentrc-sync: store service has unexpected type %T", svc)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("agentrc-sync: cannot determine home dir: %w", err)
	}

	// Read global config for role definitions.
	globalCfg, err := readGlobalConfig(filepath.Join(home, ".agentrc", "config.yaml"))
	if err != nil {
		logger.Warn("agentrc-sync: could not read global config", "error", err.Error())
		globalCfg = &globalConfig{Roles: map[string]roleEntry{}}
	}

	// Read project-level .agentrc/config.yaml.
	projectCfg, err := readProjectConfig(".agentrc/config.yaml")
	if err != nil {
		logger.Info("agentrc-sync: no project config found", "error", err.Error())
		p.status = plugin.PluginStatus{Loaded: true, Enabled: true, LoadedAt: time.Now()}
		return nil
	}

	if len(projectCfg.Agents) == 0 {
		logger.Info("agentrc-sync: no agents defined in project config")
		p.status = plugin.PluginStatus{Loaded: true, Enabled: true, LoadedAt: time.Now()}
		return nil
	}

	rolesDir := filepath.Join(home, ".agentrc", "roles")
	configPath, _ := filepath.Abs(".agentrc/config.yaml")

	// Track which slugs we synced so we can disable removed agents.
	syncedSlugs := make(map[string]bool, len(projectCfg.Agents))

	for slug, agentDef := range projectCfg.Agents {
		syncedSlugs[slug] = true

		// Compose system prompt from roles.
		systemPrompt := composeSystemPrompt(globalCfg, rolesDir, agentDef.Roles)

		// Append project context if specified.
		if agentDef.Context != "" {
			ctxPath := filepath.Join(".agentrc", agentDef.Context)
			if data, err := os.ReadFile(ctxPath); err == nil {
				systemPrompt += "\n\n[Project Context]\n" + string(data)
			} else {
				logger.Warn("agentrc-sync: could not read context file", "path", ctxPath, "error", err.Error())
			}
		}

		// Build tags from roles + skills.
		tags := []string{"agentrc"}
		tags = append(tags, agentDef.Roles...)
		tagsJSON, _ := json.Marshal(tags)

		agent := &store.AgentProfile{
			Name:         agentDef.Name,
			Slug:         slug,
			Description:  agentDef.Description,
			SystemPrompt: systemPrompt,
			CanExecute:   true,
			Status:       "active",
			Source:       "agentrc",
			SourceRef:    configPath,
			Tags:         string(tagsJSON),
		}

		if err := s.UpsertAgentBySlug(agent); err != nil {
			logger.Error("agentrc-sync: failed to upsert agent", "slug", slug, "error", err.Error())
			continue
		}
		logger.Info("agentrc-sync: synced agent", "slug", slug, "name", agentDef.Name)
	}

	// Disable agents that were previously synced from agentrc but are no longer in the config.
	existing, err := s.ListAgentsBySource("agentrc")
	if err != nil {
		logger.Warn("agentrc-sync: could not list existing agentrc agents", "error", err.Error())
	} else {
		for _, a := range existing {
			if !syncedSlugs[a.Slug] && a.Status == "active" {
				a.Status = "disabled"
				if err := s.UpdateAgent(&a); err != nil {
					logger.Warn("agentrc-sync: failed to disable removed agent", "slug", a.Slug, "error", err.Error())
				} else {
					logger.Info("agentrc-sync: disabled removed agent", "slug", a.Slug)
				}
			}
		}
	}

	p.status = plugin.PluginStatus{
		Loaded:   true,
		Enabled:  true,
		LoadedAt: time.Now(),
	}
	logger.Info("agentrc-sync: loaded", "agents_synced", len(syncedSlugs))
	return nil
}

func (p *Plugin) Unload() error {
	p.status.Loaded = false
	p.status.Enabled = false
	if p.host != nil {
		p.host.Logger().Info("agentrc-sync: unloaded")
	}
	return nil
}

func (p *Plugin) Status() plugin.PluginStatus {
	return p.status
}

// readGlobalConfig reads ~/.agentrc/config.yaml for role definitions.
func readGlobalConfig(path string) (*globalConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg globalConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse global config: %w", err)
	}
	if cfg.Roles == nil {
		cfg.Roles = map[string]roleEntry{}
	}
	return &cfg, nil
}

// readProjectConfig reads .agentrc/config.yaml for agent definitions.
func readProjectConfig(path string) (*agentrcConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg agentrcConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse project config: %w", err)
	}
	return &cfg, nil
}

// composeSystemPrompt reads and concatenates role files into a system prompt.
func composeSystemPrompt(globalCfg *globalConfig, rolesDir string, roles []string) string {
	var parts []string
	for _, roleName := range roles {
		entry, ok := globalCfg.Roles[roleName]
		if !ok {
			parts = append(parts, fmt.Sprintf("[Role: %s]\n(role definition not found)\n", roleName))
			continue
		}

		rolePath := filepath.Join(rolesDir, entry.File)
		data, err := os.ReadFile(rolePath)
		if err != nil {
			parts = append(parts, fmt.Sprintf("[Role: %s]\n(could not read %s: %v)\n", roleName, entry.File, err))
			continue
		}

		parts = append(parts, strings.TrimSpace(string(data)))
	}

	return strings.Join(parts, "\n\n")
}
