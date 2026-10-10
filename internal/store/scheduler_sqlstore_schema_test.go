package store

import (
	"context"
	"reflect"
	"testing"
	"time"

	gosched "github.com/hollis-labs/libs/util/scheduler"
	"github.com/hollis-labs/libs/util/scheduler/sqlstore"
)

// The additive schema is removable only while it carries no material. Once
// staging data or an authority marker exists, Down must preserve it intact.
func TestSchedulerSQLSchemaRollbackGuard(t *testing.T) {
	for _, material := range []string{"empty", "schedule", "fire", "authority"} {
		t.Run(material, func(t *testing.T) {
			ctx := context.Background()
			host := newTestStore(t)
			provider := newMigrationProvider(t, host)
			if _, downErr := provider.DownTo(ctx, 172); downErr != nil {
				t.Fatal(downErr)
			}
			agent := makeTestAgentRawSQL(t, host, "history")
			at := time.Date(2026, 3, 4, 5, 6, 7, 123456789, time.UTC)
			// This downgrade test supplies historical rows explicitly; current
			// actor APIs never dual-read a schema from before the fresh cut.
			scheduleID := "historical"
			if _, err := host.DB.ExecContext(ctx, `INSERT INTO agent_schedules(id,agent_id,name,schedule_kind,schedule_spec,body,next_run,job_type,job_payload) VALUES(?,?,'history','cron','* * * * *','retained body',?,'command_run','{"command":"noop"}')`, scheduleID, agent.ID, at.UTC().Format(time.RFC3339Nano)); err != nil {
				t.Fatal(err)
			}
			if _, err := host.DB.ExecContext(ctx, `INSERT INTO schedule_runs(id,schedule_id,run_id,scheduled_at,fired_at,status,job_type,job_payload) VALUES('legacy-row-alias',?,'legacy-fire',?,?,'pending','command_run','{"command":"noop"}')`, scheduleID, at.UTC().Format(time.RFC3339Nano), at.UTC().Format(time.RFC3339Nano)); err != nil {
				t.Fatal(err)
			}
			readHistory := func() []string {
				t.Helper()
				var body, next, run, status string
				if err := host.DB.QueryRowContext(ctx, `SELECT body,next_run FROM agent_schedules WHERE id=?`, scheduleID).Scan(&body, &next); err != nil {
					t.Fatal(err)
				}
				if err := host.DB.QueryRowContext(ctx, `SELECT run_id,status FROM schedule_runs WHERE id='legacy-row-alias'`).Scan(&run, &status); err != nil {
					t.Fatal(err)
				}
				return []string{body, next, run, status}
			}
			original := readHistory()
			assertHistory := func() {
				t.Helper()
				if got := readHistory(); !reflect.DeepEqual(got, original) {
					t.Fatalf("historical rows changed: %v", got)
				}
			}
			if _, upErr := provider.UpTo(ctx, 173); upErr != nil {
				t.Fatal(upErr)
			}
			assertHistory()
			shared, err := NewSchedulerSQLStore(host)
			if err != nil {
				t.Fatal(err)
			}
			switch material {
			case "schedule":
				raw, rawErr := sqlstore.New(host.DB)
				if rawErr != nil {
					t.Fatal(rawErr)
				}
				if operationErr := raw.CreateSchedule(ctx, gosched.Schedule{ID: "retained", NextRun: at, Enabled: true}); operationErr != nil {
					t.Fatal(operationErr)
				}
			case "fire":
				if _, operationErr := host.DB.ExecContext(ctx, `INSERT INTO gosched_fires(id,schedule_id,scheduled_at,fired_at,claim_expires_at,status,next_attempt_at) VALUES('retained','historical',?,?,?,'succeeded',?)`, schedulerTime(at), schedulerTime(at), schedulerTime(time.Time{}), schedulerTime(time.Time{})); operationErr != nil {
					t.Fatal(operationErr)
				}
			case "authority":
				if _, operationErr := host.DB.ExecContext(ctx, `INSERT INTO scheduler_storage_authority(singleton,authority,migrated_at) VALUES(1,'sqlstore-v1',?)`, schedulerTime(at)); operationErr != nil {
					t.Fatal(operationErr)
				}
			}
			_, err = provider.DownTo(ctx, 172)
			if material == "empty" {
				if err != nil {
					t.Fatal(err)
				}
				if _, operationErr := shared.ListSchedules(ctx); operationErr == nil {
					t.Fatal("empty Down left lifecycle schema installed")
				}
				assertHistory()
				if _, upErr := provider.UpTo(ctx, 173); upErr != nil {
					t.Fatal(upErr)
				}
				assertHistory()
				if _, replayErr := provider.UpTo(ctx, 173); replayErr != nil {
					t.Fatal(replayErr)
				}
				assertHistory()
			} else {
				if err == nil {
					t.Fatal("destructive Down accepted durable material")
				}
				assertHistory()
				if _, err := shared.ListSchedules(ctx); err != nil {
					t.Fatalf("failed Down damaged schema: %v", err)
				}
				switch material {
				case "schedule":
					if _, found, err := shared.GetSchedule(ctx, "retained"); err != nil || !found {
						t.Fatalf("schedule lost: %v %v", found, err)
					}
				case "fire":
					if _, found, err := shared.GetFire(ctx, "retained"); err != nil || !found {
						t.Fatalf("fire lost: %v %v", found, err)
					}
				case "authority":
					var authority string
					if err := host.DB.QueryRowContext(ctx, `SELECT authority FROM scheduler_storage_authority WHERE singleton=1`).Scan(&authority); err != nil || authority != "sqlstore-v1" {
						t.Fatalf("authority lost: %s %v", authority, err)
					}
				}
			}
		})
	}
}
