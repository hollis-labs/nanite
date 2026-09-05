package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
	_ "modernc.org/sqlite"
)

// TestRetiredDurableAgentFileAuthorityPreservesDatabaseState is the negative
// migration case for retiring .nanite/durable-agents. The first assertion is
// intentionally made after reopening the migrated database but before the
// service container starts, so same-boot reconciliation cannot recreate a row
// or schedule and hide a destructive migration. The second assertion proves a
// missing former source file has no effect during boot either.
func TestRetiredDurableAgentFileAuthorityPreservesDatabaseState(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "xdg", "data"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "xdg", "state"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "xdg", "cache"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "xdg", "config"))

	dbPath := filepath.Join(root, "nanite.db")
	seedStore, err := storetest.New(t, ctx, dbPath)
	if err != nil {
		t.Fatalf("create seed store: %v", err)
	}
	profile := &store.AgentProfile{
		Name:         "Retired File Source",
		Slug:         "retired-file-source",
		SystemPrompt: "Persist independently of a deleted config file.",
		Source:       "user",
		Durable:      true,
	}
	if createErr := seedStore.CreateAgent(ctx, profile); createErr != nil {
		t.Fatalf("create profile: %v", createErr)
	}
	instance := &store.DurableAgentInstance{
		Name:             "Retired File Source",
		Slug:             "retired-file-source",
		ProfileID:        profile.ID,
		LifecycleClass:   store.DurableAgentClassProcess,
		Provider:         "anthropic",
		Model:            "claude-sonnet-4-5",
		RuntimeKind:      "cli",
		LaunchSourceType: store.DurableAgentLaunchProcessTick,
		LaunchSourceID:   "retired-file-source",
		Status:           store.DurableAgentStatusSleeping,
		MetadataJSON:     `{"managed_source":"managed_file","managed_config_path":".nanite/durable-agents/retired-file-source.yaml","managed_profile_slug":"retired-file-source"}`,
	}
	if createErr := seedStore.CreateDurableAgentInstance(ctx, instance); createErr != nil {
		t.Fatalf("create durable instance: %v", createErr)
	}
	schedule := store.AgentSchedule{
		ID:           "retired-file-source-schedule",
		AgentID:      profile.ID,
		Name:         "durable-proof",
		ScheduleKind: store.ScheduleKindCron,
		ScheduleSpec: "0 3 * * *",
		Body:         "Prove this schedule survives retirement of its former file source.",
		Priority:     17,
		Status:       store.ScheduleStatusActive,
		CreatedAt:    "2026-09-01T00:00:00Z",
		CreatedBy:    "managed_file",
		NextRun:      "2099-09-02T03:00:00Z",
		MaxRetries:   7,
		OnFail:       store.ScheduleOnFailNotify,
		JobType:      store.ScheduleJobTypeDurableAgentWake,
		JobPayload:   `{"proof":true}`,
	}
	if scheduleErr := seedStore.InsertAgentSchedule(ctx, schedule); scheduleErr != nil {
		t.Fatalf("create schedule: %v", scheduleErr)
	}
	if closeErr := seedStore.Close(ctx); closeErr != nil {
		t.Fatalf("close seed store: %v", closeErr)
	}

	// The former source tree is deliberately absent when migrations and boot
	// run. No reconciliation path is available to recreate either DB row.
	reopened, err := store.New(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen migrated store: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close(context.Background()) })

	beforeBootInstance, err := reopened.GetDurableAgentInstanceBySlug(ctx, instance.Slug)
	if err != nil {
		t.Fatalf("durable instance missing immediately after migrations: %v", err)
	}
	beforeBootSchedule, err := reopened.GetAgentSchedule(ctx, schedule.ID)
	if err != nil {
		t.Fatalf("schedule missing immediately after migrations: %v", err)
	}
	if beforeBootInstance.Status != store.DurableAgentStatusSleeping {
		t.Fatalf("migration changed durable status to %q", beforeBootInstance.Status)
	}

	container, err := NewContainer(ContainerConfig{
		Store:                    reopened,
		Providers:                provider.NewRegistry(),
		WorkingDir:               root,
		DisableEmbeddedTesseract: true,
	})
	if err != nil {
		t.Fatalf("boot container without former config tree: %v", err)
	}
	t.Cleanup(container.Shutdown)

	afterBootInstance, err := reopened.GetDurableAgentInstanceBySlug(ctx, instance.Slug)
	if err != nil {
		t.Fatalf("durable instance missing after boot: %v", err)
	}
	afterBootSchedule, err := reopened.GetAgentSchedule(ctx, schedule.ID)
	if err != nil {
		t.Fatalf("schedule missing after boot: %v", err)
	}
	if !reflect.DeepEqual(afterBootInstance, beforeBootInstance) {
		t.Fatalf("boot changed durable instance after source-file retirement:\n before: %#v\n  after: %#v", beforeBootInstance, afterBootInstance)
	}
	if !reflect.DeepEqual(afterBootSchedule, beforeBootSchedule) {
		t.Fatalf("boot changed schedule after source-file retirement:\n before: %#v\n  after: %#v", beforeBootSchedule, afterBootSchedule)
	}
}

