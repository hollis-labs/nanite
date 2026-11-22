package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/hollis-labs/nanite/internal/modelsdevtest"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
	"github.com/hollis-labs/substrate/harness/adapters/provider"
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
	if createErr := storetest.HistoricalProfile(ctx, seedStore, profile); createErr != nil {
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
	instance.ID = "retained-file-instance"
	if _, createErr := seedStore.DB.ExecContext(ctx, `INSERT INTO durable_agent_instances(id,name,slug,profile_id,lifecycle_class,provider,model,runtime_kind,launch_source_type,launch_source_id,status,metadata_json) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, instance.ID, instance.Name, instance.Slug, instance.ProfileID, instance.LifecycleClass, instance.Provider, instance.Model, instance.RuntimeKind, instance.LaunchSourceType, instance.LaunchSourceID, instance.Status, instance.MetadataJSON); createErr != nil {
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
	if _, scheduleErr := seedStore.DB.ExecContext(ctx, `INSERT INTO agent_schedules(id,agent_id,name,schedule_kind,schedule_spec,body,priority,status,created_at,created_by,next_run,max_retries,on_fail,job_type,job_payload) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, schedule.ID, schedule.AgentID, schedule.Name, schedule.ScheduleKind, schedule.ScheduleSpec, schedule.Body, schedule.Priority, schedule.Status, schedule.CreatedAt, schedule.CreatedBy, schedule.NextRun, schedule.MaxRetries, schedule.OnFail, schedule.JobType, schedule.JobPayload); scheduleErr != nil {
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

	beforeBoot, err := snapshotFormerFileManagedState(ctx, reopened)
	if err != nil || len(beforeBoot.Instances) != 1 || len(beforeBoot.Schedules) != 1 {
		t.Fatalf("retained historical state: %+v %v", beforeBoot, err)
	}
	if _, err := reopened.GetDurableAgentInstanceBySlug(ctx, instance.Slug); !errors.Is(err, store.ErrDurableAgentInstanceNotFound) {
		t.Fatalf("historical instance entered fresh runtime: %v", err)
	}

	container, err := NewContainer(ContainerConfig{
		ModelCatalogOptions:      modelsdevtest.Options(t),
		Store:                    reopened,
		Providers:                provider.NewRegistry(),
		WorkingDir:               root,
		DisableEmbeddedTesseract: true,
	})
	if err != nil {
		t.Fatalf("boot container without former config tree: %v", err)
	}
	t.Cleanup(container.Shutdown)

	afterBoot, err := snapshotFormerFileManagedState(ctx, reopened)
	if err != nil || !reflect.DeepEqual(afterBoot, beforeBoot) {
		t.Fatalf("boot changed retained historical state: before=%#v after=%#v err=%v", beforeBoot, afterBoot, err)
	}
	if _, err := reopened.GetAgentForActor(ctx, profile.ID); !errors.Is(err, store.ErrVerifiedActorRequired) {
		t.Fatalf("historical profile became an actor: %v", err)
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
		ModelCatalogOptions: modelsdevtest.Options(t),
		Store:               migrated, Providers: provider.NewRegistry(), WorkingDir: root, DisableEmbeddedTesseract: true,
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

// Historical rows are inspected explicitly; ordinary instance/schedule readers
// deliberately do not expose these rows after the fresh partition cut.
type formerFileManagedSnapshot struct{ Instances, Schedules [][]any }

func snapshotFormerFileManagedState(ctx context.Context, st *store.Store) (formerFileManagedSnapshot, error) {
	read := func(query string) ([][]any, error) {
		rows, err := st.DB.QueryContext(ctx, query)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		cols, err := rows.Columns()
		if err != nil {
			return nil, err
		}
		out := [][]any{}
		for rows.Next() {
			cells := make([]any, len(cols))
			dest := make([]any, len(cols))
			for i := range cells {
				dest[i] = &cells[i]
			}
			if err := rows.Scan(dest...); err != nil {
				return nil, err
			}
			for i, v := range cells {
				if b, ok := v.([]byte); ok {
					cells[i] = append([]byte(nil), b...)
				}
			}
			out = append(out, cells)
		}
		return out, rows.Err()
	}
	instances, err := read(`SELECT * FROM durable_agent_instances WHERE json_extract(metadata_json,'$.managed_source')='managed_file' ORDER BY id`)
	if err != nil {
		return formerFileManagedSnapshot{}, err
	}
	schedules, err := read(`SELECT * FROM agent_schedules WHERE agent_id IN (SELECT profile_id FROM durable_agent_instances WHERE json_extract(metadata_json,'$.managed_source')='managed_file') ORDER BY id`)
	return formerFileManagedSnapshot{Instances: instances, Schedules: schedules}, err
}
