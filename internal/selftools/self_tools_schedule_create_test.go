package selftools

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/store"
)

// makeTestAgentProfile creates a real agent_profiles row -- agent_schedules.
// agent_id is a real FK (foreign_keys=ON on this store's connection, per
// migration 071/127) so every test below needs one.
func makeTestAgentProfile(t *testing.T, s *store.Store, slug string) *store.AgentProfile {
	t.Helper()
	a := &store.AgentProfile{
		Name:         "Test Agent " + slug,
		Slug:         slug,
		SystemPrompt: "You are a test agent.",
	}
	if err := s.CreateAgent(context.Background(), a); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	return a
}

// makeTestSessionWithPrimaryAgent creates a session and attaches agentID as
// its primary agent -- mirrors durable_agents.go's selectOrCreateLaunchSession
// (EnsureSessionAgent(sess.ID, inst.ProfileID, "default", true)), the real
// mechanism a durable-agent session gets its primary-agent binding from.
func makeTestSessionWithPrimaryAgent(t *testing.T, s *store.Store, agentID string) *store.Session {
	t.Helper()
	sess := &store.Session{}
	if err := s.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := s.EnsureSessionAgent(context.Background(), sess.ID, agentID, "default", true); err != nil {
		t.Fatalf("EnsureSessionAgent: %v", err)
	}
	return sess
}

