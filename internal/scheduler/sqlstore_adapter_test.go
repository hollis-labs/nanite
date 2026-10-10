package scheduler

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	gosched "github.com/hollis-labs/libs/util/scheduler"
	"github.com/hollis-labs/libs/util/scheduler/conformance"
	"github.com/hollis-labs/libs/util/scheduler/sqlstore"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

// Only tests expose raw seeding and a DB handle for fixtures/fault injection.
// Production callers see only the sealed host facade.
type testSQLAdapter struct {
	*SQLStoreAdapter
	raw *sqlstore.Store
	db  *sql.DB
}

func (a *testSQLAdapter) CreateSchedule(ctx context.Context, sch gosched.Schedule) error {
	return a.raw.CreateSchedule(ctx, sch)
}
func (a *testSQLAdapter) DB() *sql.DB { return a.db }

var sqlstoreAt = time.Date(2026, 3, 4, 5, 6, 7, 123456789, time.UTC)

func newSQLAdapter(t *testing.T, path string, projections bool) (*testSQLAdapter, *store.Store) {
	t.Helper()
	host, err := storetest.New(t, context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if operationErr := host.Close(context.Background()); operationErr != nil {
			t.Error(operationErr)
		}
	})
	adapter, err := NewSQLStoreAdapter(host, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if projections {
		tx, stepErr := host.DB.BeginTx(context.Background(), nil)
		if stepErr != nil {
			t.Fatal(stepErr)
		}
		if checkedErr := store.InstallSchedulerProjections(context.Background(), tx); checkedErr != nil {
			_ = tx.Rollback()
			t.Fatal(checkedErr)
		}
		if checkedErr := tx.Commit(); checkedErr != nil {
			t.Fatal(checkedErr)
		}
	}
	raw, err := sqlstore.New(host.DB)
	if err != nil {
		t.Fatal(err)
	}
	return &testSQLAdapter{SQLStoreAdapter: adapter, raw: raw, db: host.DB}, host
}

func TestSQLStoreConformance(t *testing.T) {
	conformance.Run(t, func(t *testing.T) gosched.Store {
		a, _ := newSQLAdapter(t, filepath.Join(t.TempDir(), "bare.db"), false)
		return a.raw
	})
}
func TestSQLStoreAdapterConformance(t *testing.T) {
	conformance.Run(t, func(t *testing.T) gosched.Store {
		a, _ := newSQLAdapter(t, filepath.Join(t.TempDir(), "host.db"), true)
		return a
	})
}

func sqlstoreClaimed(t *testing.T, a *testSQLAdapter, id string) gosched.Fire {
	t.Helper()
	ctx := context.Background()
	sch := gosched.Schedule{ID: id, Enabled: true, NextRun: sqlstoreAt, JobType: JobTypeCommandRun, Payload: []byte(`{"command":"echo","agent_id":"test"}`)}
	if err := a.CreateSchedule(ctx, sch); err != nil {
		t.Fatal(err)
	}
	f := gosched.Fire{ID: gosched.DeriveFireID(id, sqlstoreAt), ScheduleID: id, ScheduledAt: sqlstoreAt, NextAttemptAt: sqlstoreAt, Status: gosched.FirePending, JobType: sch.JobType, Payload: sch.Payload}
	if ok, err := a.CreateFire(ctx, gosched.FireCreation{ScheduleID: id, ExpectedNext: sqlstoreAt, Fire: f}); err != nil || !ok {
		t.Fatalf("create: %v %v", ok, err)
	}
	f, ok, err := a.ClaimFire(ctx, gosched.FireClaim{FireID: f.ID, ExpectedStatus: gosched.FirePending, ClaimedAt: sqlstoreAt, ClaimExpiresAt: sqlstoreAt.Add(time.Minute)})
	if err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	return f
}

func recoverSQLFire(t *testing.T, a *testSQLAdapter, f gosched.Fire) gosched.Fire {
	t.Helper()
	got, ok, err := a.ClaimFire(context.Background(), gosched.FireClaim{FireID: f.ID, ExpectedStatus: gosched.FireClaimed, ExpectedAttempt: f.Attempt, ExpectedFiredAt: f.FiredAt, ClaimedAt: f.FiredAt.Add(time.Minute), ClaimExpiresAt: f.FiredAt.Add(2 * time.Minute)})
	if err != nil || !ok {
		t.Fatalf("recovery: %v %v", ok, err)
	}
	return got
}