// TestRetiredFileAuthorityRealDatabaseCopySurvivesMigrationAndBoot is an
// opt-in dogfood proof over an isolated SQLite backup of the deployed Nanite
// database. It refuses the deployed file and all aliases of it. Set
// NANITE_REAL_DB_COPY to a disposable writable backup and NANITE_LIVE_DB to
// the read-only source path; the normal test suite skips this case.
func TestRetiredFileAuthorityRealDatabaseCopySurvivesMigrationAndBoot(t *testing.T) {
	copyPath := os.Getenv("NANITE_REAL_DB_COPY")
	if copyPath == "" {
		t.Skip("set NANITE_REAL_DB_COPY to an isolated writable backup")
	}
	livePath := os.Getenv("NANITE_LIVE_DB")
	if livePath == "" {
		t.Fatal("set NANITE_LIVE_DB to the read-only deployed database used to create the backup")
	}
	absCopy, err := validateWritableDatabaseCopy(copyPath, livePath)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	ro, err := sql.Open("sqlite", "file:"+absCopy+"?mode=ro&immutable=1")
	if err != nil {
		t.Fatalf("open backup read-only: %v", err)
	}
	before, err := snapshotFormerFileManagedState(ctx, &store.Store{DB: ro})
	if closeErr := ro.Close(); err == nil && closeErr != nil {
		err = closeErr
	}
	if err != nil {
		t.Fatalf("snapshot backup before migrations: %v", err)
	}
	if len(before.Instances) == 0 || len(before.Schedules) == 0 {
		t.Fatalf("backup lacks positive controls: instances=%d schedules=%d", len(before.Instances), len(before.Schedules))
	}

	migrated, err := openWritableDatabaseCopy(ctx, absCopy, livePath)
	if err != nil {
		t.Fatalf("migrate backup: %v", err)
	}
	t.Cleanup(func() { _ = migrated.Close(context.Background()) })
	afterMigration, err := snapshotFormerFileManagedState(ctx, migrated)
	if err != nil {
		t.Fatalf("snapshot after migrations: %v", err)
	}
	if !reflect.DeepEqual(afterMigration, before) {
		t.Fatalf("migrations changed former file-managed durable state:\n before=%#v\n after=%#v", before, afterMigration)
	}

	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "xdg", "data"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "xdg", "state"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "xdg", "cache"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "xdg", "config"))
	container, err := NewContainer(ContainerConfig{
		Store: migrated, Providers: provider.NewRegistry(), WorkingDir: root, DisableEmbeddedTesseract: true,
	})
	if err != nil {
		t.Fatalf("boot against migrated backup: %v", err)
	}
	t.Cleanup(container.Shutdown)
	afterBoot, err := snapshotFormerFileManagedState(ctx, migrated)
	if err != nil {
		t.Fatalf("snapshot after boot: %v", err)
	}
	if !reflect.DeepEqual(afterBoot, before) {
		t.Fatalf("boot changed former file-managed durable state:\n before=%#v\n after=%#v", before, afterBoot)
	}
}

var errWritableDatabaseIsLive = errors.New("refusing to open deployed database for write")

