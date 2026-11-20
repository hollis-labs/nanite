package engine

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/libs/agent/steer"
	"github.com/hollis-labs/libs/agent/trace"
	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/events"
	"github.com/hollis-labs/nanite/internal/provider/fake"
	"github.com/hollis-labs/nanite/internal/slots"
	"github.com/hollis-labs/nanite/internal/store"
)

var (
	notesSlot = steer.Slot{Name: "notes", About: "notes", Module: "notes", Views: []string{"notes"}, Tools: []string{"notes_read"}, Instructions: "Keep notes short."}
	mailSlot  = steer.Slot{Name: "mail", About: "the inbox", Module: "mail", Tools: []string{"mail_list"}}
)

// steeringFor configures steering on an engine from setupEngine.
func steeringFor(t *testing.T, e *Engine, reflexes []steer.Reflex, classify func(context.Context, steer.Situation, []steer.Slot) (steer.Classification, error)) *steer.Registry {
	t.Helper()
	reg, err := steer.NewRegistry(steer.DefaultPresets()...)
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Add(steer.LayerApp, "", steer.Preset{Name: "focus", Instructions: "Be brief.", Reflexes: []string{"hush"}, Slots: []string{"mail"}}); err != nil {
		t.Fatal(err)
	}
	e.cfg.Steering = &Steering{Registry: reg, Slots: func() []steer.Slot { return []steer.Slot{notesSlot, mailSlot} },
		Reflexes: func() []steer.Reflex { return reflexes }, Classify: classify}
	return reg
}

// steerRun sends a message and returns the Steered event and the run's text.
func steerRun(t *testing.T, e *Engine, id, text string, sit *steer.Situation) (events.Steered, string) {
	t.Helper()
	run, err := e.Send(t.Context(), id, Input{Text: text, Situation: sit})
	if err != nil {
		t.Fatal(err)
	}
	var got events.Steered
	var seen bool
	for ev := range run.Events() {
		if s, ok := ev.(events.Steered); ok {
			got, seen = s, true
		}
	}
	res, err := run.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Fatal("no Steered event")
	}
	return got, res.Text
}

func lastUserText(req llmtypes.ChatRequest) string {
	m := req.Messages[len(req.Messages)-1]
	var b strings.Builder
	for _, c := range m.ContentBlocks {
		b.WriteString(c.Text + "\n")
	}
	return b.String()
}

func systemText(req llmtypes.ChatRequest) string {
	var b strings.Builder
	for _, s := range req.SlotBlocks {
		b.WriteString(s.Content + "\n")
	}
	return b.String()
}

var hush = steer.Reflex{Name: "hush", TriggerKind: steer.TriggerText, Trigger: json.RawMessage(`{"pattern":"."}`),
	ActionKind: steer.ActionRemind, Action: json.RawMessage(`{"body":"hold non-urgent updates"}`)}

// R6 acceptance: a deliberate mode activates its reflexes and instructions.
func TestDeliberateModeSteersTheTurn(t *testing.T) {
	p := &watchedProvider{Provider: fake.New(fake.Text("one"), fake.Text("two"))}
	e, _, id := setupEngine(t, p, "")
	steeringFor(t, e, []steer.Reflex{hush}, nil)
	in := &steer.Situation{Focused: &steer.Focus{View: "notes"}}

	got, _ := steerRun(t, e, id, "what next?", in)
	if got.Mode != "general" || got.ModeSource != steer.SourceDefault || len(got.Fired) != 0 {
		t.Fatalf("before the mode: %+v", got)
	}
	if strings.Contains(systemText(p.requests[0]), "Be brief.") || strings.Contains(lastUserText(p.requests[0]), "hold non-urgent") {
		t.Fatal("the mode's instructions or reflex applied before it was set")
	}
	if !strings.Contains(lastUserText(p.requests[0]), "Work in front: notes (notes). Keep notes short.") {
		t.Fatalf("work slot not told: %s", lastUserText(p.requests[0]))
	}

	if err := e.SetMode(t.Context(), id, "focus"); err != nil {
		t.Fatal(err)
	}
	got, _ = steerRun(t, e, id, "and now?", in)
	if got.Mode != "focus" || got.ModeSource != steer.SourceDeliberate || !slices.Equal(got.Fired, []string{"hush"}) || !slices.Equal(got.Slots, []string{"notes", "mail"}) {
		t.Fatalf("with the mode: %+v", got)
	}
	if !strings.Contains(systemText(p.requests[1]), "Mode: focus. Be brief.") {
		t.Fatalf("mode instructions not in the system blocks:\n%s", systemText(p.requests[1]))
	}
	user := lastUserText(p.requests[1])
	if !strings.Contains(user, "- hold non-urgent updates") || !strings.Contains(user, "Also active: mail") || !strings.Contains(user, "Top of mind: notes_read, mail_list.") {
		t.Fatalf("turn context: %s", user)
	}
}

