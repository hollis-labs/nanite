package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/agent/reflexes"
	"github.com/hollis-labs/nanite/internal/store"
)

// --- Pure translation tests (no DB) ----------------------------------------

func TestBuildReflexAgentSchedule_CronSpec_ComputesNextRun(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	spec := map[string]interface{}{
		"name":          "daily-report",
		"schedule_kind": "cron",
		"schedule_spec": "0 9 * * *",
		"body":          "run the daily report",
	}
	row, err := buildReflexAgentSchedule("agent-1", spec, now)
	if err != nil {
		t.Fatalf("buildReflexAgentSchedule: %v", err)
	}
	if row.AgentID != "agent-1" {
		t.Errorf("AgentID = %q, want agent-1", row.AgentID)
	}
	if row.Name != "daily-report" {
		t.Errorf("Name = %q, want daily-report", row.Name)
	}
	if row.ScheduleKind != store.ScheduleKindCron {
		t.Errorf("ScheduleKind = %q, want cron", row.ScheduleKind)
	}
	if row.Body != "run the daily report" {
		t.Errorf("Body = %q", row.Body)
	}
	if row.JobType != "" {
		t.Errorf("JobType = %q, want empty (defer to InsertAgentSchedule's default)", row.JobType)
	}
	wantNext := store.ComputeAgentScheduleNextRun(store.ScheduleKindCron, "0 9 * * *", now).Format(time.RFC3339)
	if row.NextRun != wantNext {
		t.Errorf("NextRun = %q, want %q (via store.ComputeAgentScheduleNextRun)", row.NextRun, wantNext)
	}
	if row.ID == "" {
		t.Error("ID must be non-empty")
	}
	if row.CreatedBy != reflexScheduleSource {
		t.Errorf("CreatedBy = %q, want %q", row.CreatedBy, reflexScheduleSource)
	}
}

func TestBuildReflexAgentSchedule_OneShot_NextRunIsNow(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	spec := map[string]interface{}{
		"name":          "one-off-nudge",
		"schedule_kind": "one_shot",
		"body":          "nudge once",
	}
	row, err := buildReflexAgentSchedule("agent-1", spec, now)
	if err != nil {
		t.Fatalf("buildReflexAgentSchedule: %v", err)
	}
	if row.NextRun != now.Format(time.RFC3339) {
		t.Errorf("NextRun = %q, want %q", row.NextRun, now.Format(time.RFC3339))
	}
}

func TestBuildReflexAgentSchedule_Defaults(t *testing.T) {
	now := time.Now()
	spec := map[string]interface{}{
		"name":          "defaults-test",
		"schedule_kind": "one_shot",
		"body":          "body",
	}
	row, err := buildReflexAgentSchedule("agent-1", spec, now)
	if err != nil {
		t.Fatalf("buildReflexAgentSchedule: %v", err)
	}
	if row.MaxRetries != 0 {
		t.Errorf("MaxRetries = %d, want 0 (left unset so InsertAgentSchedule's own default of 3 applies)", row.MaxRetries)
	}
	if row.OnFail != "" {
		t.Errorf("OnFail = %q, want empty (left unset so InsertAgentSchedule's own 'retry' default applies)", row.OnFail)
	}
	if row.JobType != "" {
		t.Errorf("JobType = %q, want empty (left unset so InsertAgentSchedule's own durable_agent_wake default applies)", row.JobType)
	}
	if row.JobPayload != "" {
		t.Errorf("JobPayload = %q, want empty (left unset so InsertAgentSchedule's own '{}' default applies)", row.JobPayload)
	}
}

func TestBuildReflexAgentSchedule_ExplicitOverrides(t *testing.T) {
	now := time.Now()
	spec := map[string]interface{}{
		"name":          "overrides-test",
		"schedule_kind": "one_shot",
		"body":          "body",
		"job_type":      store.ScheduleJobTypeCommandRun,
		"job_payload":   map[string]interface{}{"agent_id": "agent-1", "command": "noop_tool"},
		"max_retries":   float64(5), // JSON numbers decode as float64
		"on_fail":       store.ScheduleOnFailDisable,
		"priority":      float64(7),
		"session_id":    "sess-1",
		"expires_at":    "2026-09-01T00:00:00Z",
	}
	row, err := buildReflexAgentSchedule("agent-1", spec, now)
	if err != nil {
		t.Fatalf("buildReflexAgentSchedule: %v", err)
	}
	if row.JobType != store.ScheduleJobTypeCommandRun {
		t.Errorf("JobType = %q", row.JobType)
	}
	if row.JobPayload != `{"agent_id":"agent-1","command":"noop_tool"}` {
		t.Errorf("JobPayload = %q", row.JobPayload)
	}
	if row.MaxRetries != 5 {
		t.Errorf("MaxRetries = %d, want 5", row.MaxRetries)
	}
	if row.OnFail != store.ScheduleOnFailDisable {
		t.Errorf("OnFail = %q, want disable", row.OnFail)
	}
	if row.Priority != 7 {
		t.Errorf("Priority = %d, want 7", row.Priority)
	}
	if row.SessionID != "sess-1" {
		t.Errorf("SessionID = %q, want sess-1", row.SessionID)
	}
	if row.ExpiresAt != "2026-09-01T00:00:00Z" {
		t.Errorf("ExpiresAt = %q", row.ExpiresAt)
	}
}