func TestSQLStoreReceiptFences(t *testing.T) {
	for _, tc := range []string{"valid", "wrong-attempt", "stale-epoch", "terminal", "missing"} {
		t.Run(tc, func(t *testing.T) {
			a, _ := newSQLAdapter(t, filepath.Join(t.TempDir(), "receipt.db"), false)
			f := sqlstoreClaimed(t, a, "receipt")
			id, attempt, epoch := f.ID, f.Attempt, f.FiredAt
			want := false
			switch tc {
			case "valid":
				want = true
			case "wrong-attempt":
				attempt++
			case "stale-epoch":
				recoverSQLFire(t, a, f)
			case "terminal":
				ok, err := a.TransitionFire(context.Background(), gosched.FireTransition{FireID: id, Attempt: attempt, From: gosched.FireClaimed, ClaimedAt: epoch, To: gosched.FireSucceeded})
				if err != nil || !ok {
					t.Fatalf("terminal: %v %v", ok, err)
				}
			case "missing":
				id = "missing"
			}
			ok, err := a.MarkScheduleFireDispatchAccepted(context.Background(), id, attempt, epoch, sqlstoreAt)
			if err != nil || ok != want {
				t.Fatalf("accept: %v %v; want %v", ok, err, want)
			}
			exists, err := a.IsScheduleFireDispatchAccepted(context.Background(), id)
			if err != nil || exists != want {
				t.Fatalf("receipt: %v %v", exists, err)
			}
		})
	}
}

func TestSQLStoreReceiptAcceptedBeforeTerminalAndReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "restart.db")
	a, host := newSQLAdapter(t, path, false)
	f := sqlstoreClaimed(t, a, "restart")
	first := sqlstoreAt.Add(time.Second)
	for _, at := range []time.Time{first, first.Add(time.Second)} {
		if ok, err := a.MarkScheduleFireDispatchAccepted(ctx, f.ID, f.Attempt, f.FiredAt, at); err != nil || !ok {
			t.Fatalf("accept: %v %v", ok, err)
		}
	}
	var stored string
	if err := host.DB.QueryRowContext(ctx, `SELECT accepted_at FROM scheduler_dispatch_receipts WHERE fire_id=?`, f.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != first.Format(store.SchedulerTimeLayout) {
		t.Fatalf("receipt time changed: %s", stored)
	}
	if err := host.Close(ctx); err != nil {
		t.Fatal(err)
	}
	reopened, _ := newSQLAdapter(t, path, false)
	recovered := recoverSQLFire(t, reopened, f)
	runner := &RunnerAdapter{Dispatches: reopened}
	err := runner.Enqueue(ctx, gosched.Job{FireID: f.ID, Attempt: recovered.Attempt, FiredAt: recovered.FiredAt, JobType: JobTypeCommandRun, Payload: f.Payload})
	if !errors.Is(err, gosched.ErrDuplicateJob) {
		t.Fatalf("recovery dispatched accepted target: %v", err)
	}
	if ok, err := reopened.TransitionFire(ctx, gosched.FireTransition{FireID: f.ID, Attempt: recovered.Attempt, From: gosched.FireClaimed, ClaimedAt: recovered.FiredAt, To: gosched.FireSkipped}); err != nil || !ok {
		t.Fatalf("terminal: %v %v", ok, err)
	}
	if ok, err := reopened.MarkScheduleFireDispatchAccepted(ctx, f.ID, recovered.Attempt, recovered.FiredAt, sqlstoreAt); err != nil || ok {
		t.Fatalf("terminal receipt accepted: %v %v", ok, err)
	}
}

type cancelSQLCommand struct{ cancel context.CancelFunc }

func (c cancelSQLCommand) Execute(context.Context, string, string, map[string]any) (*service.ToolResult, error) {
	c.cancel()
	return &service.ToolResult{Output: "accepted"}, nil
}

