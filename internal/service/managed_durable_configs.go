package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	// Schedule is an optional structured directive that gets seeded as a
	// real agent_schedules row once this instance's profile ID is known
	// (see syncManagedDurableAgentConfig). Distinct from the free-text
	// Metadata["schedule"] label (e.g. Atlas Curator's "nightly") which
	// stays a descriptive-only string for backward compat — this field is
	// the machine-parsed source of truth for CW-20260816-0021's
	// generalized schedule-seeding mechanism.
	Schedule  *ManagedDurableAgentSchedule `json:"schedule,omitempty" yaml:"schedule,omitempty"`
	Archived  bool                         `json:"archived,omitempty" yaml:"archived,omitempty"`
	Source    string                       `json:"-" yaml:"-"`
	SourceRef string                       `json:"-" yaml:"-"`
}

// ManagedDurableAgentSchedule declares a real agent_schedules row for a
// file-dropped process-class instance. Kind must be one of
// store.ScheduleKindCron / ScheduleKindEveryNTicks / ScheduleKindOnTick /
// ScheduleKindOneShot / ScheduleKindOnEvent; Spec's shape depends on Kind
// (e.g. a 5-field cron expression for ScheduleKindCron). Body is the
// instruction text delivered as the woken session's first user turn when
// this schedule fires (see durable_wake.go's RunDue, which forwards
// Schedule.Body into DurableAgentWakePayload.Prompt) — write it as a
// directive to the agent, not as free-form notes.
type ManagedDurableAgentSchedule struct {
	Name     string `json:"name" yaml:"name"`
	Kind     string `json:"kind" yaml:"kind"`
	Spec     string `json:"spec,omitempty" yaml:"spec,omitempty"`
	Body     string `json:"body" yaml:"body"`
	Priority int64  `json:"priority,omitempty" yaml:"priority,omitempty"`
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
	saved, err := st.SyncDurableAgentInstanceConfig(inst)
	if err != nil {
		return nil, err
	}
	// This is the earliest point in the boot sequence a file-dropped
	// instance's real profile ID is known (ReconcileManagedAgentIDs +
	// AutoIngestAgents have already run by the time
	// SyncManagedDurableAgentConfigs is called from container.go) — so
	// it's the right hook for real agent_schedules seeding, per
	// CW-20260816-0021's trace. profile.ID (not saved.ID, the instance
	// ID) is the correct FK — ListDue/ListSchedules key agent_schedules
	// lookups by inst.ProfileID, not the instance ID.
	if cfg.Schedule != nil {
		if err := syncManagedDurableAgentSchedule(context.Background(), st, profile.ID, *cfg.Schedule); err != nil {
			return nil, fmt.Errorf("managed durable config %s schedule %s: %w", cfg.Slug, cfg.Schedule.Name, err)
		}
	}
	return saved, nil
}

// syncManagedDurableAgentSchedule upserts a real agent_schedules row for a
// file-dropped instance's structured schedule: block, keyed by a
// deterministic ID derived from profileID+name so re-running this on every
// container boot (SyncManagedDurableAgentConfigs's normal cadence) updates
// the same row instead of duplicating it.
//
// InsertAgentSchedule is INSERT OR REPLACE — a naive re-insert on every
// boot would silently reset fired_count/last_fired_at/created_at to zero
// values each time, which would re-arm an already-fired one_shot schedule
// and reset a cron schedule's due-ness reference point (wakeScheduleDue
// falls back to created_at when last_fired_at is empty) to "now" on every
// redeploy — defeating the whole point of persisting fire state. So this
// preserves that trio from any existing row with the same ID, the same
// discipline SyncDurableAgentInstanceConfig already uses for
// CurrentSessionID/FailureReason/ArchivedAt above. It also preserves an
// operator's manual 'paused' status rather than silently reactivating a
// schedule they turned off through the admin surface.
func syncManagedDurableAgentSchedule(ctx context.Context, st *store.Store, profileID string, sch ManagedDurableAgentSchedule) error {
	row := store.AgentSchedule{
		ID:           managedDurableAgentScheduleID(profileID, sch.Name),
		AgentID:      profileID,
		Name:         sch.Name,
		ScheduleKind: sch.Kind,
		ScheduleSpec: sch.Spec,
		Body:         sch.Body,
		Priority:     sch.Priority,
		Status:       store.ScheduleStatusActive,
		CreatedBy:    managedDurableConfigSource,
	}
	existing, err := st.GetAgentSchedule(ctx, row.ID)
	switch {
	case err == nil && existing != nil:
		row.FiredCount = existing.FiredCount
		row.LastFiredAt = existing.LastFiredAt
		row.CreatedAt = existing.CreatedAt
		if existing.Status == store.ScheduleStatusPaused {
			row.Status = store.ScheduleStatusPaused
		}
	case errors.Is(err, store.ErrAgentScheduleNotFound):
		// First sync for this schedule — InsertAgentSchedule defaults
		// CreatedAt to datetime('now') when left empty.
	default:
		return err
	}
	return st.InsertAgentSchedule(ctx, row)
}

// managedDurableAgentScheduleID derives a stable agent_schedules.id from
// profileID+name (sha256, first 16 bytes hex-encoded) so re-syncing the
// same managed config across boots upserts the same row deterministically
// instead of minting a fresh random ID every time.
func managedDurableAgentScheduleID(profileID, name string) string {
	sum := sha256.Sum256([]byte(profileID + ":" + name))
	return "managed-schedule-" + hex.EncodeToString(sum[:16])
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
