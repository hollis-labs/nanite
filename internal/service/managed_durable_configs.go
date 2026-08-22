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
	"github.com/hollis-labs/nanite/internal/pathsafe"
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
// store.ScheduleKindCron / store.ScheduleKindOneShot — the only two
// schedule_kind values agent_schedules' CHECK constraint accepts as of
// migration 127 (TASKS/scheduling/01-schema-schedule-kind-collapse-and-
// retry-columns.md; the other three historical values —
// every_n_ticks/on_tick/on_event — were dropped from the schema entirely,
// not merely deprecated). Spec's shape depends on Kind (e.g. a 5-field
// cron expression for ScheduleKindCron). Body is the
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

// ManagedDurableAgentPath resolves a durable-agent slug to its managed-file
// path under configRoot's "durable-agents" subdirectory. slug is validated
// against the canonical URL-safe slug pattern (agent.ValidateSlug) before
// the join, and the join itself is confined via pathsafe.ResolveUnder as
// defense in depth — the durable-agent-config sibling of GO-AGENT-001, same
// defect shape, same fix.
func ManagedDurableAgentPath(configRoot, slug string) (string, error) {
	if strings.TrimSpace(configRoot) == "" {
		return "", fmt.Errorf("managed durable config root is required")
	}
	if strings.TrimSpace(slug) == "" {
		return "", fmt.Errorf("managed durable slug is required")
	}
	if err := agent.ValidateSlug(slug); err != nil {
		return "", fmt.Errorf("managed durable agent: %w", err)
	}
	durableDir := filepath.Join(configRoot, "durable-agents")
	resolved, err := pathsafe.ResolveUnder(durableDir, slug+".yaml")
	if err != nil {
		return "", fmt.Errorf("managed durable agent: resolve managed path: %w", err)
	}
	return resolved, nil
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
	instances, err := st.ListDurableAgentInstances(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, true)
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
		if _, err := st.SyncDurableAgentInstanceConfig(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, &store.DurableAgentInstance{
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
	profile, err := st.GetAgentBySlug(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, cfg.ProfileSlug)
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
	saved, err := st.SyncDurableAgentInstanceConfig(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, inst)
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
// and reset a cron schedule's due-ness reference point to "now" on every
// redeploy (this used to matter for wakeScheduleDue's last_fired_at/
// created_at fallback chain, retired in full by TASKS/scheduling/
// 05-engine-wiring-and-full-replace.md — see next_run below for what
// replaced it) — defeating the whole point of persisting fire state. So
// this preserves that trio from any existing row with the same ID, the
// same discipline SyncDurableAgentInstanceConfig already uses for
// CurrentSessionID/FailureReason/ArchivedAt above. It also preserves any
// existing non-active status (paused, expired, ...) rather than silently
// reactivating a schedule an operator turned off or that already expired
// through the admin surface or a fired one_shot.
//
// next_run/max_retries/on_fail/job_type/job_payload preservation (real bug
// found and fixed while wiring TASKS/scheduling/05-engine-wiring-and-full-
// replace.md, not present before that task): before this fix, this
// function only preserved FiredCount/LastFiredAt/CreatedAt/Status on an
// existing-row re-sync — next_run (and the other four migration-127
// columns) fell through to InsertAgentSchedule's zero-value defaulting on
// every single re-sync, i.e. every container boot after the first. Since
// SyncManagedDurableAgentConfigs runs unconditionally on every boot
// (managed_durable_configs.go's own package doc), and
// store.backfillScheduleNextRun only ever backfills next_run once, at
// Store.New() time — strictly *before* this function's caller runs, in the
// same boot — this re-sync would silently null out next_run on every
// single restart, permanently un-scheduling the row from
// go-scheduler.Engine's perspective (a NULL/zero NextRun is "unscheduled
// and skipped" by go-scheduler's own convention). That would have made the
// one real production schedule (Loom Curator's lint-and-export) invisible
// to the new engine on every boot after the very first migration-127
// backfill — caught by this task's own required live dogfeed, fixed here
// rather than left for that dogfeed to merely report. An existing row's
// next_run/max_retries/on_fail/job_type/job_payload are now preserved the
// same way FiredCount/LastFiredAt/CreatedAt/Status already were.
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
		if existing.Status != store.ScheduleStatusActive {
			row.Status = existing.Status
		}
		// Preserve the go-scheduler-facing columns across every re-sync —
		// see this function's doc comment above for the bug this closes.
		// A defensive fallback still computes a fresh next_run if the
		// existing row somehow has none (e.g. it predates migration 127
		// and this process's own backfillScheduleNextRun pass hasn't run
		// yet for some reason) rather than leaving the row permanently
		// unscheduled.
		row.NextRun = existing.NextRun
		if row.NextRun == "" {
			row.NextRun = store.ComputeAgentScheduleNextRun(row.ScheduleKind, row.ScheduleSpec, time.Now()).Format(time.RFC3339)
		}
		row.MaxRetries = existing.MaxRetries
		row.OnFail = existing.OnFail
		row.JobType = existing.JobType
		row.JobPayload = existing.JobPayload
	case errors.Is(err, store.ErrAgentScheduleNotFound):
		// First sync for this schedule — InsertAgentSchedule defaults
		// CreatedAt/MaxRetries/OnFail/JobType/JobPayload when left empty,
		// but next_run has no such default (an empty value means a real,
		// meaningful "unscheduled" everywhere else in this codebase) — so
		// a brand-new managed schedule needs next_run computed here,
		// explicitly, rather than waiting for the next process restart's
		// backfillScheduleNextRun pass to make it live.
		row.NextRun = store.ComputeAgentScheduleNextRun(row.ScheduleKind, row.ScheduleSpec, time.Now()).Format(time.RFC3339)
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
	return st.GetAgentBySlug(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, profile.Slug)
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
