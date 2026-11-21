package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/libs/harness/core/adapters/runtimeevents"
	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/agent"
	"github.com/hollis-labs/nanite/internal/provider/fake"
	"github.com/hollis-labs/nanite/internal/store"
)

const integrityAgent = `---
schema_version: "2"
definition_id: def:integrity-test
revision: "1"
name: integrity-test
description: Integrity fixture.
behavior:
  purpose: Test integrity checks on recovery.
requirements: {}
harness_profile:
  permissions:
    profile: default
continuity:
  mode: ephemeral
---
Integrity test.
`

// crashedConversation writes, through the store, what a process killed
// mid-tool leaves behind: a running turn whose tool call has no result.
// corrupt, if set, adds records to the transcript before the "crash".
func crashedConversation(t *testing.T, corrupt func(*store.Journal, store.Turn)) (dir, conversation string, turn store.Turn) {
	t.Helper()
	dir = t.TempDir()
	for name, body := range map[string]string{"agent.md": integrityAgent, "host.yaml": "kind: embedded\nlifetime: application\napplication_id: integrity-test\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil { // #nosec G703 -- A fixed name under the test's temporary directory.
			t.Fatal(err)
		}
	}
	ctx := t.Context()
	db, err := store.Open(ctx, filepath.Join(dir, ".nanite"), nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	conversation = store.NewID(now)
	if err := db.CreateConversation(ctx, store.Conversation{ID: conversation, Role: "chat", Status: "active", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	j, err := db.OpenJournal(ctx, conversation)
	if err != nil {
		t.Fatal(err)
	}
	turn = store.Turn{ID: store.NewID(now), UserMessageID: store.NewID(now)}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(j.StartTurn(turn, now))
	must(j.AppendMessage(store.Message{ID: turn.UserMessageID, TurnID: turn.ID, Role: "user", CreatedAt: now,
		Blocks: []llmtypes.ContentBlock{{Type: "text", Text: "secret question"}}}, nil))
	must(j.AppendMessage(store.Message{ID: store.NewID(now), TurnID: turn.ID, Role: "assistant", CreatedAt: now,
		Blocks: []llmtypes.ContentBlock{{Type: "tool_use", ID: "call-1", Name: "read", Input: &map[string]any{}}}}, &turn.UserMessageID))
	if corrupt != nil {
		corrupt(j, turn)
	}
	must(j.Close())
	must(db.Close())
	return dir, conversation, turn
}

// orphanResult answers a tool call that was never made.
func orphanResult(j *store.Journal, turn store.Turn) {
	now := time.Now().UTC()
	_ = j.AppendMessage(store.Message{ID: store.NewID(now), TurnID: turn.ID, Role: "tool", CreatedAt: now,
		Blocks: []llmtypes.ContentBlock{{Type: "tool_result", ToolUseID: "never-called", Content: "secret output"}}}, nil)
}

func segmentOf(t *testing.T, dir, conversation string) string {
	t.Helper()
	return filepath.Join(dir, ".nanite", "transcripts", conversation, "00000000000000000001.jsonl")
}

type eventLog struct {
	mu     sync.Mutex
	events []string
}

func (l *eventLog) sink() agent.EventSink {
	return runtimeevents.SinkFunc(func(_ context.Context, ev runtimeevents.Event) error {
		raw, _ := json.Marshal(ev)
		l.mu.Lock()
		l.events = append(l.events, string(raw))
		l.mu.Unlock()
		return nil
	})
}

// integrity returns the integrity events, which must carry no content.
// They are emitted asynchronously: it waits until no new event has arrived
// for a short while.
func (l *eventLog) integrity(t *testing.T) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	last := -1
	for time.Now().Before(deadline) {
		l.mu.Lock()
		n := len(l.events)
		l.mu.Unlock()
		if n == last {
			break
		}
		last = n
		time.Sleep(150 * time.Millisecond)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []map[string]any
	for _, raw := range l.events {
		if !strings.Contains(raw, `"integrity"`) {
			continue
		}
		for _, content := range []string{"secret question", "secret output", "never-called", "call-1"} {
			if strings.Contains(raw, content) {
				t.Fatalf("integrity event carries %q: %s", content, raw)
			}
		}
		var ev struct{ Payload map[string]any }
		if err := json.Unmarshal([]byte(raw), &ev); err != nil {
			t.Fatal(err)
		}
		out = append(out, ev.Payload)
	}
	return out
}

func TestResumeInterruptedTurnPassesIntegrity(t *testing.T) {
	dir, conversation, turn := crashedConversation(t, nil)
	var events eventLog
	a, err := agent.Open(t.Context(), agent.Options{Dir: dir, Provider: fake.New(fake.Text("recovered")), GlobalSkillsDir: "-", Events: events.sink()})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	got := events.integrity(t)
	if len(got) != 1 || got[0]["stage"] != "recover" || got[0]["disposition"] != "resumable" || got[0]["severity"] != "recoverable" {
		t.Fatalf("recover events %v", got)
	}
	c, err := a.Conversation(t.Context(), conversation)
	if err != nil {
		t.Fatal(err)
	}
	report, err := c.CheckIntegrity(t.Context())
	if err != nil || !report.Safe() || report.Worst() != agent.IntegrityRecoverable {
		t.Fatalf("check %v, %v", report.Findings, err)
	}
	run, err := c.Resume(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if result, err := run.Wait(); err != nil || result.Text != "recovered" || run.ID() != turn.ID {
		t.Fatalf("resume %+v, %v", result, err)
	}
	if got := events.integrity(t); len(got) != 2 || got[1]["stage"] != "resume" || got[1]["disposition"] != "resumable" {
		t.Fatalf("resume events %v", got)
	}
	report, err = c.CheckIntegrity(t.Context())
	if err != nil || len(report.Findings) != 0 {
		t.Fatalf("after resume %v, %v", report.Findings, err)
	}
}

func TestCleanConversationResumeIsNotChecked(t *testing.T) {
	// Opening an agent with no running turn validates nothing: candidate
	// selection is bounded by running turns, not by history.
	dir, conversation, _ := crashedConversation(t, nil)
	var events eventLog
	a, err := agent.Open(t.Context(), agent.Options{Dir: dir, Provider: fake.New(fake.Text("ok")), GlobalSkillsDir: "-", Events: events.sink()})
	if err != nil {
		t.Fatal(err)
	}
	c, err := a.Conversation(t.Context(), conversation)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Discard(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	events = eventLog{}
	a, err = agent.Open(t.Context(), agent.Options{Dir: dir, Provider: fake.New(fake.Text("ok")), GlobalSkillsDir: "-", Events: events.sink()})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if got := events.integrity(t); len(got) != 0 {
		t.Fatalf("reopen validated %v", got)
	}
	c, err = a.Conversation(t.Context(), conversation)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Resume(t.Context()); !errors.Is(err, agent.ErrNoInterrupted) {
		t.Fatalf("resume of a clean conversation: %v", err)
	}
}

func TestResumeRefusesUnsafeFindingAndDiscardStaysAvailable(t *testing.T) {
	dir, conversation, turn := crashedConversation(t, orphanResult)
	var events eventLog
	provider := fake.New() // any model call fails the test below
	a, err := agent.Open(t.Context(), agent.Options{Dir: dir, Provider: provider, GlobalSkillsDir: "-", Events: events.sink()})
	if err != nil {
		t.Fatal(err)
	}
	if got := events.integrity(t); len(got) != 1 || got[0]["disposition"] != "blocked" || got[0]["severity"] != "unsafe" {
		t.Fatalf("recover events %v", got)
	}
	c, err := a.Conversation(t.Context(), conversation)
	if err != nil {
		t.Fatal(err)
	}
	if c.Interrupted() == nil || c.Interrupted().ID != turn.ID {
		t.Fatal("recovery did not mark the unsafe turn interrupted, so Discard could not reach it")
	}
	run, err := c.Resume(t.Context())
	var refused *agent.IntegrityError
	if run != nil || !errors.Is(err, agent.ErrIntegrity) || !errors.As(err, &refused) {
		t.Fatalf("resume = %v, %v", run, err)
	}
	unsafe := refused.Report.Unsafe()
	if refused.Conversation != conversation || len(unsafe) != 1 || unsafe[0].Code != "orphan_tool_result" || unsafe[0].Evidence["tool_use_id"] != "never-called" {
		t.Fatalf("refusal %+v", refused)
	}
	if !strings.Contains(err.Error(), "orphan_tool_result") || strings.Contains(err.Error(), "secret") ||
		!refused.Interrupted || !strings.Contains(err.Error(), "can still be discarded") {
		t.Fatalf("error text %q (interrupted %v)", err, refused.Interrupted)
	}
	if got := events.integrity(t); len(got) != 2 || got[1]["stage"] != "resume" || got[1]["disposition"] != "blocked" {
		t.Fatalf("resume events %v", got)
	}
	if c.Interrupted() == nil {
		t.Fatal("a refused resume changed the turn")
	}
	if err := c.Discard(t.Context()); err != nil {
		t.Fatalf("discard after a refused resume: %v", err)
	}
	if c.Interrupted() != nil {
		t.Fatal("discard left the turn interrupted")
	}
	// Discard closes the turn but leaves the history unsafe: a new turn
	// would send it to the model, so it is refused too, across a restart.
	if run, err := c.Send(t.Context(), agent.Input{Text: "next"}); run != nil || !errors.As(err, &refused) ||
		refused.Interrupted || !strings.Contains(err.Error(), "closed to new turns") {
		t.Fatalf("send after discard = %v, %v", run, err)
	}
	if got := events.integrity(t); got[len(got)-1]["stage"] != "send" || got[len(got)-1]["disposition"] != "blocked" {
		t.Fatalf("send events %v", got)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = agent.Open(t.Context(), agent.Options{Dir: dir, Provider: provider, GlobalSkillsDir: "-"})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if c, err = a.Conversation(t.Context(), conversation); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Send(t.Context(), agent.Input{Text: "next"}); !errors.Is(err, agent.ErrIntegrity) {
		t.Fatalf("send after a restart: %v", err)
	}
	// Other conversations are untouched.
	other, err := a.NewConversation(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if report, err := other.CheckIntegrity(t.Context()); err != nil || len(report.Findings) != 0 {
		t.Fatalf("a new conversation: %v, %v", report.Findings, err)
	}
}

func TestTornTailBlocksResumeUntilRepaired(t *testing.T) {
	dir, conversation, turn := crashedConversation(t, nil)
	segment := segmentOf(t, dir, conversation)
	committed, err := os.Stat(segment)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(segment, os.O_WRONLY|os.O_APPEND, 0) // #nosec G304 -- The fixture's own transcript segment.
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"type":"user","uuid":"torn","mess`); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	var events eventLog
	a, err := agent.Open(t.Context(), agent.Options{Dir: dir, Provider: fake.New(fake.Text("recovered")), GlobalSkillsDir: "-", Events: events.sink()})
	if err != nil {
		t.Fatalf("a torn conversation kept the agent from opening: %v", err)
	}
	if got := events.integrity(t); len(got) != 1 || got[0]["disposition"] != "blocked" {
		t.Fatalf("recover events %v", got)
	}
	c, err := a.Conversation(t.Context(), conversation)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Resume(t.Context())
	var refused *agent.IntegrityError
	if !errors.As(err, &refused) || refused.Report.Unsafe()[0].Code != "torn_tail" {
		t.Fatalf("resume of a torn conversation: %v", err)
	}
	before, err := os.ReadFile(segment) // #nosec G304 -- The fixture's own transcript segment.
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Send(t.Context(), agent.Input{Text: "more"}); !errors.As(err, &refused) {
		t.Fatalf("send past a torn tail: %v", err)
	}
	if err := c.Discard(t.Context()); !errors.As(err, &refused) {
		t.Fatalf("discard of a torn conversation: %v", err)
	}
	if after, err := os.ReadFile(segment); err != nil || string(after) != string(before) { // #nosec G304 -- The fixture's own transcript segment.
		t.Fatalf("the torn transcript changed: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}

	// The operator repairs the transcript by cutting the uncommitted tail,
	// then rebuilds the index from the transcripts.
	if err := os.Truncate(segment, committed.Size()); err != nil {
		t.Fatal(err)
	}
	a, err = agent.Open(t.Context(), agent.Options{Dir: dir, Provider: fake.New(fake.Text("recovered")), GlobalSkillsDir: "-"})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := a.RebuildIndex(t.Context()); err != nil {
		t.Fatal(err)
	}
	c, err = a.Conversation(t.Context(), conversation)
	if err != nil {
		t.Fatal(err)
	}
	if report, err := c.CheckIntegrity(t.Context()); err != nil || !report.Safe() {
		t.Fatalf("after repair %v, %v", report.Findings, err)
	}
	run, err := c.Resume(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if result, err := run.Wait(); err != nil || result.Text != "recovered" || run.ID() != turn.ID {
		t.Fatalf("resume after repair %+v, %v", result, err)
	}
}

func TestSecondProcessIsRefusedWhileRecovering(t *testing.T) {
	dir, conversation, turn := crashedConversation(t, nil)
	first, err := agent.Open(t.Context(), agent.Options{Dir: dir, Provider: fake.New(fake.Text("recovered")), GlobalSkillsDir: "-"})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	// A second process on the same data directory is refused by the agent
	// lock before it replays, validates or writes anything.
	if second, err := agent.Open(t.Context(), agent.Options{Dir: dir, Provider: fake.New(), GlobalSkillsDir: "-"}); err == nil {
		_ = second.Close()
		t.Fatal("two processes recovered the same agent")
	} else if !strings.Contains(err.Error(), "lock agent data directory") {
		t.Fatalf("second open: %v", err)
	}
	c, err := first.Conversation(t.Context(), conversation)
	if err != nil {
		t.Fatal(err)
	}
	if report, err := c.CheckIntegrity(t.Context()); err != nil || !report.Safe() {
		t.Fatalf("after the refused open %v, %v", report.Findings, err)
	}
	run, err := c.Resume(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil || run.ID() != turn.ID {
		t.Fatal(err)
	}
}

// A transcript a run is writing can look torn: the check refuses as busy
// instead of reporting a transient finding.
func TestIntegrityCheckRefusesWhileARunIsActive(t *testing.T) {
	dir := t.TempDir()
	writeAgent(t, dir)
	p := fake.New(fake.Response{Events: []llmtypes.StreamEvent{{Type: llmtypes.EventDelta, Content: "running"}}, Stall: true})
	a, err := agent.Open(t.Context(), agent.Options{Dir: dir, GlobalSkillsDir: "-", Provider: p})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	c, err := a.NewConversation(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	run, err := c.Send(t.Context(), agent.Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	for event := range run.Events() {
		if _, ok := event.(agent.TextDelta); ok {
			break
		}
	}
	if _, err := c.CheckIntegrity(t.Context()); !errors.Is(err, agent.ErrBusy) {
		t.Fatalf("check during a run: %v", err)
	}
	if _, err := c.Resume(t.Context()); !errors.Is(err, agent.ErrBusy) {
		t.Fatalf("resume during a run: %v", err)
	}
	run.Cancel()
	_, _ = run.Wait()
	if report, err := c.CheckIntegrity(t.Context()); err != nil || !report.Safe() {
		t.Fatalf("after the run: %v, %v", report.Findings, err)
	}
}

// A crash while starting a new turn tears the tail with no turn running.
// Recovery still finds it (open-time replay saw the torn tail), and every
// run is refused with the findings until the tail is repaired.
func TestTornTailWithNoRunningTurnIsFound(t *testing.T) {
	dir, conversation, _ := crashedConversation(t, nil)
	// Close the crashed turn first, so nothing is running.
	a, err := agent.Open(t.Context(), agent.Options{Dir: dir, Provider: fake.New(), GlobalSkillsDir: "-"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := a.Conversation(t.Context(), conversation)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Discard(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	segment := segmentOf(t, dir, conversation)
	committed, err := os.Stat(segment)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(segment, os.O_WRONLY|os.O_APPEND, 0) // #nosec G304 -- The fixture's own transcript segment.
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"type":"system","subtype":"nanite.turn.start","pay`); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	var events eventLog
	a, err = agent.Open(t.Context(), agent.Options{Dir: dir, Provider: fake.New(fake.Text("after repair")), GlobalSkillsDir: "-", Events: events.sink()})
	if err != nil {
		t.Fatal(err)
	}
	if got := events.integrity(t); len(got) != 1 || got[0]["stage"] != "recover" || got[0]["disposition"] != "blocked" {
		t.Fatalf("recover events %v", got)
	}
	if c, err = a.Conversation(t.Context(), conversation); err != nil {
		t.Fatal(err)
	}
	var refused *agent.IntegrityError
	if _, err := c.Send(t.Context(), agent.Input{Text: "next"}); !errors.As(err, &refused) || refused.Report.Unsafe()[0].Code != "torn_tail" {
		t.Fatalf("send: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(segment, committed.Size()); err != nil {
		t.Fatal(err)
	}
	a, err = agent.Open(t.Context(), agent.Options{Dir: dir, Provider: fake.New(fake.Text("after repair")), GlobalSkillsDir: "-"})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if c, err = a.Conversation(t.Context(), conversation); err != nil {
		t.Fatal(err)
	}
	// The repaired conversation passes its check on the next run, which
	// clears the mark and goes ahead.
	run, err := c.Send(t.Context(), agent.Input{Text: "next"})
	if err != nil {
		t.Fatal(err)
	}
	if result, err := run.Wait(); err != nil || result.Text != "after repair" {
		t.Fatalf("send after repair %+v, %v", result, err)
	}
}

// Damage before the tail is not a torn tail: nothing past it is projected
// as if it were the whole history, and the agent refuses to open, as it
// always has.
func TestMidSegmentDamageStillRefusesToOpen(t *testing.T) {
	dir, conversation, _ := crashedConversation(t, nil)
	segment := segmentOf(t, dir, conversation)
	raw, err := os.ReadFile(segment) // #nosec G304 -- The fixture's own transcript segment.
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.SplitAfter(string(raw), "\n")
	lines[2] = "not a record\n"
	if err := os.WriteFile(segment, []byte(strings.Join(lines, "")), 0o600); err != nil { // #nosec G703 -- The fixture's own transcript segment.
		t.Fatal(err)
	}
	// A fresh index makes the store replay from the start.
	if err := os.Remove(filepath.Join(dir, ".nanite", "nanite.db")); err != nil {
		t.Fatal(err)
	}
	a, err := agent.Open(t.Context(), agent.Options{Dir: dir, Provider: fake.New(), GlobalSkillsDir: "-"})
	if err == nil {
		_ = a.Close()
		t.Fatal("opened over damaged committed history")
	}
	if !strings.Contains(err.Error(), "changed history") {
		t.Fatalf("open: %v", err)
	}
}

// A host's event sink may call back into the agent from an integrity event:
// the check emits with no lock held, so this cannot deadlock.
func TestIntegrityEventSinkMayCallBack(t *testing.T) {
	dir, conversation, _ := crashedConversation(t, orphanResult)
	var mu sync.Mutex
	var c *agent.Conversation
	calledBack := make(chan error, 8)
	sink := runtimeevents.SinkFunc(func(ctx context.Context, ev runtimeevents.Event) error {
		raw, _ := json.Marshal(ev)
		mu.Lock()
		conv := c
		mu.Unlock()
		if conv != nil && strings.Contains(string(raw), `"integrity"`) {
			_, err := conv.CheckIntegrity(ctx)
			// Calls that run a check of their own and emit for the same
			// conversation, from inside its event.
			_ = conv.Discard(ctx)
			_, _ = conv.Send(ctx, agent.Input{Text: "from the sink"})
			select {
			case calledBack <- err:
			default:
			}
		}
		return nil
	})
	a, err := agent.Open(t.Context(), agent.Options{Dir: dir, Provider: fake.New(), GlobalSkillsDir: "-", Events: sink})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	conv, err := a.Conversation(t.Context(), conversation)
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	c = conv
	mu.Unlock()
	done := make(chan error, 1)
	go func() { _, err := conv.Resume(t.Context()); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, agent.ErrIntegrity) {
			t.Fatalf("resume: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("resume deadlocked with a sink that calls back")
	}
	// Callbacks run concurrently with other checks of this conversation, so
	// ErrBusy is a fair answer; what must not happen is a hang.
	if err := <-calledBack; err != nil && !errors.Is(err, agent.ErrBusy) {
		t.Fatalf("the sink's call back: %v", err)
	}

}

// Dispatch (whose subtasks may copy the parent's history) and Record
// (which appends to it) are refused on a blocked conversation too.
func TestDispatchAndRecordRefusedWhenBlocked(t *testing.T) {
	dir, conversation, _ := crashedConversation(t, orphanResult)
	a, err := agent.Open(t.Context(), agent.Options{Dir: dir, Provider: fake.New(), GlobalSkillsDir: "-",
		Builtins: []agent.BuiltinGroup{agent.BuiltinSubtasks}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	c, err := a.Conversation(t.Context(), conversation)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Discard(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Dispatch(t.Context(), []agent.DispatchStart{{Goal: "look"}}); !errors.Is(err, agent.ErrIntegrity) {
		t.Fatalf("dispatch on a blocked conversation: %v", err)
	}
	if err := c.Record(t.Context(), "asked", "answered"); !errors.Is(err, agent.ErrIntegrity) {
		t.Fatalf("record on a blocked conversation: %v", err)
	}
}

// A tail torn while the agent runs (a short write, a full disk) is found by
// the next run that opens the journal: it fails with the findings and marks
// the conversation, so later runs are refused before any work.
func TestTornTailWhileRunningIsTyped(t *testing.T) {
	dir := t.TempDir()
	writeAgent(t, dir)
	a, err := agent.Open(t.Context(), agent.Options{Dir: dir, GlobalSkillsDir: "-", Provider: fake.New(fake.Text("one"), fake.Text("two"))})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	c, err := a.NewConversation(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	run, err := c.Send(t.Context(), agent.Input{Text: "first"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(segmentOf(t, dir, c.ID()), os.O_WRONLY|os.O_APPEND, 0) // #nosec G304 -- The test's own transcript segment.
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"type":"sys`); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	run, err = c.Send(t.Context(), agent.Input{Text: "second"})
	if err != nil {
		t.Fatal(err)
	}
	var refused *agent.IntegrityError
	if _, err := run.Wait(); !errors.As(err, &refused) || refused.Report.Unsafe()[0].Code != "torn_tail" {
		t.Fatalf("run over a tail torn in-process: %v", err)
	}
	if _, err := c.Send(t.Context(), agent.Input{Text: "third"}); !errors.As(err, &refused) {
		t.Fatalf("the next send: %v", err)
	}
}
