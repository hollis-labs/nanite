package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/store"
)

// CW-20260911-0095. A durable_agent_wake schedule whose instance was archived
// out from under it produced two distinct defects, and these tests pin both.
//
// The live instance ran for three weeks and wrote 231MB: the resolver lists
// with includeArchived=false, so the archived row was invisible and reported
// as "no durable_agent_instances row" -- an absence, when a row existed. That
// message sent two separate investigations to the wrong table. And because
// conversion fails before firing, next_run never advances, so the row is
// permanently due and the warning repeated at tick frequency forever.

// wakeScheduleFixture builds a profile, one instance, and a due
// durable_agent_wake schedule pointing at that profile. Returns the adapter,
// the instance, and the schedule id.
func wakeScheduleFixture(t *testing.T, slug string, buf *bytes.Buffer) (*StoreAdapter, *store.DurableAgentInstance, string, time.Time) {
	t.Helper()
	s := newAdapterTestStore(t)
	agent := makeAdapterTestAgent(t, s, slug)
	inst := makeAdapterTestInstance(t, s, agent.ID, slug+"-inst")

	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	schedID := slug + "-wake"
	mustInsertSchedule(t, s, store.AgentSchedule{
		ID: schedID, AgentID: agent.ID, Name: schedID,
		ScheduleKind: store.ScheduleKindCron, ScheduleSpec: "0 3 * * *",
		Body:    "## Wake\nDo the thing.",
		JobType: store.ScheduleJobTypeDurableAgentWake,
		NextRun: now.Add(-time.Minute).Format(time.RFC3339),
	})

	logger := slog.Default()
	if buf != nil {
		logger = slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	return &StoreAdapter{Store: s, Logger: logger}, inst, schedID, now
}

// TestResolveDurableAgentInstance_ArchivedReportsArchivedNotAbsent is the
// control for the misleading message. Before the fix the archived case
// returned errNoDurableAgentInstance -- "no row for this agent_profiles.id"
// -- while a row demonstrably existed.
func TestResolveDurableAgentInstance_ArchivedReportsArchivedNotAbsent(t *testing.T) {
	ctx := context.Background()
	adapter, inst, _, _ := wakeScheduleFixture(t, "archived-msg", nil)

	// Resolves cleanly while the instance is live.
	if _, err := adapter.resolveDurableAgentInstanceID(inst.ProfileID); err != nil {
		t.Fatalf("resolve before archive: %v", err)
	}

	if _, err := adapter.Store.ArchiveDurableAgentInstance(ctx, inst.ID); err != nil {
		t.Fatalf("ArchiveDurableAgentInstance: %v", err)
	}

	_, err := adapter.resolveDurableAgentInstanceID(inst.ProfileID)
	if !errors.Is(err, errArchivedDurableAgentInstance) {
		t.Fatalf("resolve after archive = %v, want errArchivedDurableAgentInstance", err)
	}
	if errors.Is(err, errNoDurableAgentInstance) {
		t.Fatal("archived instance reported as an absence; that is the message that cost two investigations")
	}
}

// TestResolveDurableAgentInstance_GenuinelyAbsentStillReportsAbsent keeps the
// original message reachable. Without this the fix could report "archived"
// for a profile that never had an instance at all, which is the same class of
// wrong message pointing the other way.
func TestResolveDurableAgentInstance_GenuinelyAbsentStillReportsAbsent(t *testing.T) {
	s := newAdapterTestStore(t)
	agent := makeAdapterTestAgent(t, s, "no-instance")
	adapter := &StoreAdapter{Store: s, Logger: slog.Default()}

	_, err := adapter.resolveDurableAgentInstanceID(agent.ID)
	if !errors.Is(err, errNoDurableAgentInstance) {
		t.Fatalf("resolve with no instance at all = %v, want errNoDurableAgentInstance", err)
	}
}

// TestListDueSchedules_UnconvertibleRowWarnsOnceThenDebugs pins the volume
// fix. The row stays permanently due across every call -- conversion fails
// before firing, so nothing advances next_run -- which is exactly the live
// condition. Ten ticks must produce one warn, not ten.
func TestListDueSchedules_UnconvertibleRowWarnsOnceThenDebugs(t *testing.T) {
	ctx := context.Background()
	var buf bytes.Buffer
	adapter, inst, schedID, now := wakeScheduleFixture(t, "warn-once", &buf)

	if _, err := adapter.Store.ArchiveDurableAgentInstance(ctx, inst.ID); err != nil {
		t.Fatalf("ArchiveDurableAgentInstance: %v", err)
	}

	const ticks = 10
	for i := 0; i < ticks; i++ {
		due, err := adapter.ListDueSchedules(ctx, now, 10)
		if err != nil {
			t.Fatalf("ListDueSchedules tick %d: %v", i, err)
		}
		// The row is skipped every tick, never converted.
		for _, d := range due {
			if d.ID == schedID {
				t.Fatalf("tick %d returned the unconvertible schedule %s", i, schedID)
			}
		}
	}

	warns, debugs := countLevels(t, buf.Bytes(), schedID)
	if warns != 1 {
		t.Fatalf("got %d WARN lines for %s across %d ticks, want exactly 1", warns, schedID, ticks)
	}
	if debugs != ticks-1 {
		t.Fatalf("got %d DEBUG lines, want %d — the information must survive the demotion, not vanish", debugs, ticks-1)
	}
	if !strings.Contains(buf.String(), "archived") {
		t.Fatal("the warning does not say the target is archived; that is the whole diagnostic value")
	}
}

// countLevels counts WARN and DEBUG records mentioning scheduleID.
func countLevels(t *testing.T, raw []byte, scheduleID string) (warns, debugs int) {
	t.Helper()
	for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal(line, &rec); err != nil {
			t.Fatalf("log line is not JSON: %s", line)
		}
		if id, _ := rec["schedule_id"].(string); id != scheduleID {
			continue
		}
		switch rec["level"] {
		case "WARN":
			warns++
		case "DEBUG":
			debugs++
		}
	}
	return warns, debugs
}
