package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/store"
)

// TestSyncManagedDurableAgentSchedule_PreservesNonActiveStatus is a
// regression test for a code-review finding on CW-20260816-0021: the
// original fix only preserved a manually 'paused' status across re-sync,
// silently flipping 'expired' schedules back to 'active' on every boot.
// Covers both non-active statuses the schema allows (paused, expired) plus
// the baseline active-stays-active case.
func TestSyncManagedDurableAgentSchedule_PreservesNonActiveStatus(t *testing.T) {
	ctx := context.Background()
	st, err := store.New(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	sch := ManagedDurableAgentSchedule{
		Name: "lint-and-export",
		Kind: store.ScheduleKindCron,
		Spec: "0 3 * * *",
		Body: "run the thing",
	}

	for _, tc := range []struct {
		name           string
		existingStatus string
		wantStatus     string
	}{
		{"active stays active", store.ScheduleStatusActive, store.ScheduleStatusActive},
		{"paused is preserved", store.ScheduleStatusPaused, store.ScheduleStatusPaused},
		{"expired is preserved", store.ScheduleStatusExpired, store.ScheduleStatusExpired},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Each subtest gets its own profile so the deterministic
			// schedule ID (derived from profileID+name) doesn't collide
			// across subtests sharing this test's *testing.T store.
			profileID := "profile-" + tc.name
			if err := st.CreateAgent(&store.AgentProfile{
				ID:           profileID,
				Name:         tc.name,
				Slug:         tc.name,
				Class:        "process",
				SystemPrompt: "test",
				Source:       "project",
			}); err != nil {
				t.Fatalf("CreateAgent: %v", err)
			}

			id := managedDurableAgentScheduleID(profileID, sch.Name)
			// First sync creates the row.
			if err := syncManagedDurableAgentSchedule(ctx, st, profileID, sch); err != nil {
				t.Fatalf("initial sync: %v", err)
			}
			// Simulate engine/operator state: bump fired_count and set status.
			if err := st.UpdateAgentScheduleStatus(ctx, id, tc.existingStatus); err != nil {
				t.Fatalf("UpdateAgentScheduleStatus: %v", err)
			}
			if err := st.BumpAgentScheduleFireCount(ctx, id, time.Now()); err != nil {
				t.Fatalf("BumpAgentScheduleFireCount: %v", err)
			}

			// Re-sync, as happens on every boot.
			if err := syncManagedDurableAgentSchedule(ctx, st, profileID, sch); err != nil {
				t.Fatalf("re-sync: %v", err)
			}

			got, err := st.GetAgentSchedule(ctx, id)
			if err != nil {
				t.Fatalf("GetAgentSchedule: %v", err)
			}
			if got.Status != tc.wantStatus {
				t.Fatalf("status after re-sync = %q, want %q", got.Status, tc.wantStatus)
			}
			if got.FiredCount != 1 {
				t.Fatalf("fired_count after re-sync = %d, want 1 (re-sync must not reset it)", got.FiredCount)
			}
		})
	}
}
