package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/hollis-labs/nanite/internal/store"
)

const (
	loomCuratorDurableSlug = "loom-curator"
	loomLintExportName     = "lint-and-export"
)

// loomLintExportSchedule is Nanite's canonical, database-provisioned default
// for the Loom Curator pilot. It replaces the former project YAML directive;
// the durable-agent creation path persists it once and the database row is
// authoritative thereafter.
func loomLintExportSchedule(profileID string, now time.Time) store.AgentSchedule {
	return store.AgentSchedule{
		ID:           builtinDurableAgentScheduleID(profileID, loomLintExportName),
		AgentID:      profileID,
		Name:         loomLintExportName,
		ScheduleKind: store.ScheduleKindCron,
		ScheduleSpec: "0 3 * * *",
		Body: `Run your scheduled_lint_and_export procedure now: sweep the ` + "`nanite`" + `
wiki bundle only (apps/loom/docs/architecture.md §10 — never touch any
other bundle). Call loom_bundle_conformance scoped to bundle=nanite for
structural lint. For any finding below the confidence threshold, do
not fix or drop it silently — route it into Fragments Engine's inbox
for review via message_*, the same mechanism
write_or_stage_page already uses for staged pages. Then call
loom_export_bundle scoped to bundle=nanite to regenerate the bundle's
OKF export directory from current wiki_pages rows.`,
		Status:    store.ScheduleStatusActive,
		CreatedBy: "builtin",
		NextRun: store.ComputeAgentScheduleNextRun(
			store.ScheduleKindCron,
			"0 3 * * *",
			now,
		).Format(time.RFC3339),
	}
}

// provisionBuiltinDurableSchedules persists required builtin schedules only
// when their durable instance is first provisioned. Existing rows are the
// authority: matching by profile and semantic name preserves both the former
// managed_file row and any operator-customized replacement without an upsert.
func (s *durableAgentService) provisionBuiltinDurableSchedules(ctx context.Context, inst *store.DurableAgentInstance) error {
	if inst == nil || inst.Slug != loomCuratorDurableSlug {
		return nil
	}
	existing, err := s.store.ListAgentSchedules(ctx, inst.ProfileID)
	if err != nil {
		return fmt.Errorf("list schedules for builtin provisioning: %w", err)
	}
	for _, schedule := range existing {
		if schedule.Name == loomLintExportName {
			return nil
		}
	}
	if err := s.store.InsertAgentSchedule(ctx, loomLintExportSchedule(inst.ProfileID, time.Now().UTC())); err != nil {
		return fmt.Errorf("provision builtin schedule %s: %w", loomLintExportName, err)
	}
	return nil
}

// builtinDurableAgentScheduleID retains the former deterministic identifier so
// newly provisioned databases and already-deployed rows share one stable key.
func builtinDurableAgentScheduleID(profileID, name string) string {
	sum := sha256.Sum256([]byte(profileID + ":" + name))
	return "managed-schedule-" + hex.EncodeToString(sum[:16])
}
