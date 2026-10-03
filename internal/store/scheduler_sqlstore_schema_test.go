package store

import (
	"context"
	"io/fs"
	"path/filepath"
	"testing"
	"time"

	gosched "github.com/hollis-labs/go-scheduler"
	"github.com/pressly/goose/v3"
)

// The additive schema is removable only while it carries no material. Once
// staging data or an authority marker exists, Down must preserve it intact.
func TestSchedulerSQLSchemaRollbackGuard(t *testing.T) {
	for _, material := range []string{"empty", "schedule", "fire", "authority"} {
		t.Run(material, func(t *testing.T) {
			ctx := context.Background()
			host, err := New(ctx, filepath.Join(t.TempDir(), "schema.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if operationErr := host.Close(ctx); operationErr != nil {
					t.Error(operationErr)
				}
			}()
			shared, err := NewSchedulerSQLStore(host)
			if err != nil {
				t.Fatal(err)
			}
			at := time.Date(2026, 3, 4, 5, 6, 7, 123456789, time.UTC)
			switch material {
			case "schedule":
				if operationErr := shared.CreateSchedule(ctx, gosched.Schedule{ID: "retained", NextRun: at, Enabled: true}); operationErr != nil {
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
			migrations, err := fs.Sub(migrationsFS, "migrations")
			if err != nil {
				t.Fatal(err)
			}
			provider, err := goose.NewProvider(goose.DialectSQLite3, host.DB, migrations)
			if err != nil {
				t.Fatal(err)
			}
			_, err = provider.Down(ctx)
			if material == "empty" {
				if err != nil {
					t.Fatal(err)
				}
				if _, operationErr := shared.ListSchedules(ctx); operationErr == nil {
					t.Fatal("empty Down left lifecycle schema installed")
				}
			} else {
				if err == nil {
					t.Fatal("destructive Down accepted durable material")
				}
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
