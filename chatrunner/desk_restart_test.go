package chatrunner_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	chat "github.com/hollis-labs/libs/agent/chat"
	"github.com/hollis-labs/libs/agent/desk"
	chatstream "github.com/hollis-labs/libs/ui/ui-go/chatstream"
	"github.com/hollis-labs/nanite/agent"
	"github.com/hollis-labs/nanite/chatrunner"
)

// restartHost runs one agent directory through several runner processes,
// collecting the desk's notices without a chat Service in between.
type restartHost struct {
	dir string
	mu  sync.Mutex
	got []string
}

const restartChat = "restart-chat"

func newRestartHost(t *testing.T) *restartHost {
	t.Helper()
	dir := t.TempDir()
	definition := "---\nschema_version: \"2\"\ndefinition_id: def:chat-test\nrevision: \"1\"\nname: chat-test\ndescription: Chat test.\nbehavior:\n  purpose: Test.\nrequirements: {}\nharness_profile:\n  permissions:\n    profile: default\ncontinuity:\n  mode: durable\n---\nTest instructions.\n"
	profile := "kind: embedded\nlifetime: application\napplication_id: chat-test\nruntime:\n  provider: anthropic\n  model: test-model\n"
	for name, text := range map[string]string{"agent.md": definition, "host.yaml": profile} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return &restartHost{dir: dir}
}

