package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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
	t.Cleanup(func() { st.Close(context.Background()) })

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
			if err := st.CreateAgent(context.Background(), &store.AgentProfile{
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

// TestManagedDurableAgentPath_RejectsTraversalSlug is the durable-agent
// sibling of GO-AGENT-001: ManagedDurableAgentPath shares the exact same
// unguarded filepath.Join shape agent.ManagedAgentPath had — see this
// task's "Scope note" for why this is in scope despite not being in
// GO-AGENT-001's original evidence list.
func TestManagedDurableAgentPath_RejectsTraversalSlug(t *testing.T) {
	root := t.TempDir()
	for _, malicious := range []string{
		"../evil",
		"../../etc/passwd",
		"a/b",
		"UPPER",
		"with space",
	} {
		if path, err := ManagedDurableAgentPath(root, malicious); err == nil {
			t.Errorf("ManagedDurableAgentPath(%q) = %q, nil, want a rejection error", malicious, path)
		}
	}
}

// TestManagedDurableAgentPath_AcceptsLegitimateSlug pins the happy path.
func TestManagedDurableAgentPath_AcceptsLegitimateSlug(t *testing.T) {
	root := t.TempDir()
	path, err := ManagedDurableAgentPath(root, "torque-supervisor")
	if err != nil {
		t.Fatalf("ManagedDurableAgentPath: %v", err)
	}
	if filepath.Base(path) != "torque-supervisor.yaml" {
		t.Fatalf("path base = %q, want torque-supervisor.yaml", filepath.Base(path))
	}
}

// TestWriteManagedDurableAgentConfig_RoundTrips closes out
// WriteManagedDurableAgentConfig's previous 0.0% coverage (GO-AGENT-002).
func TestWriteManagedDurableAgentConfig_RoundTrips(t *testing.T) {
	root := t.TempDir()
	path, err := ManagedDurableAgentPath(root, "torque-supervisor")
	if err != nil {
		t.Fatalf("ManagedDurableAgentPath: %v", err)
	}
	cfg := ManagedDurableAgentConfig{
		Name:           "Torque Supervisor",
		Slug:           "torque-supervisor",
		ProfileSlug:    "supervisor-profile",
		LifecycleClass: store.DurableAgentClassProcess,
	}
	if err := WriteManagedDurableAgentConfig(path, cfg); err != nil {
		t.Fatalf("WriteManagedDurableAgentConfig: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !strings.Contains(string(raw), "torque-supervisor") {
		t.Fatalf("written file missing slug:\n%s", raw)
	}
}

// TestSaveManagedDurableAgentConfig_RejectsSlugTraversalOnCreate is the
// service-level regression for the durable-agent create path
// (handleCreateDurableAgent -> saveManagedDurableInstance ->
// SaveManagedDurableAgentConfig).
func TestSaveManagedDurableAgentConfig_RejectsSlugTraversalOnCreate(t *testing.T) {
	ctx := context.Background()
	st, err := store.New(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { st.Close(context.Background()) })
	if err := st.CreateAgent(context.Background(), &store.AgentProfile{ID: "profile-create-traversal", Name: "p", Slug: "p", SystemPrompt: "x", Source: "project"}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	root := t.TempDir()

	for _, malicious := range []string{"../evil", "../../etc/passwd", "a/b"} {
		if _, err := SaveManagedDurableAgentConfig(st, root, ManagedDurableAgentConfig{
			Slug:        malicious,
			ProfileSlug: "p",
		}); err == nil {
			t.Errorf("slug %q: expected rejection", malicious)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "evil.yaml")); !os.IsNotExist(err) {
		t.Fatalf("traversal wrote above configRoot: %v", err)
	}
}

// TestSaveManagedDurableAgentConfig_RejectsSlugTraversalOnRenameShapedUpdate
// mirrors the API's rename shape (handleUpdateDurableAgent sets a new slug
// on an existing instance and re-saves): a legitimate instance already
// exists, and a subsequent save call with a crafted slug must be rejected
// without touching the existing file.
func TestSaveManagedDurableAgentConfig_RejectsSlugTraversalOnRenameShapedUpdate(t *testing.T) {
	ctx := context.Background()
	st, err := store.New(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { st.Close(context.Background()) })
	if err := st.CreateAgent(context.Background(), &store.AgentProfile{ID: "profile-rename-traversal", Name: "p", Slug: "p", SystemPrompt: "x", Source: "project"}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	root := t.TempDir()

	if _, err := SaveManagedDurableAgentConfig(st, root, ManagedDurableAgentConfig{
		Name:        "Torque Supervisor",
		Slug:        "torque-supervisor",
		ProfileSlug: "p",
	}); err != nil {
		t.Fatalf("initial save: %v", err)
	}
	legitPath, err := ManagedDurableAgentPath(root, "torque-supervisor")
	if err != nil {
		t.Fatalf("ManagedDurableAgentPath: %v", err)
	}
	if _, err := os.Stat(legitPath); err != nil {
		t.Fatalf("precondition: legit file missing: %v", err)
	}

	if _, err := SaveManagedDurableAgentConfig(st, root, ManagedDurableAgentConfig{
		Slug:        "../evil",
		ProfileSlug: "p",
	}); err == nil {
		t.Error("expected rejection for a traversal slug on a rename-shaped update")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "evil.yaml")); !os.IsNotExist(err) {
		t.Fatalf("traversal wrote above configRoot: %v", err)
	}
	if _, err := os.Stat(legitPath); err != nil {
		t.Fatalf("legit file must survive a rejected rename attempt: %v", err)
	}
}