func TestBuildReflexAgentSchedule_JobPayloadAsPreEncodedString(t *testing.T) {
	now := time.Now()
	spec := map[string]interface{}{
		"name":          "payload-string-test",
		"schedule_kind": "one_shot",
		"body":          "body",
		"job_payload":   `{"reflex_id":"r1"}`,
	}
	row, err := buildReflexAgentSchedule("agent-1", spec, now)
	if err != nil {
		t.Fatalf("buildReflexAgentSchedule: %v", err)
	}
	if row.JobPayload != `{"reflex_id":"r1"}` {
		t.Errorf("JobPayload = %q", row.JobPayload)
	}
}

// TestBuildReflexAgentSchedule_MalformedSpecs is the Done-means regression
// proving a malformed/incomplete action_spec fails cleanly -- a returned
// error, never a partially-built row.
func TestBuildReflexAgentSchedule_MalformedSpecs(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		spec map[string]interface{}
	}{
		{
			name: "missing name",
			spec: map[string]interface{}{
				"schedule_kind": "one_shot",
				"body":          "body",
			},
		},
		{
			name: "missing schedule_kind",
			spec: map[string]interface{}{
				"name": "n",
				"body": "body",
			},
		},
		{
			name: "invalid schedule_kind (retired every_n_ticks value)",
			spec: map[string]interface{}{
				"name":          "n",
				"schedule_kind": "every_n_ticks",
				"body":          "body",
			},
		},
		{
			name: "cron kind missing schedule_spec",
			spec: map[string]interface{}{
				"name":          "n",
				"schedule_kind": "cron",
				"body":          "body",
			},
		},
		{
			// Regression for TASKS/scheduling/07-wire-add-schedule-reflex.md's
			// Review notes / TASKS/ESCALATIONS.md's 2026-08-20
			// "Scheduling Phase 2 review: task 07's malformed-cron gap"
			// finding: a syntactically invalid but non-empty schedule_spec
			// used to fall through to store.ComputeAgentScheduleNextRun's
			// "due now" fallback and insert a schema-valid row that
			// go-scheduler's own tick() would then skip forever (parse
			// error before the CAS claim, never firing again). Must be
			// rejected here, before next_run is ever computed.
			name: "cron kind with malformed (non-empty) schedule_spec",
			spec: map[string]interface{}{
				"name":          "n",
				"schedule_kind": "cron",
				"schedule_spec": "not a cron expr",
				"body":          "body",
			},
		},
		{
			name: "missing body",
			spec: map[string]interface{}{
				"name":          "n",
				"schedule_kind": "one_shot",
			},
		},
		{
			name: "invalid job_type",
			spec: map[string]interface{}{
				"name":          "n",
				"schedule_kind": "one_shot",
				"body":          "body",
				"job_type":      "not_a_real_job_type",
			},
		},
		{
			name: "invalid on_fail",
			spec: map[string]interface{}{
				"name":          "n",
				"schedule_kind": "one_shot",
				"body":          "body",
				"on_fail":       "ignore",
			},
		},
		{
			name: "invalid job_payload JSON string",
			spec: map[string]interface{}{
				"name":          "n",
				"schedule_kind": "one_shot",
				"body":          "body",
				"job_payload":   "{not valid json",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := buildReflexAgentSchedule("agent-1", tc.spec, now); err == nil {
				t.Fatalf("buildReflexAgentSchedule(%v) = nil error, want an error", tc.spec)
			}
		})
	}
}

// --- Integration tests: real *store.Store -----------------------------------