func TestSQLStoreReceiptCancellation(t *testing.T) {
	a, _ := newSQLAdapter(t, filepath.Join(t.TempDir(), "cancel.db"), false)
	f := sqlstoreClaimed(t, a, "cancel")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if ok, err := a.MarkScheduleFireDispatchAccepted(ctx, f.ID, f.Attempt, f.FiredAt, sqlstoreAt); err == nil || ok {
		t.Fatalf("canceled acceptance: %v %v", ok, err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	r := &RunnerAdapter{Dispatches: a, Commands: cancelSQLCommand{cancel}}
	if err := r.Enqueue(ctx, gosched.Job{FireID: f.ID, Attempt: f.Attempt, FiredAt: f.FiredAt, JobType: JobTypeCommandRun, Payload: f.Payload}); err != nil {
		t.Fatal(err)
	}
	if ok, err := a.IsScheduleFireDispatchAccepted(context.Background(), f.ID); err != nil || !ok {
		t.Fatalf("WithoutCancel lost acceptance: %v %v", ok, err)
	}
}

func TestSQLStoreReceiptConcurrentRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "race.db")
	a, _ := newSQLAdapter(t, path, false)
	// Independent handles contend on SQLite's writer boundary, rather than
	// merely queueing both operations through one Go database connection.
	b, _ := newSQLAdapter(t, path, false)
	for i := 0; i < 12; i++ {
		f := sqlstoreClaimed(t, a, fmt.Sprintf("race-%d", i))
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		var accepted, recovered bool
		var acceptErr, recoverErr error
		go func() {
			defer wg.Done()
			<-start
			accepted, acceptErr = a.MarkScheduleFireDispatchAccepted(context.Background(), f.ID, f.Attempt, f.FiredAt, sqlstoreAt)
		}()
		go func() {
			defer wg.Done()
			<-start
			_, recovered, recoverErr = b.ClaimFire(context.Background(), gosched.FireClaim{FireID: f.ID, ExpectedStatus: gosched.FireClaimed, ExpectedAttempt: f.Attempt, ExpectedFiredAt: f.FiredAt, ClaimedAt: f.FiredAt.Add(time.Minute), ClaimExpiresAt: f.FiredAt.Add(2 * time.Minute)})
		}()
		close(start)
		wg.Wait()
		if acceptErr != nil || recoverErr != nil || !recovered {
			t.Fatalf("race: accept %v/%v recover %v/%v", accepted, acceptErr, recovered, recoverErr)
		}
		exists, err := b.IsScheduleFireDispatchAccepted(context.Background(), f.ID)
		if err != nil || exists != accepted {
			t.Fatalf("serialized outcome: receipt %v/%v accepted %v", exists, err, accepted)
		}
		if ok, err := a.MarkScheduleFireDispatchAccepted(context.Background(), f.ID, f.Attempt, f.FiredAt, sqlstoreAt); err != nil || ok {
			t.Fatalf("old owner accepted after recovery: %v %v", ok, err)
		}
	}
}

func sqlAgentSchedule(t *testing.T, host *store.Store, id string) store.AgentSchedule {
	t.Helper()
	agent := makeAdapterTestAgent(t, host, id)
	return store.AgentSchedule{ID: id, AgentID: agent.ID, Name: id, Body: "body", ScheduleKind: store.ScheduleKindCron, ScheduleSpec: "* * * * *", NextRun: sqlstoreAt.Format(time.RFC3339Nano), JobType: JobTypeCommandRun, JobPayload: `{ "command": "echo", "agent_id": "test", "opaque": "☃" }`, FiredCount: 7}
}
func sqlCreateHostFire(t *testing.T, a *testSQLAdapter, sch gosched.Schedule, next time.Time) gosched.Fire {
	t.Helper()
	f := gosched.Fire{ID: gosched.DeriveFireID(sch.ID, sch.NextRun), ScheduleID: sch.ID, ScheduledAt: sch.NextRun, NextAttemptAt: sch.NextRun, Status: gosched.FirePending, JobType: sch.JobType, Payload: sch.Payload, Retry: sch.Retry}
	ok, err := a.CreateFire(context.Background(), gosched.FireCreation{ScheduleID: sch.ID, ExpectedNext: sch.NextRun, NextRun: next, Fire: f})
	if err != nil || !ok {
		t.Fatalf("host fire: %v %v", ok, err)
	}
	return f
}