// open starts a runner process on the directory.
func (h *restartHost) open(t *testing.T, b *brain, d chatrunner.DeskOptions) *chatrunner.Runner {
	t.Helper()
	r, err := chatrunner.Open(t.Context(), chatrunner.Options{
		Agent: agent.Options{Dir: h.dir, Provider: b, GlobalSkillsDir: "-", Builtins: []agent.BuiltinGroup{agent.BuiltinSubtasks, agent.BuiltinBackground}},
		Observe: func(_ context.Context, _ string, _ chat.Turn, run chat.Run) error {
			for ev, err := range run.Events() {
				if err != nil {
					return err
				}
				if ev.Verb == chatstream.VerbActivity && ev.Kind == chatrunner.ActivityNotice {
					v := activityValue(t, ev)
					if v["kind"] == chatrunner.NoticeKindDesk {
						body, _ := v["body"].(string)
						h.mu.Lock()
						h.got = append(h.got, body)
						h.mu.Unlock()
					}
				}
			}
			return nil
		},
		Desk: &d,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

// turn runs a user turn to its end and returns its events.
func turn(t *testing.T, r *chatrunner.Runner, text string) []chatstream.Event {
	t.Helper()
	x, err := r.Start(t.Context(), chat.Turn{ConversationID: restartChat, Text: text})
	if err != nil {
		t.Fatal(err)
	}
	var evs []chatstream.Event
	for ev, err := range x.Events() {
		if err != nil {
			t.Fatal(err)
		}
		evs = append(evs, ev)
	}
	return evs
}

func (h *restartHost) notices() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return strings.Join(h.got, "\n---\n")
}

// waitNotices waits until the desk's notices hold every text in has.
func (h *restartHost) waitNotices(t *testing.T, what string, has ...string) string {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		all := h.notices()
		missing := false
		for _, s := range has {
			if !strings.Contains(all, s) {
				missing = true
			}
		}
		if !missing {
			return all
		}
		if time.Now().After(deadline) {
			t.Fatalf("waiting for %s; notices so far:\n%s", what, all)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitFor polls cond for up to 15s.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// once closes the runner, which drains every queued notice through
// Observe, then checks each text was delivered exactly once.
func (h *restartHost) once(t *testing.T, r *chatrunner.Runner, texts ...string) {
	t.Helper()
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	all := h.notices()
	for _, s := range texts {
		if n := strings.Count(all, s); n != 1 {
			t.Fatalf("%q delivered %d times:\n%s", s, n, all)
		}
	}
}

// db runs statements on the agent directory's database between processes.
func (h *restartHost) db(t *testing.T, query string, args ...any) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(h.dir, ".nanite", "nanite.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatal(err)
	}
}

// subtaskJobs is each subtask job's ID and output path by its goal's
// first line, read between processes.
func (h *restartHost) subtaskJobs(t *testing.T) map[string][2]string {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(h.dir, ".nanite", "nanite.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	rows, err := db.QueryContext(t.Context(), `SELECT id,spec_json,COALESCE(output_path,'') FROM jobs WHERE kind='subagent'`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string][2]string{}
	for rows.Next() {
		var id, spec, path string
		if err := rows.Scan(&id, &spec, &path); err != nil {
			t.Fatal(err)
		}
		var v struct{ Goal string }
		if err := json.Unmarshal([]byte(spec), &v); err != nil {
			t.Fatal(err)
		}
		goal, _, _ := strings.Cut(v.Goal, "\n")
		out[goal] = [2]string{id, path}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// editState rewrites the chat's saved desk state between processes.
func (h *restartHost) editState(t *testing.T, edit func(map[string]any)) {
	t.Helper()
	a, err := agent.Open(t.Context(), agent.Options{Dir: h.dir, Provider: &brain{}, GlobalSkillsDir: "-"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	c, err := a.Conversation(t.Context(), chatrunner.ConversationID(restartChat))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := c.HostState(t.Context(), "desk")
	if err != nil || raw == "" {
		t.Fatalf("no saved state: %v", err)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatal(err)
	}
	edit(v)
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetHostState(t.Context(), "desk", string(b)); err != nil {
		t.Fatal(err)
	}
}

// stallBoth runs a plan of two stalled jobs in a first process and closes
// it while they run.
func (h *restartHost) stallBoth(t *testing.T, opts chatrunner.DeskOptions) {
	t.Helper()
	first := h.open(t, &brain{hang: true}, opts)
	turn(t, first, twoSteps)
	waitFor(t, "both jobs running", func() bool {
		st := workStates(first)
		return len(st) == 2 && st[0] == desk.WorkRunning && st[1] == desk.WorkRunning
	})
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if got := h.notices(); got != "" {
		t.Fatalf("the first process delivered: %s", got)
	}
}

func slowPlan() chatrunner.DeskOptions {
	plan := desk.Plan{Confidence: 0.95, Items: []desk.PlanItem{
		{ID: "a", Kind: desk.KindResearch, Goal: "slow:alpha-result"},
		{ID: "b", Kind: desk.KindResearch, Goal: "slow:beta-result"},
	}}
	return chatrunner.DeskOptions{Intake: desk.Intake{Mode: desk.IntakeAuto, Planner: fixedPlanner{plan}}, Policy: mergePolicy(100 * time.Millisecond)}
}

func workStates(r *chatrunner.Runner) []desk.WorkState {
	var out []desk.WorkState
	for _, topic := range r.Desk().Ledger().List(nil) {
		for _, w := range topic.Work {
			out = append(out, w.State)
		}
	}
	return out
}

// Jobs a restart stopped are started again by the next process, and their
// results reach the owner once. The ledger the desk owns comes back too.
func TestDeskRestartsJobsARestartStopped(t *testing.T) {
	h := newRestartHost(t)
	plan := desk.Plan{Confidence: 0.95, Items: []desk.PlanItem{
		{ID: "a", Kind: desk.KindResearch, Goal: "slow:alpha-result"},
		{ID: "b", Kind: desk.KindResearch, Goal: "slow:beta-result"},
	}}
	opts := chatrunner.DeskOptions{Intake: desk.Intake{Mode: desk.IntakeAuto, Planner: fixedPlanner{plan}}, Policy: mergePolicy(100 * time.Millisecond)}

	first := h.open(t, &brain{hang: true}, opts)
	turn(t, first, twoSteps)
	waitFor(t, "both jobs running", func() bool {
		st := workStates(first)
		return len(st) == 2 && st[0] == desk.WorkRunning && st[1] == desk.WorkRunning
	})
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if got := h.notices(); got != "" {
		t.Fatalf("the first process delivered: %s", got)
	}

	second := h.open(t, &brain{}, opts)
	if topics := second.Desk().Ledger().List(nil); len(topics) != 1 {
		t.Fatalf("the ledger did not come back: %+v", topics)
	}
	h.waitNotices(t, "the restarted jobs' results", "alpha-result", "beta-result")
	waitFor(t, "the work recorded done", func() bool {
		st := workStates(second)
		return len(st) == 2 && st[0] == desk.WorkDone && st[1] == desk.WorkDone
	})
	h.once(t, second, ": alpha-result", ": beta-result")
}

// A suggested plan survives a restart and can be accepted afterwards.
func TestDeskSuggestedPlanSurvivesARestart(t *testing.T) {
	h := newRestartHost(t)
	plan := desk.Plan{Confidence: 0.5, Items: []desk.PlanItem{
		{ID: "a", Kind: desk.KindResearch, Goal: "report:alpha-result"},
		{ID: "b", Kind: desk.KindResearch, Goal: "report:beta-result"},
	}}
	opts := chatrunner.DeskOptions{Intake: desk.Intake{Mode: desk.IntakeSuggest, Planner: fixedPlanner{plan}}, Policy: mergePolicy(100 * time.Millisecond)}

	first := h.open(t, &brain{}, opts)
	var id string
	for _, ev := range turn(t, first, twoSteps) {
		if ev.Verb == chatstream.VerbActivity && ev.Kind == chatrunner.ActivityDeskPlan {
			id, _ = activityValue(t, ev)["id"].(string)
		}
	}
	if id == "" {
		t.Fatal("no suggestion")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second := h.open(t, &brain{}, opts)
	if err := second.Desk().AcceptPlan(t.Context(), restartChat, id, nil); err != nil {
		t.Fatalf("accept after a restart: %v", err)
	}
	h.waitNotices(t, "the accepted plan's results", "alpha-result", "beta-result")
	if err := second.Desk().AcceptPlan(t.Context(), restartChat, id, nil); err == nil {
		t.Fatal("a plan was accepted twice")
	}
	h.once(t, second, ": alpha-result", ": beta-result")
}

// What the delivery editor held when the process stopped is delivered by
// the next one, once.
func TestDeskHeldDeliverySurvivesARestart(t *testing.T) {
	h := newRestartHost(t)
	plan := desk.Plan{Confidence: 0.95, Items: []desk.PlanItem{
		{ID: "a", Kind: desk.KindResearch, Goal: "report:alpha-result"},
		{ID: "b", Kind: desk.KindResearch, Goal: "report:beta-result"},
	}}
	opts := chatrunner.DeskOptions{Intake: desk.Intake{Mode: desk.IntakeAuto, Planner: fixedPlanner{plan}}, Policy: mergePolicy(100 * time.Millisecond)}

	first := h.open(t, &brain{}, opts)
	first.Desk().SetOwnerTyping(restartChat, true) // the owner is typing: hold everything
	turn(t, first, twoSteps)
	waitFor(t, "both results held", func() bool {
		n := 0
		for _, c := range first.Desk().Held(restartChat) {
			n += c
		}
		return n == 2
	})
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if got := h.notices(); got != "" {
		t.Fatalf("the first process delivered while the owner typed: %s", got)
	}

	second := h.open(t, &brain{}, opts)
	h.waitNotices(t, "the held results", "alpha-result", "beta-result")
	h.once(t, second, ": alpha-result", ": beta-result")
}

// A step saved as running before its job was recorded (a save between the
// two) goes back to planned on restart and runs, rather than staying stuck.
func TestDeskRestartsAStepSavedWithoutItsJob(t *testing.T) {
	h := newRestartHost(t)
	opts := slowPlan()
	h.stallBoth(t, opts)
	h.editState(t, func(v map[string]any) {
		plan := v["plans"].([]any)[0].(map[string]any)
		delete(plan, "refs") // running, with no job: the save landed in the window
	})
	second := h.open(t, &brain{}, opts)
	h.waitNotices(t, "the steps' results", "alpha-result", "beta-result")
	h.once(t, second, ": alpha-result", ": beta-result")
}

// A job the process exited under (the next start marks it lost) is
// started again.
func TestDeskRestartsAJobLostToAProcessExit(t *testing.T) {
	h := newRestartHost(t)
	opts := slowPlan()
	h.stallBoth(t, opts)
	// As if the process had exited: the jobs are still marked running.
	h.db(t, `UPDATE jobs SET status='running', reason='', ended_at=NULL WHERE kind='subagent'`)
	second := h.open(t, &brain{}, opts)
	h.waitNotices(t, "the restarted jobs' results", "alpha-result", "beta-result")
	h.once(t, second, ": alpha-result", ": beta-result")
}

// A job that finished while no process ran the desk has its result
// applied on restart, and is not run again.
func TestDeskAppliesAJobThatFinishedWhileDown(t *testing.T) {
	h := newRestartHost(t)
	opts := slowPlan()
	h.stallBoth(t, opts)
	for goal, job := range h.subtaskJobs(t) {
		out := filepath.Join(t.TempDir(), "out.json")
		result, _ := json.Marshal(map[string]string{"result": "finished-" + strings.TrimPrefix(goal, "slow:")})
		if err := os.WriteFile(out, result, 0600); err != nil {
			t.Fatal(err)
		}
		h.db(t, `UPDATE jobs SET status='succeeded', reason='', output_path=? WHERE id=?`, out, job[0])
	}
	second := h.open(t, &brain{hang: true}, opts) // a job run again would never report
	h.waitNotices(t, "the finished jobs' results", "finished-alpha-result", "finished-beta-result")
	h.once(t, second, ": finished-alpha-result", ": finished-beta-result")
}

// A chat's state saved by a newer version is neither read nor overwritten.
func TestDeskLeavesNewerStateAlone(t *testing.T) {
	h := newRestartHost(t)
	plan := desk.Plan{Confidence: 0.95, Items: []desk.PlanItem{
		{ID: "a", Kind: desk.KindResearch, Goal: "report:alpha-result"},
		{ID: "b", Kind: desk.KindResearch, Goal: "report:beta-result"},
	}}
	opts := chatrunner.DeskOptions{Intake: desk.Intake{Mode: desk.IntakeAuto, Planner: fixedPlanner{plan}}, Policy: mergePolicy(100 * time.Millisecond)}
	first := h.open(t, &brain{}, opts)
	turn(t, first, "hello")
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	newer := `{"version":99,"chat_id":"` + restartChat + `","from":"the future"}`
	a, err := agent.Open(t.Context(), agent.Options{Dir: h.dir, Provider: &brain{}, GlobalSkillsDir: "-"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := a.Conversation(t.Context(), chatrunner.ConversationID(restartChat))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetHostState(t.Context(), "desk", newer); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}

	second := h.open(t, &brain{}, opts)
	turn(t, second, twoSteps)
	h.waitNotices(t, "the plan's results", "alpha-result", "beta-result")
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	h.editState(t, func(v map[string]any) {
		if v["from"] != "the future" || v["version"] != float64(99) {
			t.Fatalf("the newer state was overwritten: %v", v)
		}
	})
}
