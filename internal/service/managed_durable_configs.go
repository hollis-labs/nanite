package service

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/store"
	"gopkg.in/yaml.v3"
)

const managedDurableConfigSource = "managed_file"

type ManagedDurableAgentConfig struct {
	Name             string            `json:"name" yaml:"name"`
	Slug             string            `json:"slug" yaml:"slug"`
	ProfileSlug      string            `json:"profile_slug" yaml:"profile_slug"`
	LifecycleClass   string            `json:"lifecycle_class" yaml:"lifecycle_class"`
	Provider         string            `json:"provider" yaml:"provider"`
	Model            string            `json:"model" yaml:"model"`
	RuntimeKind      string            `json:"runtime_kind" yaml:"runtime_kind"`
	LaunchSourceType string            `json:"launch_source_type" yaml:"launch_source_type"`
	LaunchSourceID   string            `json:"launch_source_id" yaml:"launch_source_id"`
	WorkRoot         string            `json:"work_root,omitempty" yaml:"work_root,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	Archived         bool              `json:"archived,omitempty" yaml:"archived,omitempty"`
	Source           string            `json:"-" yaml:"-"`
	SourceRef        string            `json:"-" yaml:"-"`
}

func UserManagedDurableAgentPath(homeDir, slug string) (string, error) {
	home := homeDir
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home dir: %w", err)
		}
	}
	return filepath.Join(home, ".nanite", "durable-agents", slug+".yaml"), nil
}

func ManagedDurableAgentPath(configRoot, slug string) (string, error) {
	if strings.TrimSpace(configRoot) == "" {
		return "", fmt.Errorf("managed durable config root is required")
	}
	if strings.TrimSpace(slug) == "" {
		return "", fmt.Errorf("managed durable slug is required")
	}
	return filepath.Join(configRoot, "durable-agents", slug+".yaml"), nil
}

func WriteManagedDurableAgentConfig(path string, cfg ManagedDurableAgentConfig) error {
	raw, err := yaml.Marshal(&cfg)
	if err != nil {
		return fmt.Errorf("marshal durable agent config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("ensure durable agent dir: %w", err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return fmt.Errorf("write durable agent config: %w", err)
	}
	return nil
}

func SyncManagedDurableAgentConfigs(st *store.Store, configRoot string) error {
	configs, err := discoverManagedDurableAgentConfigs(configRoot)
	if err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(configs))
	for _, cfg := range configs {
		if _, err := syncManagedDurableAgentConfig(st, cfg); err != nil {
			slog.Warn("service: sync managed durable config", "slug", cfg.Slug, "path", cfg.SourceRef, "err", err)
			continue
		}
		seen[cfg.Slug] = struct{}{}
	}
	instances, err := st.ListDurableAgentInstances(true)
	if err != nil {
		return err
	}
	for _, inst := range instances {
		meta, ok := managedDurableMetadata(inst.MetadataJSON)
		if !ok {
			continue
		}
		if _, present := seen[inst.Slug]; present {
			continue
		}
		if meta["managed_source"] != managedDurableConfigSource {
			continue
		}
		if inst.Status == store.DurableAgentStatusArchived {
			continue
		}
		if _, err := st.SyncDurableAgentInstanceConfig(&store.DurableAgentInstance{
			ID:               inst.ID,
			Name:             inst.Name,
			Slug:             inst.Slug,
			ProfileID:        inst.ProfileID,
			LifecycleClass:   inst.LifecycleClass,
			Provider:         inst.Provider,
			Model:            inst.Model,
			RuntimeKind:      inst.RuntimeKind,
			LaunchSourceType: inst.LaunchSourceType,
			LaunchSourceID:   inst.LaunchSourceID,
			WorkRoot:         inst.WorkRoot,
			Status:           store.DurableAgentStatusArchived,
			MetadataJSON:     inst.MetadataJSON,
			CreatedAt:        inst.CreatedAt,
			ArchivedAt:       ptrTime(time.Now().UTC()),
		}); err != nil {
			return err
		}
	}
	return nil
}

func syncManagedDurableAgentConfig(st *store.Store, cfg ManagedDurableAgentConfig) (*store.DurableAgentInstance, error) {
	profile, err := st.GetAgentBySlug(cfg.ProfileSlug)
	if err != nil {
		return nil, fmt.Errorf("managed durable config %s profile %s: %w", cfg.Slug, cfg.ProfileSlug, err)
	}
	metadata := map[string]string{}
	for k, v := range cfg.Metadata {
		metadata[k] = v
	}
	metadata["managed_source"] = managedDurableConfigSource
	metadata["managed_config_path"] = cfg.SourceRef
	metadata["managed_profile_slug"] = cfg.ProfileSlug
	inst := &store.DurableAgentInstance{
		Name:             cfg.Name,
		Slug:             cfg.Slug,
		ProfileID:        profile.ID,
		LifecycleClass:   cfg.LifecycleClass,
		Provider:         cfg.Provider,
		Model:            cfg.Model,
		RuntimeKind:      cfg.RuntimeKind,
		LaunchSourceType: cfg.LaunchSourceType,
		LaunchSourceID:   cfg.LaunchSourceID,
		WorkRoot:         cfg.WorkRoot,
		MetadataJSON:     durableAgentEventMetadata(metadata),
	}
	if cfg.Archived {
		inst.Status = store.DurableAgentStatusArchived
		inst.ArchivedAt = ptrTime(time.Now().UTC())
	}
	return st.SyncDurableAgentInstanceConfig(inst)
}

func discoverManagedDurableAgentConfigs(configRoot string) ([]ManagedDurableAgentConfig, error) {
	type sourceDir struct {
		dir    string
		source string
	}
	var dirs []sourceDir
	if configRoot != "" {
		dirs = append(dirs, sourceDir{
			dir:    filepath.Join(configRoot, "durable-agents"),
			source: managedDurableConfigSource,
		})
	}
	seen := map[string]struct{}{}
	var out []ManagedDurableAgentConfig
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir.dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			ext := strings.ToLower(filepath.Ext(entry.Name()))
			if ext != ".yaml" && ext != ".yml" {
				continue
			}
			path := filepath.Join(dir.dir, entry.Name())
			raw, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("read durable config %s: %w", path, err)
			}
			var cfg ManagedDurableAgentConfig
			if err := yaml.Unmarshal(raw, &cfg); err != nil {
				return nil, fmt.Errorf("parse durable config %s: %w", path, err)
			}
			if cfg.Slug == "" {
				cfg.Slug = strings.TrimSuffix(entry.Name(), ext)
			}
			if cfg.Slug == "" || cfg.ProfileSlug == "" {
				return nil, fmt.Errorf("durable config %s missing slug or profile_slug", path)
			}
			if _, ok := seen[cfg.Slug]; ok {
				continue
			}
			seen[cfg.Slug] = struct{}{}
			cfg.Source = dir.source
			cfg.SourceRef = path
			out = append(out, cfg)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}

func managedDurableMetadata(raw string) (map[string]string, bool) {
	if strings.TrimSpace(raw) == "" {
		return nil, false
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, false
	}
	if out["managed_source"] == "" {
		return nil, false
	}
	return out, true
}

func ptrTime(t time.Time) *time.Time {
	return &t
}

func SaveManagedAgentProfile(st *store.Store, configRoot string, profile *store.AgentProfile, procedures []agent.ProcedureDefinition) (*store.AgentProfile, error) {
	if err := agent.EnsureManagedConfigDirs(configRoot); err != nil {
		return nil, err
	}
	path, err := agent.ManagedAgentPath(configRoot, profile.Slug)
	if err != nil {
		return nil, err
	}
	if err := agent.WriteManagedAgentProfile(path, profile, procedures); err != nil {
		return nil, err
	}
	def, err := agent.ParseMDFile(path)
	if err != nil {
		return nil, err
	}
	def.Source = "user"
	def.SourceRef = path
	if err := IngestAgentDefinition(st, def); err != nil {
		return nil, err
	}
	return st.GetAgentBySlug(profile.Slug)
}

func SaveManagedDurableAgentConfig(st *store.Store, configRoot string, cfg ManagedDurableAgentConfig) (*store.DurableAgentInstance, error) {
	if err := agent.EnsureManagedConfigDirs(configRoot); err != nil {
		return nil, err
	}
	path, err := ManagedDurableAgentPath(configRoot, cfg.Slug)
	if err != nil {
		return nil, err
	}
	if err := WriteManagedDurableAgentConfig(path, cfg); err != nil {
		return nil, err
	}
	cfg.Source = "user"
	cfg.SourceRef = path
	return syncManagedDurableAgentConfig(st, cfg)
}