// validateWritableDatabaseCopy is the mandatory guard before the opt-in proof
// passes a database path to writable Store.New.
func validateWritableDatabaseCopy(copyPath, livePath string) (string, error) {
	absCopy, err := filepath.Abs(copyPath)
	if err != nil {
		return "", fmt.Errorf("resolve database copy path: %w", err)
	}
	absLive, err := filepath.Abs(livePath)
	if err != nil {
		return "", fmt.Errorf("resolve deployed database path: %w", err)
	}
	resolvedCopy, err := filepath.EvalSymlinks(absCopy)
	if err != nil {
		return "", fmt.Errorf("resolve database copy symlinks: %w", err)
	}
	resolvedLive, err := filepath.EvalSymlinks(absLive)
	if err != nil {
		return "", fmt.Errorf("resolve deployed database symlinks: %w", err)
	}
	// #nosec G703 -- these explicit opt-in test paths are only statted so
	// os.SameFile can reject aliases before any writable database open.
	copyInfo, err := os.Stat(resolvedCopy)
	if err != nil {
		return "", fmt.Errorf("stat database copy: %w", err)
	}
	// #nosec G703 -- read-only identity inspection of the explicit deployed
	// path is the safety control; Store.New is never called with this path.
	liveInfo, err := os.Stat(resolvedLive)
	if err != nil {
		return "", fmt.Errorf("stat deployed database: %w", err)
	}
	if resolvedCopy == resolvedLive || os.SameFile(copyInfo, liveInfo) {
		return "", fmt.Errorf("%w: %s", errWritableDatabaseIsLive, absCopy)
	}
	return resolvedCopy, nil
}

func openWritableDatabaseCopy(ctx context.Context, copyPath, livePath string) (*store.Store, error) {
	safePath, err := validateWritableDatabaseCopy(copyPath, livePath)
	if err != nil {
		return nil, err
	}
	return store.New(ctx, safePath)
}

func TestWritableDatabaseCopyRejectsLiveDatabaseAliases(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	livePath := filepath.Join(root, "live.db")
	live, err := store.New(ctx, livePath)
	if err != nil {
		t.Fatalf("create live fixture: %v", err)
	}
	if err := live.Close(ctx); err != nil {
		t.Fatalf("close live fixture: %v", err)
	}

	symlinkPath := filepath.Join(root, "live-symlink.db")
	if err := os.Symlink(livePath, symlinkPath); err != nil {
		t.Fatalf("create symlink alias: %v", err)
	}
	hardlinkPath := filepath.Join(root, "live-hardlink.db")
	if err := os.Link(livePath, hardlinkPath); err != nil {
		t.Fatalf("create hardlink alias: %v", err)
	}

	for name, candidate := range map[string]string{
		"exact path": livePath,
		"symlink":    symlinkPath,
		"hardlink":   hardlinkPath,
	} {
		t.Run(name, func(t *testing.T) {
			opened, openErr := openWritableDatabaseCopy(ctx, candidate, livePath)
			if opened != nil {
				_ = opened.Close(ctx)
			}
			if !errors.Is(openErr, errWritableDatabaseIsLive) {
				t.Fatalf("open error = %v, want alias refusal before Store.New", openErr)
			}
		})
	}
}

type formerFileManagedSnapshot struct {
	Instances []store.DurableAgentInstance
	Schedules []store.AgentSchedule
}

func snapshotFormerFileManagedState(ctx context.Context, st *store.Store) (formerFileManagedSnapshot, error) {
	instances, err := st.ListDurableAgentInstances(ctx, true)
	if err != nil {
		return formerFileManagedSnapshot{}, err
	}
	out := formerFileManagedSnapshot{}
	profileIDs := make(map[string]struct{})
	for _, inst := range instances {
		var meta map[string]any
		if json.Unmarshal([]byte(inst.MetadataJSON), &meta) != nil || meta["managed_source"] != "managed_file" {
			continue
		}
		out.Instances = append(out.Instances, inst)
		profileIDs[inst.ProfileID] = struct{}{}
	}
	sort.Slice(out.Instances, func(i, j int) bool { return out.Instances[i].ID < out.Instances[j].ID })
	seenSchedules := make(map[string]struct{})
	for profileID := range profileIDs {
		schedules, err := st.ListAgentSchedules(ctx, profileID)
		if err != nil {
			return formerFileManagedSnapshot{}, fmt.Errorf("list schedules for %s: %w", profileID, err)
		}
		for _, schedule := range schedules {
			if _, seen := seenSchedules[schedule.ID]; seen {
				continue
			}
			seenSchedules[schedule.ID] = struct{}{}
			out.Schedules = append(out.Schedules, schedule)
		}
	}
	sort.Slice(out.Schedules, func(i, j int) bool { return out.Schedules[i].ID < out.Schedules[j].ID })
	return out, nil
}