func TestSQLStoreHostMetadataTransactions(t *testing.T) {
	ctx := context.Background()
	a, host := newSQLAdapter(t, filepath.Join(t.TempDir(), "metadata.db"), true)
	row := sqlAgentSchedule(t, host, "metadata")
	if err := a.InsertAgentSchedule(ctx, row); err != nil {
		t.Fatal(err)
	}
	sch, _, err := a.GetSchedule(ctx, row.ID)
	if err != nil {
		t.Fatal(err)
	}
	f := sqlCreateHostFire(t, a, sch, sqlstoreAt.Add(time.Minute))
	got, err := a.GetAgentSchedule(ctx, row.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.FiredCount != 8 || got.NextRun != sqlstoreAt.Add(time.Minute).Format(time.RFC3339Nano) || got.LastFiredAt != sqlstoreAt.Format(time.RFC3339Nano) {
		t.Fatalf("projection: %+v", got)
	}
	var legacy string
	if operationErr := host.DB.QueryRowContext(ctx, `SELECT legacy_row_id FROM scheduler_fire_identity WHERE fire_id=?`, f.ID).Scan(&legacy); operationErr != nil || legacy != f.ID {
		t.Fatalf("new identity %s: %v", legacy, operationErr)
	}
	// Duplicate and stale materializations cannot change the counter or times.
	for _, expected := range []time.Time{sqlstoreAt, sqlstoreAt.Add(time.Minute)} {
		ok, operationErr := a.CreateFire(ctx, gosched.FireCreation{ScheduleID: sch.ID, ExpectedNext: expected, NextRun: sqlstoreAt.Add(2 * time.Minute), Fire: f})
		if operationErr != nil || ok {
			t.Fatalf("duplicate/stale create: %v %v", ok, operationErr)
		}
	}
	got, err = a.GetAgentSchedule(ctx, row.ID)
	if err != nil || got.FiredCount != 8 {
		t.Fatalf("duplicate count: %+v %v", got, err)
	}
	// Edit payload/policy while retaining the existing FK parent and fire snapshot.
	row = *got
	row.JobPayload = `{"command":"changed"}`
	row.MaxRetries = 9
	if operationErr := a.InsertAgentSchedule(ctx, row); operationErr != nil {
		t.Fatal(operationErr)
	}
	frozen, _, err := a.GetFire(ctx, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(frozen.Payload) != string(f.Payload) || frozen.Retry != f.Retry {
		t.Fatalf("edit changed fire snapshot: %+v", frozen)
	}
	if operationErr := a.DeleteAgentSchedule(ctx, row.ID); operationErr == nil {
		t.Fatal("deleted schedule with shared history")
	}
	if operationErr := a.UpdateAgentScheduleStatus(ctx, row.ID, store.ScheduleStatusPaused); operationErr != nil {
		t.Fatal(operationErr)
	}
	got, err = a.GetAgentSchedule(ctx, row.ID)
	if err != nil || got.Status != store.ScheduleStatusPaused {
		t.Fatalf("pause: %+v %v", got, err)
	}
	if operationErr := a.UpdateAgentScheduleStatus(ctx, row.ID, store.ScheduleStatusActive); operationErr != nil {
		t.Fatal(operationErr)
	}
	if operationErr := a.DisableSchedule(ctx, row.ID); operationErr != nil {
		t.Fatal(operationErr)
	}
	got, err = a.GetAgentSchedule(ctx, row.ID)
	if err != nil || got.Status != store.ScheduleStatusExpired {
		t.Fatalf("disable: %+v %v", got, err)
	}
	// FK failure after shared writes rolls back every part of a producer transaction.
	bad := row
	bad.ID = "bad"
	bad.AgentID = "missing-profile"
	if err := a.InsertAgentSchedule(ctx, bad); err == nil {
		t.Fatal("missing profile accepted")
	}
	if _, found, err := a.GetSchedule(ctx, bad.ID); err != nil || found {
		t.Fatalf("orphan lifecycle after rollback: %v %v", found, err)
	}
	// Invalid timestamp and payload fail before mutating existing state.
	bad = row
	bad.NextRun = "not-time"
	if err := a.InsertAgentSchedule(ctx, bad); err == nil {
		t.Fatal("malformed time accepted")
	}
	bad = row
	bad.JobPayload = "{"
	if err := a.InsertAgentSchedule(ctx, bad); err == nil {
		t.Fatal("malformed payload accepted")
	}
}

func TestSQLStoreProjectionFailureRollsBackFire(t *testing.T) {
	ctx := context.Background()
	a, host := newSQLAdapter(t, filepath.Join(t.TempDir(), "fail.db"), true)
	row := sqlAgentSchedule(t, host, "fail")
	if err := a.InsertAgentSchedule(ctx, row); err != nil {
		t.Fatal(err)
	}
	if _, err := host.DB.ExecContext(ctx, `CREATE TRIGGER fail_counter BEFORE UPDATE OF fired_count ON actor_schedules BEGIN SELECT RAISE(ABORT,'injected projection failure'); END`); err != nil {
		t.Fatal(err)
	}
	beforeMetadata, metadataErr := a.GetAgentSchedule(ctx, row.ID)
	if metadataErr != nil {
		t.Fatal(metadataErr)
	}
	sch, _, err := a.GetSchedule(ctx, row.ID)
	if err != nil {
		t.Fatal(err)
	}
	f := gosched.Fire{ID: "failed-fire", ScheduleID: row.ID, ScheduledAt: sch.NextRun, Status: gosched.FirePending}
	if ok, operationErr := a.CreateFire(ctx, gosched.FireCreation{ScheduleID: row.ID, ExpectedNext: sch.NextRun, NextRun: sch.NextRun.Add(time.Minute), Fire: f}); operationErr == nil || ok || !strings.Contains(operationErr.Error(), "injected projection failure") {
		t.Fatalf("projection error: %v %v", ok, operationErr)
	}
	if _, found, operationErr := a.GetFire(ctx, f.ID); operationErr != nil || found {
		t.Fatalf("partial fire committed: %v %v", found, operationErr)
	}
	persisted, _, err := a.GetSchedule(ctx, row.ID)
	if err != nil || !reflect.DeepEqual(persisted, sch) {
		t.Fatalf("shared lifecycle changed after rollback: %+v %v", persisted, err)
	}
	afterMetadata, metadataErr := a.GetAgentSchedule(ctx, row.ID)
	if metadataErr != nil || *afterMetadata != *beforeMetadata {
		t.Fatalf("projection metadata changed after rollback: %+v %v", afterMetadata, metadataErr)
	}
	var identityRows int
	if identityErr := host.DB.QueryRowContext(ctx, `SELECT count(*) FROM scheduler_fire_identity WHERE fire_id=?`, f.ID).Scan(&identityRows); identityErr != nil || identityRows != 0 {
		t.Fatalf("failed projection retained fire identity: %d %v", identityRows, identityErr)
	}
}

func TestSQLStoreWorkflowProducerAndCancel(t *testing.T) {
	ctx := context.Background()
	a, host := newSQLAdapter(t, filepath.Join(t.TempDir(), "workflow.db"), true)
	row := store.WorkflowActivationSchedule{ScheduleID: store.WorkflowActivationScheduleID("activation"), ActivationID: "activation", ActivationJSON: `{ "kind":"timer", "opaque":"☃" }`, FireAt: sqlstoreAt.Format(time.RFC3339Nano), NextRun: sqlstoreAt.Format(time.RFC3339Nano)}
	if err := a.ScheduleWorkflowActivation(ctx, row); err != nil {
		t.Fatal(err)
	}
	sch, _, err := a.GetSchedule(ctx, row.ScheduleID)
	if err != nil {
		t.Fatal(err)
	}
	f := sqlCreateHostFire(t, a, sch, time.Time{})
	if operationErr := a.DisableSchedule(ctx, row.ScheduleID); operationErr != nil {
		t.Fatal(operationErr)
	}
	meta, err := host.GetWorkflowActivationSchedule(ctx, row.ScheduleID)
	if err != nil || meta.Status != "materialized" {
		t.Fatalf("materialization: %+v %v", meta, err)
	}
	if operationErr := a.ScheduleWorkflowActivation(ctx, row); operationErr != nil {
		t.Fatal(operationErr)
	}
	shared, _, err := a.GetSchedule(ctx, row.ScheduleID)
	if err != nil || shared.Enabled || !shared.NextRun.IsZero() {
		t.Fatalf("replay reenabled activation: %+v %v", shared, err)
	}
	conflict := row
	conflict.ActivationJSON = `{"kind":"changed"}`
	if operationErr := a.ScheduleWorkflowActivation(ctx, conflict); operationErr == nil {
		t.Fatal("conflicting replay accepted")
	}
	// A claimed fire remains recoverable; an unclaimed sibling is closed.
	claimed, ok, err := a.ClaimFire(ctx, gosched.FireClaim{FireID: f.ID, ExpectedStatus: gosched.FirePending, ClaimedAt: sqlstoreAt, ClaimExpiresAt: sqlstoreAt.Add(time.Minute)})
	if err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	pending := row
	pending.ActivationID = "pending"
	pending.ScheduleID = store.WorkflowActivationScheduleID(pending.ActivationID)
	if operationErr := a.ScheduleWorkflowActivation(ctx, pending); operationErr != nil {
		t.Fatal(operationErr)
	}
	pendingSch, _, err := a.GetSchedule(ctx, pending.ScheduleID)
	if err != nil {
		t.Fatal(err)
	}
	unclaimed := sqlCreateHostFire(t, a, pendingSch, time.Time{})
	if operationErr := a.CancelWorkflowActivation(ctx, pending.ActivationID, sqlstoreAt.Add(time.Second)); operationErr != nil {
		t.Fatal(operationErr)
	}
	canceled, _, err := a.GetFire(ctx, unclaimed.ID)
	if err != nil || canceled.Status != gosched.FireSkipped {
		t.Fatalf("unclaimed cancel: %+v %v", canceled, err)
	}
	if operationErr := a.CancelWorkflowActivation(ctx, row.ActivationID, sqlstoreAt.Add(time.Second)); operationErr != nil {
		t.Fatal(operationErr)
	}
	meta, err = host.GetWorkflowActivationSchedule(ctx, row.ScheduleID)
	if err != nil || meta.Status != "canceled" {
		t.Fatalf("cancel metadata: %+v %v", meta, err)
	}
	recoverSQLFire(t, a, claimed)
	if operationErr := a.ScheduleWorkflowActivation(ctx, row); operationErr != nil {
		t.Fatal(operationErr)
	}
	shared, _, err = a.GetSchedule(ctx, row.ScheduleID)
	if err != nil || shared.Enabled {
		t.Fatalf("canceled replay reenabled: %+v %v", shared, err)
	}
	// Explicit map rejects cross-family collision and rolls back the overwritten
	// neutral row; prefix shape alone is never an ownership assertion.
	agentRow := sqlAgentSchedule(t, host, "collision-agent")
	agentRow.ID = row.ScheduleID
	if operationErr := a.InsertAgentSchedule(ctx, agentRow); operationErr == nil {
		t.Fatal("cross-family collision accepted")
	}
	shared, _, err = a.GetSchedule(ctx, row.ScheduleID)
	if err != nil || shared.JobType != JobTypeWorkflowActivation {
		t.Fatalf("collision mutated shared state: %+v %v", shared, err)
	}
}

func TestSQLStoreDueFireOrdering(t *testing.T) {
	ctx := context.Background()
	a, _ := newSQLAdapter(t, filepath.Join(t.TempDir(), "order.db"), false)
	// The older scheduled occurrence wins even though its lease expired later.
	older := sqlstoreClaimed(t, a, "older")
	newer := sqlstoreClaimed(t, a, "newer")
	if _, err := a.DB().ExecContext(ctx, `UPDATE gosched_fires SET scheduled_at=?,claim_expires_at=? WHERE id=?`, sqlstoreAt.Add(-time.Hour).Format(store.SchedulerTimeLayout), sqlstoreAt.Add(time.Minute).Format(store.SchedulerTimeLayout), older.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB().ExecContext(ctx, `UPDATE gosched_fires SET claim_expires_at=? WHERE id=?`, sqlstoreAt.Add(time.Second).Format(store.SchedulerTimeLayout), newer.ID); err != nil {
		t.Fatal(err)
	}
	due, err := a.ListDueFires(ctx, sqlstoreAt.Add(2*time.Minute), 1)
	if err != nil || len(due) != 1 || due[0].ID != older.ID {
		t.Fatalf("host fairness order: %+v %v", due, err)
	}
}

func TestSQLStoreDynamicWakeAndMixedDueSchedules(t *testing.T) {
	ctx := context.Background()
	a, host := newSQLAdapter(t, filepath.Join(t.TempDir(), "dynamic.db"), true)
	agent := makeAdapterTestAgent(t, host, "wake")
	row := store.AgentSchedule{ID: "a-wake", AgentID: agent.ID, Name: "wake", Body: "original prompt", ScheduleKind: store.ScheduleKindOneShot, NextRun: sqlstoreAt.Format(time.RFC3339Nano)}
	// Creation does not freeze or even require an instance; resolution is due-time.
	if err := a.InsertAgentSchedule(ctx, row); err != nil {
		t.Fatal(err)
	}
	activation := store.WorkflowActivationSchedule{ScheduleID: store.WorkflowActivationScheduleID("due"), ActivationID: "due", ActivationJSON: `{"kind":"timer"}`, FireAt: row.NextRun, NextRun: row.NextRun}
	if err := a.ScheduleWorkflowActivation(ctx, activation); err != nil {
		t.Fatal(err)
	}
	due, err := a.ListDueSchedules(ctx, sqlstoreAt, 1)
	if err != nil || len(due) != 1 || due[0].ID != activation.ScheduleID {
		t.Fatalf("invalid wake starved workflow: %+v %v", due, err)
	}
	first := makeAdapterTestInstance(t, host, agent.ID, "first")
	due, err = a.ListDueSchedules(ctx, sqlstoreAt, 1)
	if err != nil || len(due) != 1 || due[0].ID != row.ID {
		t.Fatalf("mixed due order: %+v %v", due, err)
	}
	if !strings.Contains(string(due[0].Payload), first.ID) {
		t.Fatalf("wake did not resolve current instance: %s", due[0].Payload)
	}
	if _, operationErr := host.ArchiveDurableAgentInstance(ctx, first.ID); operationErr != nil {
		t.Fatal(operationErr)
	}
	due, err = a.ListDueSchedules(ctx, sqlstoreAt, 1)
	if err != nil || len(due) != 1 || due[0].ID != activation.ScheduleID {
		t.Fatalf("archived wake not skipped: %+v %v", due, err)
	}
	second := makeAdapterTestInstance(t, host, agent.ID, "second")
	due, err = a.ListDueSchedules(ctx, sqlstoreAt, 1)
	if err != nil || len(due) != 1 || !strings.Contains(string(due[0].Payload), second.ID) {
		t.Fatalf("replacement wake not resolved: %+v %v", due, err)
	}
	makeAdapterTestInstance(t, host, agent.ID, "ambiguous")
	due, err = a.ListDueSchedules(ctx, sqlstoreAt, 1)
	if err != nil || len(due) != 1 || due[0].ID != activation.ScheduleID {
		t.Fatalf("ambiguous wake not skipped: %+v %v", due, err)
	}
}

func TestSQLStoreConditionalProducerConcurrent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "builtin.db")
	a, host := newSQLAdapter(t, path, true)
	b, _ := newSQLAdapter(t, path, false)
	row := sqlAgentSchedule(t, host, "builtin")
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	var inserted [2]bool
	var errs [2]error
	for i, adapter := range []*testSQLAdapter{a, b} {
		go func(i int, adapter *testSQLAdapter) {
			defer wg.Done()
			<-start
			candidate := row
			candidate.ID = fmt.Sprintf("builtin-%d", i)
			inserted[i], errs[i] = adapter.InsertAgentScheduleIfNameMissing(ctx, candidate)
		}(i, adapter)
	}
	close(start)
	wg.Wait()
	if errs[0] != nil || errs[1] != nil || inserted[0] == inserted[1] {
		t.Fatalf("conditional producer winners: %v errors %v", inserted, errs)
	}
	rows, err := host.ListAgentSchedules(ctx, row.AgentID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("host material: %+v %v", rows, err)
	}
	shared, found, err := a.GetSchedule(ctx, rows[0].ID)
	if err != nil || !found || string(shared.Payload) != row.JobPayload {
		t.Fatalf("atomic lifecycle material: %+v %v/%v", shared, found, err)
	}
	row.ID = "free"
	row.Name = "free"
	if err := a.InsertAgentSchedule(ctx, row); err != nil {
		t.Fatal(err)
	}
	if err := a.DeleteAgentSchedule(ctx, row.ID); err != nil {
		t.Fatal(err)
	}
	if _, found, err := a.GetSchedule(ctx, row.ID); err != nil || found {
		t.Fatalf("delete left lifecycle: %v %v", found, err)
	}
}

func TestSQLStoreMissingMetadataDoesNotBlockDispatchOrRecovery(t *testing.T) {
	ctx := context.Background()
	a, host := newSQLAdapter(t, filepath.Join(t.TempDir(), "orphan.db"), true)
	var logs bytes.Buffer
	a.legacy.Logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	orphan := sqlAgentSchedule(t, host, "orphan")
	if err := a.InsertAgentSchedule(ctx, orphan); err != nil {
		t.Fatal(err)
	}
	// Simulate optional metadata loss in the private fresh partition. The
	// binding stays authorized; deleting a legacy profile is neither a fixture
	// for this loss nor a supported production operation.
	if _, err := host.DB.ExecContext(ctx, `DELETE FROM actor_schedules WHERE id=?`, orphan.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := host.GetAgentForActor(ctx, orphan.AgentID); err != nil {
		t.Fatalf("optional metadata loss removed prior authority: %v", err)
	}
	valid := sqlAgentSchedule(t, host, "valid")
	if err := a.InsertAgentSchedule(ctx, valid); err != nil {
		t.Fatal(err)
	}
	recovered := sqlstoreClaimed(t, a, "recover")
	runner := &sequenceRunner{}
	engine := gosched.New(a.SQLStoreAdapter, runner, gosched.WithClock(&schedulerTestClock{now: sqlstoreAt.Add(2 * time.Minute)}))
	if err := engine.TickNow(ctx); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, job := range runner.snapshot() {
		seen[job.ScheduleID] = true
	}
	if !seen[valid.ID] || !seen[recovered.ScheduleID] || seen[orphan.ID] {
		t.Fatalf("orphan blocked tick: %+v", runner.snapshot())
	}
	for i := 0; i < 2; i++ {
		if _, err := a.ListDueSchedules(ctx, sqlstoreAt.Add(2*time.Minute), 100); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Count(logs.String(), "level=WARN") != 1 || !strings.Contains(logs.String(), "schedule_id=orphan") {
		t.Fatalf("orphan warning not deduplicated: %s", logs.String())
	}
}

func TestSQLStoreManualFireCount(t *testing.T) {
	ctx := context.Background()
	a, host := newSQLAdapter(t, filepath.Join(t.TempDir(), "manual.db"), true)
	row := sqlAgentSchedule(t, host, "manual")
	if err := a.InsertAgentSchedule(ctx, row); err != nil {
		t.Fatal(err)
	}
	manualAt := sqlstoreAt.Add(time.Second)
	if err := a.BumpAgentScheduleFireCount(ctx, row.ID, manualAt); err != nil {
		t.Fatal(err)
	}
	got, err := a.GetAgentSchedule(ctx, row.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.FiredCount != 8 || got.LastFiredAt != manualAt.Format(time.RFC3339Nano) {
		t.Fatalf("manual count: %+v", got)
	}
	fires, err := a.ListDueFires(ctx, manualAt, 100)
	if err != nil || len(fires) != 0 {
		t.Fatalf("manual bump materialized fire: %+v %v", fires, err)
	}
	sch, _, err := a.GetSchedule(ctx, row.ID)
	if err != nil {
		t.Fatal(err)
	}
	sqlCreateHostFire(t, a, sch, sqlstoreAt.Add(time.Minute))
	got, err = a.GetAgentSchedule(ctx, row.ID)
	if err != nil || got.FiredCount != 9 {
		t.Fatalf("projection double-counted manual bump: %+v %v", got, err)
	}
	before := *got
	sharedBeforeFailure, found, readErr := a.GetSchedule(ctx, row.ID)
	if readErr != nil || !found {
		t.Fatalf("manual counter lifecycle before injection: %+v %v %v", sharedBeforeFailure, found, readErr)
	}
	if _, stepErr := host.DB.ExecContext(ctx, `CREATE TRIGGER fail_manual_count BEFORE UPDATE OF fired_count ON actor_schedules BEGIN SELECT RAISE(ABORT,'injected counter failure'); END`); stepErr != nil {
		t.Fatal(stepErr)
	}
	if stepErr := a.BumpAgentScheduleFireCount(ctx, row.ID, manualAt.Add(time.Second)); stepErr == nil || !strings.Contains(stepErr.Error(), "injected counter failure") {
		t.Fatalf("injected counter failure not propagated: %v", stepErr)
	}
	after, err := a.GetAgentSchedule(ctx, row.ID)
	if err != nil || *after != before {
		t.Fatalf("failed bump changed state: %+v %v", after, err)
	}
	sharedAfterFailure, found, readErr := a.GetSchedule(ctx, row.ID)
	if readErr != nil || !found || !reflect.DeepEqual(sharedBeforeFailure, sharedAfterFailure) {
		t.Fatalf("counter failure changed shared lifecycle: %+v %v %v", sharedAfterFailure, found, readErr)
	}
	if _, stepErr := host.DB.ExecContext(ctx, `DROP TRIGGER fail_manual_count`); stepErr != nil {
		t.Fatal(stepErr)
	}
	if _, stepErr := host.DB.ExecContext(ctx, `DELETE FROM actor_schedules WHERE id=?`, row.ID); stepErr != nil {
		t.Fatal(stepErr)
	}
	sharedBefore, _, err := a.GetSchedule(ctx, row.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stepErr := a.BumpAgentScheduleFireCount(ctx, row.ID, manualAt.Add(time.Second)); !errors.Is(stepErr, store.ErrAgentScheduleNotFound) {
		t.Fatalf("missing metadata bump: %v", stepErr)
	}
	sharedAfter, _, err := a.GetSchedule(ctx, row.ID)
	if err != nil || !reflect.DeepEqual(sharedAfter, sharedBefore) {
		t.Fatalf("orphan bump committed lifecycle: %+v %v", sharedAfter, err)
	}
}

func TestSQLStoreWorkflowIntoAgentCollision(t *testing.T) {
	ctx := context.Background()
	a, host := newSQLAdapter(t, filepath.Join(t.TempDir(), "collision.db"), true)
	row := sqlAgentSchedule(t, host, "collision")
	row.ID = store.WorkflowActivationScheduleID("collision")
	if err := a.InsertAgentSchedule(ctx, row); err != nil {
		t.Fatal(err)
	}
	before, _, err := a.GetSchedule(ctx, row.ID)
	if err != nil {
		t.Fatal(err)
	}
	activation := store.WorkflowActivationSchedule{ScheduleID: row.ID, ActivationID: "collision", ActivationJSON: `{"kind":"timer"}`, FireAt: row.NextRun, NextRun: row.NextRun}
	if stepErr := a.ScheduleWorkflowActivation(ctx, activation); stepErr == nil {
		t.Fatal("workflow took an agent identity")
	}
	after, _, err := a.GetSchedule(ctx, row.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("collision changed agent lifecycle: %+v %v", after, err)
	}
	if _, stepErr := host.GetWorkflowActivationSchedule(ctx, row.ID); !errors.Is(stepErr, store.ErrWorkflowActivationScheduleNotFound) {
		t.Fatalf("collision committed workflow metadata: %v", stepErr)
	}
	family, err := a.ScheduleFamily(ctx, row.ID)
	if err != nil || family != "agent" {
		t.Fatalf("collision retargeted family: %s %v", family, err)
	}
}

func TestSQLStoreDueFiresNonPositiveLimit(t *testing.T) {
	a, _ := newSQLAdapter(t, filepath.Join(t.TempDir(), "limit.db"), false)
	sqlstoreClaimed(t, a, "due")
	for _, limit := range []int{0, -1} {
		due, err := a.ListDueFires(context.Background(), sqlstoreAt.Add(2*time.Minute), limit)
		if err != nil || len(due) != 1 {
			t.Fatalf("limit %d returned %+v %v", limit, due, err)
		}
	}
}

func TestSQLStoreFacadeSealsRawWriters(t *testing.T) {
	a, _ := newSQLAdapter(t, filepath.Join(t.TempDir(), "sealed.db"), false)
	for _, surface := range []any{a.SQLStoreAdapter, a.SchedulerSQLStore} {
		if _, ok := surface.(interface{ DB() *sql.DB }); ok {
			t.Fatalf("%T exposes raw DB", surface)
		}
		if _, ok := surface.(interface {
			CreateSchedule(context.Context, gosched.Schedule) error
		}); ok {
			t.Fatalf("%T bypasses producer identities", surface)
		}
		if _, ok := surface.(interface {
			DeleteSchedule(context.Context, string) error
		}); ok {
			t.Fatalf("%T bypasses history deletion restrictions", surface)
		}
	}
}
