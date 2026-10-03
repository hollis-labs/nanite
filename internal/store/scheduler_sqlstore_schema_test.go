package store

import (
	"context"
	"reflect"
	"testing"
	"time"

	gosched "github.com/hollis-labs/go-scheduler"
	"github.com/hollis-labs/go-scheduler/sqlstore"
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
			scheduleID := makeTestSchedule(t, host, agent.ID, "historical", at)
			creation := testFireCreation(scheduleID, "legacy-fire", at)
			creation.Fire.ID = "legacy-row-alias"
			if ok, createErr := host.CreateScheduleFire(ctx, creation); createErr != nil || !ok {
				t.Fatalf("history: %v %v", ok, createErr)
			}
			historical, historyErr := host.GetAgentSchedule(ctx, scheduleID)
			if historyErr != nil {
				t.Fatal(historyErr)
			}
			fire, fireErr := host.GetScheduleFire(ctx, "legacy-fire")
			if fireErr != nil {
				t.Fatal(fireErr)
			}
			assertHistory := func() {
				t.Helper()
				got, readErr := host.GetAgentSchedule(ctx, scheduleID)
				if readErr != nil {
					t.Fatal(readErr)
				}
				gotFire, readErr := host.GetScheduleFire(ctx, "legacy-fire")
				if readErr != nil {
					t.Fatal(readErr)
				}
				if !reflect.DeepEqual(got, historical) || !reflect.DeepEqual(gotFire, fire) {
					t.Fatalf("legacy rows changed: schedule=%+v fire=%+v", got, gotFire)
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