func newReflexScheduleTestStore(t *testing.T, agentID string) *store.Store {
	t.Helper()
	st := newTestStore(t)
	if err := st.CreateAgent(context.Background(), &store.AgentProfile{
		ID:           agentID,
		Name:         "Reflex Schedule Test Agent",
		Slug:         agentID,
		Class:        "advisor",
		SystemPrompt: "test",
		Source:       "test",
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	return st
}

// TestNewReflexScheduleHook_InsertsDiscoverableRow is the Done-means
// regression: a reflex firing with action_kind='add_schedule' and a
// well-formed action_spec produces a real agent_schedules row with a
// correctly-computed next_run, discoverable by ListDueAgentSchedules (the
// low-level query 02's scheduler.StoreAdapter.ListDueSchedules is built on
// top of) on the next tick.
func TestNewReflexScheduleHook_InsertsDiscoverableRow(t *testing.T) {
	ctx := context.Background()
	agentID := "reflex-schedule-agent"
	st := newReflexScheduleTestStore(t, agentID)

	hook := NewReflexScheduleHook(st)
	spec := map[string]interface{}{
		"name":          "reflex-authored-nudge",
		"schedule_kind": "one_shot",
		"body":          "wake up and check the queue",
	}
	if err := hook(ctx, agentID, spec); err != nil {
		t.Fatalf("hook: %v", err)
	}

	rows, err := st.ListAgentSchedules(ctx, agentID)
	if err != nil {
		t.Fatalf("ListAgentSchedules: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("ListAgentSchedules = %d rows, want 1", len(rows))
	}
	row := rows[0]
	if row.NextRun == "" {
		t.Fatal("NextRun is empty -- row would never fire under the CAS-claim engine")
	}
	if row.CreatedBy != reflexScheduleSource {
		t.Errorf("CreatedBy = %q, want %q", row.CreatedBy, reflexScheduleSource)
	}
	if row.MaxRetries != 3 {
		t.Errorf("MaxRetries = %d, want InsertAgentSchedule's default 3", row.MaxRetries)
	}
	if row.OnFail != store.ScheduleOnFailRetry {
		t.Errorf("OnFail = %q, want InsertAgentSchedule's default %q", row.OnFail, store.ScheduleOnFailRetry)
	}
	if row.JobType != store.ScheduleJobTypeDurableAgentWake {
		t.Errorf("JobType = %q, want InsertAgentSchedule's default %q", row.JobType, store.ScheduleJobTypeDurableAgentWake)
	}

	// Discoverable by ListDueAgentSchedules once next_run has elapsed.
	nextRun, perr := time.Parse(time.RFC3339, row.NextRun)
	if perr != nil {
		t.Fatalf("parse NextRun: %v", perr)
	}
	due, err := st.ListDueAgentSchedules(ctx, nextRun.Add(time.Second), 10)
	if err != nil {
		t.Fatalf("ListDueAgentSchedules: %v", err)
	}
	found := false
	for _, d := range due {
		if d.ID == row.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("row %s not present in ListDueAgentSchedules once past next_run", row.ID)
	}
}

// TestExecutorApply_AddSchedule_FiresRealHook proves the wiring end to end
// through the actual reflex dispatch surface (internal/agent/reflexes.
// Executor.Apply, the same call site every add_schedule reflex fires
// through in production), not just the hook function in isolation.
func TestExecutorApply_AddSchedule_FiresRealHook(t *testing.T) {
	ctx := context.Background()
	agentID := "reflex-schedule-apply-agent"
	st := newReflexScheduleTestStore(t, agentID)

	executor := &reflexes.Executor{
		Schedule: NewReflexScheduleHook(st),
	}
	// Executor.Apply requires a non-nil Logger.
	executor.Logger = testLogger(t)

	reflex := store.AgentReflex{
		ID:         "reflex-add-schedule-1",
		AgentID:    agentID,
		Name:       "nightly-audit",
		ActionKind: store.ReflexActionAddSchedule,
		ActionSpec: `{"name":"nightly-audit-schedule","schedule_kind":"cron","schedule_spec":"0 3 * * *","body":"run the nightly audit"}`,
	}

	applied, err := executor.Apply(ctx, reflex, reflexes.State{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if applied.ActionKind != store.ReflexActionAddSchedule {
		t.Errorf("applied.ActionKind = %q", applied.ActionKind)
	}

	rows, err := st.ListAgentSchedules(ctx, agentID)
	if err != nil {
		t.Fatalf("ListAgentSchedules: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("ListAgentSchedules = %d rows, want 1", len(rows))
	}
	if rows[0].Name != "nightly-audit-schedule" {
		t.Errorf("Name = %q", rows[0].Name)
	}
	if rows[0].NextRun == "" {
		t.Error("NextRun is empty")
	}
}

// TestExecutorApply_AddSchedule_MalformedSpec_NoRowInserted is the
// Done-means regression: a malformed/incomplete action_spec fails cleanly
// -- the hook itself returns an error (asserted directly against the hook
// below), and going through the real Executor.Apply call site (which logs
// a hook error via the existing "reflex schedule hook failed" warning path
// and does not itself return an error -- matching every other action kind's
// hook-failure handling in executor.go) never leaves a broken row behind.
func TestExecutorApply_AddSchedule_MalformedSpec_NoRowInserted(t *testing.T) {
	ctx := context.Background()
	agentID := "reflex-schedule-malformed-agent"
	st := newReflexScheduleTestStore(t, agentID)

	hook := NewReflexScheduleHook(st)
	if err := hook(ctx, agentID, map[string]interface{}{
		"schedule_kind": "cron",
		// name and body and schedule_spec all missing.
	}); err == nil {
		t.Fatal("hook returned nil error for a malformed spec, want an error")
	}

	executor := &reflexes.Executor{
		Schedule: hook,
		Logger:   testLogger(t),
	}
	reflex := store.AgentReflex{
		ID:         "reflex-add-schedule-malformed",
		AgentID:    agentID,
		Name:       "malformed-schedule",
		ActionKind: store.ReflexActionAddSchedule,
		ActionSpec: `{"schedule_kind":"cron"}`,
	}
	// Apply itself does not propagate a hook error (matches Halt/SendMessage's
	// existing behavior in executor.go) -- only ListAgentSchedules below
	// proves no row was left behind.
	if _, err := executor.Apply(ctx, reflex, reflexes.State{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	rows, err := st.ListAgentSchedules(ctx, agentID)
	if err != nil {
		t.Fatalf("ListAgentSchedules: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("ListAgentSchedules = %d rows, want 0 (malformed spec must not insert a broken row): %+v", len(rows), rows)
	}
}

// TestExecutorApply_AddSchedule_MalformedCronSpec_NoRowInserted is the
// regression for TASKS/scheduling/07-wire-add-schedule-reflex.md's Review
// notes / TASKS/ESCALATIONS.md's 2026-08-20 "Scheduling Phase 2 review:
// task 07's malformed-cron gap" finding: a schedule_kind="cron" action_spec
// with a syntactically invalid (but non-empty) schedule_spec used to fall
// through to store.ComputeAgentScheduleNextRun's documented "due now"
// fallback and insert a schema-valid row -- but go-scheduler's own tick()
// (libs/go-scheduler/engine.go) re-parses CronExpr on every tick and skips
// (never claims, never fires) a row whose cron expression fails to parse,
// so that row would sit in agent_schedules forever, invisible except for a
// coarse WorkerErrors counter climbing on every tick. Mirrors
// TestExecutorApply_AddSchedule_MalformedSpec_NoRowInserted's shape: assert
// the hook itself rejects the spec directly, then confirm firing through
// the real reflexes.Executor.Apply call site (the actual production
// dispatch surface) leaves zero agent_schedules rows behind.
func TestExecutorApply_AddSchedule_MalformedCronSpec_NoRowInserted(t *testing.T) {
	ctx := context.Background()
	agentID := "reflex-schedule-malformed-cron-agent"
	st := newReflexScheduleTestStore(t, agentID)

	hook := NewReflexScheduleHook(st)
	err := hook(ctx, agentID, map[string]interface{}{
		"name":          "n",
		"schedule_kind": "cron",
		"schedule_spec": "not a cron expr",
		"body":          "body",
	})
	if err == nil {
		t.Fatal("hook returned nil error for a malformed cron schedule_spec, want an error")
	}
	const sharedRule = "schedule_spec is not a valid cron expression"
	if !strings.Contains(err.Error(), sharedRule) {
		t.Fatalf("hook error = %q, want shared validator rule %q", err, sharedRule)
	}

	executor := &reflexes.Executor{
		Schedule: hook,
		Logger:   testLogger(t),
	}
	reflex := store.AgentReflex{
		ID:         "reflex-add-schedule-malformed-cron",
		AgentID:    agentID,
		Name:       "malformed-cron-schedule",
		ActionKind: store.ReflexActionAddSchedule,
		ActionSpec: `{"name":"n","schedule_kind":"cron","schedule_spec":"not a cron expr","body":"body"}`,
	}
	// Apply itself does not propagate a hook error (matches
	// TestExecutorApply_AddSchedule_MalformedSpec_NoRowInserted's existing
	// finding for Halt/SendMessage's identical behavior in executor.go) --
	// only ListAgentSchedules below proves no row was left behind.
	if _, err := executor.Apply(ctx, reflex, reflexes.State{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	rows, err := st.ListAgentSchedules(ctx, agentID)
	if err != nil {
		t.Fatalf("ListAgentSchedules: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("ListAgentSchedules = %d rows, want 0 (malformed cron schedule_spec must not insert a row that would never fire): %+v", len(rows), rows)
	}
}
