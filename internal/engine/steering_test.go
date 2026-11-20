package engine

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/libs/agent/core/reflexes"
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

// The mode and cooldowns are not in the transcripts, so rebuilding the index
// from them keeps them.
func TestModeSurvivesRebuildIndex(t *testing.T) {
	p := &watchedProvider{Provider: fake.New(fake.Text("ok"))}
	e, db, id := setupEngine(t, p, "")
	steeringFor(t, e, nil, nil)
	if _, text := steerRun(t, e, id, "hello", nil); text != "ok" {
		t.Fatalf("turn: %q", text)
	}
	if err := e.SetMode(t.Context(), id, "focus"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetSetting(t.Context(), id, settingCooldowns, `{}`); err != nil {
		t.Fatal(err)
	}
	if err := db.RebuildIndex(t.Context()); err != nil {
		t.Fatal(err)
	}
	e.steerers = map[string]*steering{}
	if mode, err := e.Mode(t.Context(), id); err != nil || mode != "focus" {
		t.Fatalf("mode after rebuild: %q %v", mode, err)
	}
	if v, err := db.Setting(t.Context(), id, settingCooldowns); err != nil || v != "{}" {
		t.Fatalf("cooldowns after rebuild: %q %v", v, err)
	}
}

// Owner changes to the mode are ordered with what the store keeps: after
// concurrent changes, the mode in memory is the mode on disk.
func TestConcurrentSetModeMatchesTheStore(t *testing.T) {
	p := &watchedProvider{Provider: fake.New()}
	e, db, id := setupEngine(t, p, "")
	reg := steeringFor(t, e, nil, nil)
	names := append(reg.Names(), "")
	st, err := e.steerer(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	accept := steer.Verdict{Facet: steer.FacetPreset, Outcome: steer.OutcomeSuggest}
	for round := range 200 {
		var wg sync.WaitGroup
		for g := range 5 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				name := names[(round+g)%len(names)]
				if g == 4 && name != "" {
					v := accept
					v.Label = name
					if err := e.AcceptSuggestion(t.Context(), id, v, ""); err != nil {
						t.Error(err)
					}
					return
				}
				if err := e.SetMode(t.Context(), id, name); err != nil {
					t.Error(err)
				}
			}()
		}
		wg.Wait()
		disk, err := db.Setting(t.Context(), id, settingMode)
		if err != nil {
			t.Fatal(err)
		}
		if mem := st.steer.Modes().Current(); mem != disk {
			t.Fatalf("round %d: memory %q, disk %q", round, mem, disk)
		}
	}
}

// A host steers a message ahead of its turn (the front desk does): a halt is
// seen before anything else acts, and the turn uses what was prepared
// without firing the reflex again. The halted turn's trace says it halted
// and names no model.
func TestSteerAheadHaltsOnceAndTheTraceSaysSo(t *testing.T) {
	p := &watchedProvider{Provider: fake.New()}
	e, _, id := setupEngine(t, p, "")
	stop := steer.Reflex{Name: "stop", TriggerKind: steer.TriggerText, Trigger: json.RawMessage(`{"pattern":"^/stop"}`), Enabled: true,
		ActionKind: steer.ActionHalt, Action: json.RawMessage(`{"message":"Stopped."}`)}
	steeringFor(t, e, []steer.Reflex{stop}, nil)
	var sunk []trace.Turn
	e.cfg.TraceSink = func(_ context.Context, rec trace.Turn) { sunk = append(sunk, rec) }

	prepared, err := e.Steer(t.Context(), id, Input{Text: "/stop"})
	if err != nil || !prepared.Halted() {
		t.Fatalf("prepared: halted=%v %v", prepared.Halted(), err)
	}
	if _, err := e.Steer(t.Context(), id, Input{Text: "x", Source: SourceNotice}); err == nil {
		t.Fatal("a notice was steered ahead")
	}
	if _, err := e.Send(t.Context(), id, Input{Text: "/other", Steer: prepared}); err == nil {
		t.Fatal("a turn used steering prepared for another message")
	}
	run, err := e.Send(t.Context(), id, Input{Text: "/stop", Steer: prepared})
	if err != nil {
		t.Fatal(err)
	}
	var got events.Steered
	for ev := range run.Events() {
		if s, ok := ev.(events.Steered); ok {
			got = s
		}
	}
	res, err := run.Wait()
	if err != nil || res.Text != "Stopped." || len(p.requests) != 0 || !got.Halted || !slices.Equal(got.Fired, []string{"stop"}) {
		t.Fatalf("halted turn: %+v %q %v requests=%d", got, res.Text, err, len(p.requests))
	}
	rec := sunk[len(sunk)-1]
	if rec.Provider != "" || rec.Model != "" || !strings.HasPrefix(rec.Why, "halted") || !slices.Equal(rec.Fired, []string{"stop"}) || rec.Outcome.Steps != 0 {
		t.Fatalf("halted trace: provider=%q model=%q why=%q fired=%v", rec.Provider, rec.Model, rec.Why, rec.Fired)
	}
	if again, err := e.Send(t.Context(), id, Input{Text: "/stop", Steer: prepared}); err == nil {
		if _, err = again.Wait(); err == nil {
			t.Fatal("prepared steering was used twice")
		}
	}
}