// R6 acceptance: a classified suggestion surfaces but does not apply in suggest mode.
func TestClassifiedSuggestionIsNotApplied(t *testing.T) {
	p := &watchedProvider{Provider: fake.New(fake.Text("ok"), fake.Text("ok"))}
	e, _, id := setupEngine(t, p, "")
	classify := func(context.Context, steer.Situation, []steer.Slot) (steer.Classification, error) {
		return steer.Classification{steer.FacetPreset: {Label: "focus", P: 0.95}, steer.FacetSlot: {Label: "mail", P: 0.9}}, nil
	}
	steeringFor(t, e, nil, classify)
	var sunk []trace.Turn
	e.cfg.TraceSink = func(_ context.Context, rec trace.Turn) { sunk = append(sunk, rec) }

	got, _ := steerRun(t, e, id, "go through my inbox", &steer.Situation{Focused: &steer.Focus{View: "notes"}})
	if got.Mode != "general" || got.Slots[0] != "notes" {
		t.Fatalf("a suggestion was applied: %+v", got)
	}
	if len(got.Suggestions) != 2 || got.Suggestions[0].Label != "focus" || got.Suggestions[1].Label != "mail" {
		t.Fatalf("suggestions: %+v", got.Suggestions)
	}
	if strings.Contains(systemText(p.requests[0]), "Be brief.") {
		t.Fatal("the suggested preset's instructions were applied")
	}
	// The trace records the situation, the classification and the selector's reason.
	rec := sunk[0]
	if rec.Why != "focus" || !strings.Contains(string(rec.Classified), `"suggest"`) || !strings.Contains(string(rec.Situation), `"mode_source":"default"`) {
		t.Fatalf("trace: why=%s classified=%s situation=%s", rec.Why, rec.Classified, rec.Situation)
	}
	if err := rec.Validate(); err != nil {
		t.Fatal(err)
	}
	// The owner accepts the preset suggestion: it becomes the deliberate mode.
	if err := e.AcceptSuggestion(t.Context(), id, got.Suggestions[0], "notes"); err != nil {
		t.Fatal(err)
	}
	if got, _ := steerRun(t, e, id, "next", nil); got.Mode != "focus" || got.ModeSource != steer.SourceDeliberate {
		t.Fatalf("after accepting: %+v", got)
	}
}

// R6 acceptance: a module-contributed preset loads and can be set.
func TestModulePresetCanBeSet(t *testing.T) {
	p := &watchedProvider{Provider: fake.New(fake.Text("ok"))}
	e, _, id := setupEngine(t, p, "")
	reg := steeringFor(t, e, nil, nil)
	if err := reg.Add(steer.LayerModule, "drafts", steer.Preset{Name: "writing", Instructions: "Help the owner write.", OutputStyle: "detailed"}); err != nil {
		t.Fatal(err)
	}
	if err := e.SetMode(t.Context(), id, "writing"); err != nil {
		t.Fatal(err)
	}
	got, _ := steerRun(t, e, id, "start the intro", nil)
	if got.Mode != "writing" || !strings.Contains(systemText(p.requests[0]), "Mode: writing. Help the owner write. Output style: detailed.") {
		t.Fatalf("module preset: %+v\n%s", got, systemText(p.requests[0]))
	}
}