// TestCallScheduleCreate_CallerProfileContext_CronRow proves: a well-formed
// cron call, made through the H1 caller-profile ctx path (the in-process
// chat-loop's own identity mechanism), produces a real agent_schedules row
// scoped to that agent, with a correctly-computed next_run and the tool's
// own hardcoded retry-policy defaults.
func TestCallScheduleCreate_CallerProfileContext_CronRow(t *testing.T) {
	s := newTestStore(t)
	st := NewSelfToolsTransport(s)
	agentA := makeTestAgentProfile(t, s, "sched-tool-a")

	ctx := mcp.WithCallerProfile(context.Background(), agentA.ID)
	res, err := st.CallTool(ctx, scheduleCreateToolName, map[string]any{
		"kind":      "cron",
		"cron_expr": "0 9 * * *",
		"message":   "audit yesterday's export",
		"name":      "daily-export-audit",
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %+v", res.Content)
	}
	text := res.Content[0].Text
	if !strings.Contains(text, "daily-export-audit") || !strings.Contains(text, "self-sched-") {
		t.Fatalf("unexpected confirmation text: %q", text)
	}

	rows, err := s.ListAgentSchedules(context.Background(), agentA.ID)
	if err != nil {
		t.Fatalf("ListAgentSchedules: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("ListAgentSchedules: got %d rows, want 1", len(rows))
	}
	row := rows[0]

	if row.AgentID != agentA.ID {
		t.Errorf("AgentID = %q, want %q", row.AgentID, agentA.ID)
	}
	if row.Name != "daily-export-audit" {
		t.Errorf("Name = %q, want %q", row.Name, "daily-export-audit")
	}
	if row.ScheduleKind != store.ScheduleKindCron {
		t.Errorf("ScheduleKind = %q, want %q", row.ScheduleKind, store.ScheduleKindCron)
	}
	if row.ScheduleSpec != "0 9 * * *" {
		t.Errorf("ScheduleSpec = %q, want %q", row.ScheduleSpec, "0 9 * * *")
	}
	if row.Body != "audit yesterday's export" {
		t.Errorf("Body = %q, want the message", row.Body)
	}
	if row.JobType != store.ScheduleJobTypeDurableAgentWake {
		t.Errorf("JobType = %q, want %q", row.JobType, store.ScheduleJobTypeDurableAgentWake)
	}
	if row.MaxRetries != scheduleCreateMaxRetries {
		t.Errorf("MaxRetries = %d, want %d", row.MaxRetries, scheduleCreateMaxRetries)
	}
	if row.OnFail != store.ScheduleOnFailDisable {
		t.Errorf("OnFail = %q, want %q (deliberate divergence from the retry column default)", row.OnFail, store.ScheduleOnFailDisable)
	}
	if row.Status != store.ScheduleStatusActive {
		t.Errorf("Status = %q, want %q", row.Status, store.ScheduleStatusActive)
	}
	if !strings.HasPrefix(row.CreatedBy, "self:") {
		t.Errorf("CreatedBy = %q, want a self: prefix", row.CreatedBy)
	}

	gotNext, err := time.Parse(time.RFC3339, row.NextRun)
	if err != nil {
		t.Fatalf("NextRun %q did not parse: %v", row.NextRun, err)
	}
	// Recompute independently (same helper, called again "now") and assert
	// the persisted value is a real, correctly-computed next 09:00 UTC
	// occurrence -- not just any non-zero timestamp.
	independentlyComputed := store.ComputeAgentScheduleNextRun(store.ScheduleKindCron, "0 9 * * *", time.Now().UTC())
	if gotNext.Hour() != 9 || gotNext.Minute() != 0 {
		t.Errorf("NextRun = %v, want a 09:00 UTC occurrence", gotNext)
	}
	if gotNext.Before(time.Now().UTC()) {
		t.Errorf("NextRun = %v, want a time in the future", gotNext)
	}
	// Both computations should land on the same calendar occurrence
	// (allow the two "now" reads to straddle a second boundary at worst).
	if gotNext.Sub(independentlyComputed) > time.Minute || independentlyComputed.Sub(gotNext) > time.Minute {
		t.Errorf("NextRun = %v, independently recomputed = %v -- too far apart", gotNext, independentlyComputed)
	}
}

// TestCallScheduleCreate_OneShot_DueNow proves a one_shot row computes
// next_run as "due now" (matching the table's existing one_shot semantics,
// per internal/store/agent_schedules.go's ComputeAgentScheduleNextRun doc
// comment), and that an omitted name gets a derived default.
func TestCallScheduleCreate_OneShot_DueNow(t *testing.T) {
	s := newTestStore(t)
	st := NewSelfToolsTransport(s)
	agent := makeTestAgentProfile(t, s, "sched-tool-oneshot")

	before := time.Now().UTC()
	ctx := mcp.WithCallerProfile(context.Background(), agent.ID)
	res, err := st.CallTool(ctx, scheduleCreateToolName, map[string]any{
		"kind":    "one_shot",
		"message": "pick this back up",
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %+v", res.Content)
	}
	after := time.Now().UTC()

	rows, err := s.ListAgentSchedules(context.Background(), agent.ID)
	if err != nil {
		t.Fatalf("ListAgentSchedules: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("ListAgentSchedules: got %d rows, want 1", len(rows))
	}
	row := rows[0]
	if !strings.HasPrefix(row.Name, "Self-scheduled follow-up: pick this back up") {
		t.Errorf("derived Name = %q", row.Name)
	}
	gotNext, err := time.Parse(time.RFC3339, row.NextRun)
	if err != nil {
		t.Fatalf("NextRun %q did not parse: %v", row.NextRun, err)
	}
	if gotNext.Before(before.Add(-time.Second)) || gotNext.After(after.Add(time.Second)) {
		t.Errorf("one_shot NextRun = %v, want between %v and %v (due now)", gotNext, before, after)
	}
}

// TestCallScheduleCreate_SessionFallback_ResolvesPrimaryAgent proves the
// CLI-launched-agent proxy path (session_id stamped, no H1 caller-profile
// stamp -- exactly what internal/api/tools_call.go's handleSelfToolCall
// actually stamps today) still resolves the correct calling agent via
// store.GetSessionPrimaryAgent, and scopes the inserted row to it.
func TestCallScheduleCreate_SessionFallback_ResolvesPrimaryAgent(t *testing.T) {
	s := newTestStore(t)
	st := NewSelfToolsTransport(s)
	agent := makeTestAgentProfile(t, s, "sched-tool-session-fallback")
	sess := makeTestSessionWithPrimaryAgent(t, s, agent.ID)

	// No WithCallerProfile at all -- only session id, matching the real
	// proxy path's actual ctx shape.
	ctx := mcp.WithSessionID(context.Background(), sess.ID)
	res, err := st.CallTool(ctx, scheduleCreateToolName, map[string]any{
		"kind":    "one_shot",
		"message": "resolved via session fallback",
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %+v", res.Content)
	}

	rows, err := s.ListAgentSchedules(context.Background(), agent.ID)
	if err != nil {
		t.Fatalf("ListAgentSchedules: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("ListAgentSchedules: got %d rows, want 1", len(rows))
	}
	if rows[0].AgentID != agent.ID {
		t.Errorf("AgentID = %q, want %q", rows[0].AgentID, agent.ID)
	}
}

// TestCallScheduleCreate_CannotTargetAnotherAgent is the required negative
// proof: this tool's schema carries no agent_id input at all, so there is
// no argument a caller can pass to redirect the row onto a different
// agent's agent_id. Two different callers (via two different caller-
// profile contexts) each get a row scoped to THEIR OWN agent, never the
// other's -- confirming the enforcement is real, not merely "the schema
// doesn't advertise the field" on paper.
func TestCallScheduleCreate_CannotTargetAnotherAgent(t *testing.T) {
	s := newTestStore(t)
	st := NewSelfToolsTransport(s)
	agentA := makeTestAgentProfile(t, s, "sched-tool-victim")
	agentB := makeTestAgentProfile(t, s, "sched-tool-attacker")

	ctxB := mcp.WithCallerProfile(context.Background(), agentB.ID)
	// Even an explicit, unsolicited "agent_id" arg (not part of the
	// declared schema, but nothing stops a caller from sending it) must be
	// ignored -- the handler never reads args["agent_id"] anywhere.
	res, err := st.CallTool(ctxB, scheduleCreateToolName, map[string]any{
		"kind":     "one_shot",
		"message":  "attempted cross-agent schedule",
		"agent_id": agentA.ID,
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %+v", res.Content)
	}

	victimRows, err := s.ListAgentSchedules(context.Background(), agentA.ID)
	if err != nil {
		t.Fatalf("ListAgentSchedules(agentA): %v", err)
	}
	if len(victimRows) != 0 {
		t.Fatalf("agentA (never the caller) got %d schedule rows, want 0 -- cross-agent scheduling was NOT blocked", len(victimRows))
	}

	attackerRows, err := s.ListAgentSchedules(context.Background(), agentB.ID)
	if err != nil {
		t.Fatalf("ListAgentSchedules(agentB): %v", err)
	}
	if len(attackerRows) != 1 {
		t.Fatalf("agentB (the real caller) got %d schedule rows, want 1", len(attackerRows))
	}
	if attackerRows[0].AgentID != agentB.ID {
		t.Errorf("row.AgentID = %q, want %q", attackerRows[0].AgentID, agentB.ID)
	}
}

// TestCallScheduleCreate_NoIdentityInContext proves a caller with neither
// an H1 caller-profile stamp nor a session id is rejected outright, rather
// than falling back to a blank/empty agent_id.
func TestCallScheduleCreate_NoIdentityInContext(t *testing.T) {
	s := newTestStore(t)
	st := NewSelfToolsTransport(s)

	res, err := st.CallTool(context.Background(), scheduleCreateToolName, map[string]any{
		"kind":    "one_shot",
		"message": "nobody's follow-up",
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected an error result for a context with no calling-agent identity")
	}
	if !strings.Contains(res.Content[0].Text, "no calling-agent identity") {
		t.Errorf("unexpected error text: %q", res.Content[0].Text)
	}
}

// TestCallScheduleCreate_ValidationErrors covers the input-validation gate:
// invalid kind, missing/invalid cron_expr, cron_expr supplied for
// one_shot, and missing message.
func TestCallScheduleCreate_ValidationErrors(t *testing.T) {
	s := newTestStore(t)
	st := NewSelfToolsTransport(s)
	agent := makeTestAgentProfile(t, s, "sched-tool-validation")
	ctx := mcp.WithCallerProfile(context.Background(), agent.ID)

	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{
			name: "invalid kind",
			args: map[string]any{"kind": "every_n_ticks", "message": "m"},
			want: "kind must be",
		},
		{
			name: "cron missing cron_expr",
			args: map[string]any{"kind": "cron", "message": "m"},
			want: "cron_expr is required",
		},
		{
			name: "cron invalid cron_expr",
			args: map[string]any{"kind": "cron", "cron_expr": "not a cron expr", "message": "m"},
			want: "invalid cron_expr",
		},
		{
			name: "one_shot with cron_expr",
			args: map[string]any{"kind": "one_shot", "cron_expr": "0 9 * * *", "message": "m"},
			want: "must be omitted",
		},
		{
			name: "missing message",
			args: map[string]any{"kind": "one_shot"},
			want: "message is required",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := st.CallTool(ctx, scheduleCreateToolName, tc.args)
			if err != nil {
				t.Fatalf("CallTool: %v", err)
			}
			if !res.IsError {
				t.Fatalf("expected error result for %s", tc.name)
			}
			if !strings.Contains(res.Content[0].Text, tc.want) {
				t.Errorf("error text = %q, want to contain %q", res.Content[0].Text, tc.want)
			}
		})
	}

	rows, err := s.ListAgentSchedules(context.Background(), agent.ID)
	if err != nil {
		t.Fatalf("ListAgentSchedules: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("validation-rejected calls inserted %d rows, want 0", len(rows))
	}
}