// A preset the classifier applied (auto) stays in force for the notice turn
// after it, which neither classifies nor runs reflexes; its trace has no
// classification and fired is null.
func TestNoticeTurnKeepsTheClassifiedPreset(t *testing.T) {
	p := &watchedProvider{Provider: fake.New(fake.Text("one"), fake.Text("two"))}
	e, _, id := setupEngine(t, p, "")
	calls := 0
	steeringFor(t, e, []steer.Reflex{hush}, func(context.Context, steer.Situation, []steer.Slot) (steer.Classification, error) {
		calls++
		return steer.Classification{steer.FacetPreset: {Label: "focus", P: 0.95}}, nil
	})
	e.cfg.Steering.Policy = steer.Policy{steer.FacetPreset: {Gate: steer.GateAuto, Threshold: 0.8}}
	var sunk []trace.Turn
	e.cfg.TraceSink = func(_ context.Context, rec trace.Turn) { sunk = append(sunk, rec) }
	if got, _ := steerRun(t, e, id, "heads down", nil); got.Mode != "focus" || got.ModeSource != steer.SourceClassified {
		t.Fatalf("person's turn: %+v", got)
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
	if got.Mode != "focus" || got.ModeSource != steer.SourceClassified || calls != 1 || got.Fired != nil || len(got.Suggestions) != 0 {
		t.Fatalf("notice turn: %+v calls=%d", got, calls)
	}
	if !strings.Contains(systemText(p.requests[1]), "Mode: focus.") {
		t.Fatal("the classified mode did not hold for the notice turn")
	}
	rec := sunk[len(sunk)-1]
	if rec.Fired != nil || rec.Classified != nil {
		t.Fatalf("notice trace: fired=%v classified=%s", rec.Fired, rec.Classified)
	}
	if err := rec.Validate(); err != nil {
		t.Fatal(err)
	}
}

// A classifier that does not answer in time is cut off; the turn goes on
// unclassified.
func TestClassifierIsTimedOut(t *testing.T) {
	defer func(d time.Duration) { classifyTimeout = d }(classifyTimeout)
	classifyTimeout = 50 * time.Millisecond
	p := &watchedProvider{Provider: fake.New(fake.Text("ok"))}
	e, _, id := setupEngine(t, p, "")
	steeringFor(t, e, nil, func(ctx context.Context, _ steer.Situation, _ []steer.Slot) (steer.Classification, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	start := time.Now()
	got, text := steerRun(t, e, id, "hello", nil)
	if text != "ok" || len(got.Suggestions) != 0 || time.Since(start) > 5*time.Second {
		t.Fatalf("timed-out classifier: %+v %q %v", got, text, time.Since(start))
	}
}

// A resumed turn gets the mode only: no reflexes, no classifier, no work slots.
func TestResumedTurnGetsOnlyTheMode(t *testing.T) {
	p := &watchedProvider{Provider: fake.New(fake.Text("recovered"))}
	e, db, id := setupEngine(t, p, "")
	called := false
	steeringFor(t, e, []steer.Reflex{hush}, func(context.Context, steer.Situation, []steer.Slot) (steer.Classification, error) {
		called = true
		return nil, nil
	})
	if err := e.SetMode(t.Context(), id, "focus"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	journal, err := db.OpenJournal(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	turn := store.Turn{ID: store.NewID(now), UserMessageID: store.NewID(now)}
	if err := journal.StartTurn(turn, now); err != nil {
		t.Fatal(err)
	}
	if err := journal.AppendMessage(store.Message{ID: turn.UserMessageID, TurnID: turn.ID, Role: "user", CreatedAt: now,
		Blocks: []llmtypes.ContentBlock{{Type: "text", Text: "hello"}}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := journal.InterruptTurn(turn.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	var sunk []trace.Turn
	e.cfg.TraceSink = func(_ context.Context, rec trace.Turn) { sunk = append(sunk, rec) }
	run, err := e.Resume(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	var got events.Steered
	for ev := range run.Events() {
		if s, ok := ev.(events.Steered); ok {
			got = s
		}
	}
	if res, err := run.Wait(); err != nil || res.Text != "recovered" {
		t.Fatalf("resume: %+v %v", res, err)
	}
	if called || got.Mode != "focus" || got.Fired != nil || got.Slots != nil {
		t.Fatalf("resumed turn: classified=%v %+v", called, got)
	}
	if !strings.Contains(systemText(p.requests[0]), "Mode: focus.") || strings.Contains(lastUserText(p.requests[0]), "Work in front") {
		t.Fatalf("resumed turn context:\n%s\n%s", systemText(p.requests[0]), lastUserText(p.requests[0]))
	}
	if sunk[0].Fired != nil {
		t.Fatalf("resumed trace fired: %v", sunk[0].Fired)
	}
}

type panickySelector struct{}

func (panickySelector) Select(context.Context, steer.Situation, []steer.Slot) steer.Selection {
	panic("selector")
}

// A panic in the host's steering hooks leaves the turn unsteered; it still
// runs.
func TestHostHookPanicsLeaveTheTurnUnsteered(t *testing.T) {
	for name, set := range map[string]func(*Steering){
		"slots":    func(s *Steering) { s.Slots = func() []steer.Slot { panic("slots") } },
		"reflexes": func(s *Steering) { s.Reflexes = func() []steer.Reflex { panic("reflexes") } },
		"selector": func(s *Steering) { s.NewSelector = func() steer.Selector { return panickySelector{} } },
		"new selector": func(s *Steering) {
			s.NewSelector = func() steer.Selector { panic("new selector") }
		},
		"state fill": func(s *Steering) {
			s.StateFill = func(*reflexes.State, steer.Situation) { panic("state fill") }
			s.Reflexes = func() []steer.Reflex {
				return []steer.Reflex{{Name: "busy", TriggerKind: steer.TriggerPredicate, Enabled: true,
					Trigger: json.RawMessage(`{"kind":"attr","key":"mode","value":"general"}`), ActionKind: steer.ActionRemind, Action: json.RawMessage(`{"body":"mail"}`)}}
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			p := &watchedProvider{Provider: fake.New(fake.Text("ok"))}
			e, _, id := setupEngine(t, p, "")
			steeringFor(t, e, nil, nil)
			set(e.cfg.Steering)
			run, err := e.Send(t.Context(), id, Input{Text: "hello"})
			if err != nil {
				t.Fatal(err)
			}
			for ev := range run.Events() {
				if _, ok := ev.(events.Steered); ok {
					t.Fatal("a Steered event after a panic")
				}
			}
			if res, err := run.Wait(); err != nil || res.Text != "ok" {
				t.Fatalf("turn: %+v %v", res, err)
			}
		})
	}
}

// An idle conversation's steering state is dropped; its mode comes back from
// the store.
func TestIdleSteerersAreEvicted(t *testing.T) {
	defer func(d time.Duration) { steererIdle = d }(steererIdle)
	p := &watchedProvider{Provider: fake.New()}
	e, _, id := setupEngine(t, p, "")
	steeringFor(t, e, nil, nil)
	if err := e.SetMode(t.Context(), id, "focus"); err != nil {
		t.Fatal(err)
	}
	steererIdle = 0
	time.Sleep(time.Millisecond)
	if _, err := e.steerer(t.Context(), "other"); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.steerers[id]; ok || len(e.steerers) != 1 {
		t.Fatalf("idle steerer kept: %d", len(e.steerers))
	}
	if mode, err := e.Mode(t.Context(), id); err != nil || mode != "focus" {
		t.Fatalf("mode after eviction: %q %v", mode, err)
	}
}