func TestModeIsKeptAndOnlyForRoots(t *testing.T) {
	p := &watchedProvider{Provider: fake.New()}
	e, db, id := setupEngine(t, p, "")
	steeringFor(t, e, nil, nil)
	if err := e.SetMode(t.Context(), id, "nope"); !errors.Is(err, steer.ErrUnknownPreset) {
		t.Fatalf("unknown mode: %v", err)
	}
	if err := e.SetMode(t.Context(), id, "focus"); err != nil {
		t.Fatal(err)
	}
	// A new engine over the same store reads the mode back.
	e.steerers = map[string]*steering{}
	if mode, err := e.Mode(t.Context(), id); err != nil || mode != "focus" {
		t.Fatalf("kept mode: %q %v", mode, err)
	}
	child := store.NewID(time.Now().UTC())
	if err := db.CreateConversation(t.Context(), store.Conversation{ID: child, ParentID: id, Role: "worker", Status: "active", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := e.SetMode(t.Context(), child, "focus"); err == nil {
		t.Fatal("a subtask conversation got a mode")
	}
	if err := e.SetMode(t.Context(), id, ""); err != nil {
		t.Fatal(err)
	}
	if v, _ := db.Setting(t.Context(), id, settingMode); v != "" {
		t.Fatalf("cleared mode still stored: %q", v)
	}
	e.cfg.Steering = nil
	if err := e.SetMode(t.Context(), id, "focus"); !errors.Is(err, ErrNoSteering) {
		t.Fatalf("without steering: %v", err)
	}
}

func TestReflexHaltAnswersWithoutTheModel(t *testing.T) {
	p := &watchedProvider{Provider: fake.New()}
	e, db, id := setupEngine(t, p, "")
	stop := steer.Reflex{Name: "stop", TriggerKind: steer.TriggerText, Trigger: json.RawMessage(`{"pattern":"^/stop"}`), Enabled: true,
		ActionKind: steer.ActionHalt, Action: json.RawMessage(`{"message":"Stopped."}`)}
	bad := steer.Reflex{Name: "bad", TriggerKind: "cron", Enabled: true}
	steeringFor(t, e, []steer.Reflex{bad, stop}, nil)
	got, text := steerRun(t, e, id, "/stop", nil)
	if !got.Halted || text != "Stopped." || len(p.requests) != 0 {
		t.Fatalf("halt: %+v text=%q requests=%d", got, text, len(p.requests))
	}
	history, err := db.History(t.Context(), id)
	if err != nil || len(history) != 2 || history[1].Role != "assistant" || history[1].Blocks[0].Text != "Stopped." {
		t.Fatalf("history: %+v %v", history, err)
	}
}

func TestNoticeTurnGetsOnlyTheMode(t *testing.T) {
	p := &watchedProvider{Provider: fake.New(fake.Text("ok"))}
	e, _, id := setupEngine(t, p, "")
	called := false
	steeringFor(t, e, []steer.Reflex{hush}, func(context.Context, steer.Situation, []steer.Slot) (steer.Classification, error) {
		called = true
		return nil, nil
	})
	if err := e.SetMode(t.Context(), id, "focus"); err != nil {
		t.Fatal(err)
	}
	if err := e.PostNotice(t.Context(), id, "job", "done"); err != nil {
		t.Fatal(err)
	}
	run, err := e.Send(t.Context(), id, Input{Source: SourceNotice})
	if err != nil {
		t.Fatal(err)
	}
	var got events.Steered
	for ev := range run.Events() {
		if s, ok := ev.(events.Steered); ok {
			got = s
		}
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	if called || len(got.Fired) != 0 || got.Mode != "focus" {
		t.Fatalf("notice turn: classified=%v %+v", called, got)
	}
	if !strings.Contains(systemText(p.requests[0]), "Mode: focus.") {
		t.Fatal("the mode did not apply to the notice turn")
	}
}

func TestNoSteeringLeavesTheTurnAlone(t *testing.T) {
	p := &watchedProvider{Provider: fake.New(fake.Text("ok"))}
	e, _, id := setupEngine(t, p, "")
	var sunk []trace.Turn
	e.cfg.TraceSink = func(_ context.Context, rec trace.Turn) { sunk = append(sunk, rec) }
	run, err := e.Send(t.Context(), id, Input{Text: "hi", Slots: []slots.Update{}})
	if err != nil {
		t.Fatal(err)
	}
	for ev := range run.Events() {
		if _, ok := ev.(events.Steered); ok {
			t.Fatal("a Steered event without steering")
		}
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	if sunk[0].Fired != nil || sunk[0].Situation != nil {
		t.Fatalf("trace fields set without steering: %+v", sunk[0])
	}
}

// A reflex that fired is still cooling down after a restart, and the trace
// records no message text.
func TestCooldownsAreKeptAndTheTraceHasNoPrompt(t *testing.T) {
	p := &watchedProvider{Provider: fake.New(fake.Text("one"), fake.Text("two"))}
	e, _, id := setupEngine(t, p, "")
	once := steer.Reflex{Name: "once", TriggerKind: steer.TriggerText, Trigger: json.RawMessage(`{"pattern":"."}`), Enabled: true, Cooldown: 3600,
		ActionKind: steer.ActionRemind, Action: json.RawMessage(`{"body":"said once"}`)}
	steeringFor(t, e, []steer.Reflex{once}, nil)
	var sunk []trace.Turn
	e.cfg.TraceSink = func(_ context.Context, rec trace.Turn) { sunk = append(sunk, rec) }

	got, _ := steerRun(t, e, id, "SECRET-WORDS here", &steer.Situation{Focused: &steer.Focus{View: "notes", Title: "SECRET-TITLE"}})
	if !slices.Equal(got.Fired, []string{"once"}) {
		t.Fatalf("first turn: %+v", got)
	}
	if strings.Contains(string(sunk[0].Situation), "SECRET") || !strings.Contains(string(sunk[0].Situation), `"view":"notes"`) {
		t.Fatalf("trace situation: %s", sunk[0].Situation)
	}
	e.steerers = map[string]*steering{} // as after a restart
	got, _ = steerRun(t, e, id, "again", nil)
	if len(got.Fired) != 0 {
		t.Fatalf("a restart re-fired a cooling reflex: %+v", got)
	}
}
