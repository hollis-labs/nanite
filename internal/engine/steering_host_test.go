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
	"github.com/hollis-labs/nanite/internal/events"
	"github.com/hollis-labs/nanite/internal/provider/fake"
	"github.com/hollis-labs/nanite/internal/store"
)

// always fires on every person's message.
var always = steer.Reflex{Name: "always", TriggerKind: steer.TriggerText, Trigger: json.RawMessage(`{"pattern":"."}`), Enabled: true,
	ActionKind: steer.ActionRemind, Action: json.RawMessage(`{"body":"ALWAYS-REMINDER"}`)}

// eventRemind fires on the mail.new topic and below it.
var eventRemind = steer.Reflex{Name: "mail-arrived", TriggerKind: steer.TriggerEvent, Trigger: json.RawMessage(`{"topic":"mail.new"}`), Enabled: true,
	ActionKind: steer.ActionRemind, Action: json.RawMessage(`{"body":"NEW-MAIL-REMINDER"}`)}

// requestText is everything a model request carries: after the context
// placement changes, the turn's work context sits in the user message.
func requestText(t *testing.T, req any) string {
	t.Helper()
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// drain sends input and returns the run's Steered events and its text.
func drain(t *testing.T, e *Engine, id string, in Input) ([]events.Steered, string) {
	t.Helper()
	run, err := e.Send(t.Context(), id, in)
	if err != nil {
		t.Fatal(err)
	}
	var got []events.Steered
	for ev := range run.Events() {
		if s, ok := ev.(events.Steered); ok {
			got = append(got, s)
		}
	}
	res, err := run.Wait()
	if err != nil {
		t.Fatal(err)
	}
	return got, res.Text
}

// Parity gap 6: a turn the host or a module sends is steered like a notice
// turn (mode and work slots, no classifier, no reflexes), and its trace says
// where it came from.
func TestHostTurnsSkipTheClassifierAndReflexes(t *testing.T) {
	errand, err := ErrandSource("mail")
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{SourceAuto, errand} {
		t.Run(source, func(t *testing.T) {
			p := &watchedProvider{Provider: fake.New(fake.Text("ok"))}
			e, _, id := setupEngine(t, p, "")
			called := false
			steeringFor(t, e, []steer.Reflex{always}, func(context.Context, steer.Situation, []steer.Slot) (steer.Classification, error) {
				called = true
				return nil, nil
			})
			if err := e.SetMode(t.Context(), id, "focus"); err != nil {
				t.Fatal(err)
			}
			var sunk []trace.Turn
			e.cfg.TraceSink = func(_ context.Context, rec trace.Turn) { sunk = append(sunk, rec) }
			got, _ := drain(t, e, id, Input{Text: "do the errand", Source: source})
			if called || len(got) != 1 || len(got[0].Fired) != 0 || got[0].Mode != "focus" {
				t.Fatalf("host turn steering: classified=%v %+v", called, got)
			}
			if strings.Contains(requestText(t, p.requests[0]), "ALWAYS-REMINDER") || !strings.Contains(systemText(p.requests[0]), "Mode: focus.") {
				t.Fatalf("host turn context: %s", requestText(t, p.requests[0]))
			}
			if !strings.HasSuffix(lastUserText(p.requests[0]), "do the errand\n") {
				t.Fatalf("host turn text: %q", lastUserText(p.requests[0]))
			}
			if sunk[0].Source != trace.SourceHost || !strings.Contains(string(sunk[0].Situation), `"source":"`+source+`"`) || sunk[0].Fired != nil {
				t.Fatalf("trace: source=%s situation=%s fired=%v", sunk[0].Source, sunk[0].Situation, sunk[0].Fired)
			}
		})
	}
}

// Without steering the trace still records the source.
func TestHostTurnSourceIsTracedWithoutSteering(t *testing.T) {
	p := &watchedProvider{Provider: fake.New(fake.Text("ok"))}
	e, _, id := setupEngine(t, p, "")
	var sunk []trace.Turn
	e.cfg.TraceSink = func(_ context.Context, rec trace.Turn) { sunk = append(sunk, rec) }
	if _, _ = drain(t, e, id, Input{Text: "late answer", Source: "errand:late-answer"}); sunk[0].Source != trace.SourceHost ||
		string(sunk[0].Situation) != `{"source":"errand:late-answer"}` {
		t.Fatalf("trace: source=%s situation=%s", sunk[0].Source, sunk[0].Situation)
	}
}

func TestTurnSourcesAreValidated(t *testing.T) {
	e, _, id := setupEngine(t, &watchedProvider{Provider: fake.New()}, "")
	for _, in := range []Input{
		{Text: "x", Source: "errand:"},
		{Text: "x", Source: "errand:-bad"},
		{Text: "x", Source: "errand:has space"},
		{Text: "x", Source: "errand:" + strings.Repeat("m", 65)},
		{Text: "  ", Source: SourceAuto},
		{Text: "", Source: "errand:mail"},
		{Text: "x", Source: "schedule"},
	} {
		if _, err := e.Send(t.Context(), id, in); err == nil {
			t.Errorf("Send accepted %+v", in)
		}
	}
	if _, err := ErrandSource("late-answer.v2_x"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Steer(t.Context(), id, Input{Text: "x", Source: SourceAuto}); err == nil {
		t.Fatal("Steer accepted a host turn")
	}
}

// Parity gap 7: an event reflex's reminder waits for the next person's
// message, even when a host or notice turn runs first.
func TestEventReminderWaitsForThePersonsMessage(t *testing.T) {
	p := &watchedProvider{Provider: fake.New(fake.Text("one"), fake.Text("two"), fake.Text("three"))}
	e, _, id := setupEngine(t, p, "")
	steeringFor(t, e, []steer.Reflex{eventRemind}, nil)
	fired, err := e.SteerEvent(t.Context(), id, "mail.new.urgent")
	if err != nil || !slices.Equal(fired.Fired, []string{"mail-arrived"}) || len(fired.Actions) != 0 {
		t.Fatalf("SteerEvent: %+v %v", fired, err)
	}
	drain(t, e, id, Input{Text: "an errand", Source: "errand:mail"})
	if err := e.PostNotice(t.Context(), id, "job", "done"); err != nil {
		t.Fatal(err)
	}
	drain(t, e, id, Input{Source: SourceNotice})
	for i, req := range p.requests[:2] {
		if strings.Contains(requestText(t, req), "NEW-MAIL-REMINDER") {
			t.Fatalf("turn %d (not a person's) got the reminder", i)
		}
	}
	drain(t, e, id, Input{Text: "hello"})
	if !strings.Contains(requestText(t, p.requests[2]), "NEW-MAIL-REMINDER") {
		t.Fatal("the person's turn lost the reminder")
	}
	// Once only.
	p.Provider = fake.New(fake.Text("four"))
	drain(t, e, id, Input{Text: "again"})
	if strings.Contains(lastUserText(p.requests[3]), "NEW-MAIL-REMINDER") || strings.Contains(systemText(p.requests[3]), "NEW-MAIL-REMINDER") {
		t.Fatal("the reminder came twice")
	}
}

// Event and focus reflexes run in the mode in force, return host actions,
// and keep their cooldowns.
func TestEventAndFocusReflexesFollowTheMode(t *testing.T) {
	p := &watchedProvider{Provider: fake.New()}
	e, _, id := setupEngine(t, p, "")
	vocab := steer.NewVocabulary()
	if err := vocab.Register("save_state", nil); err != nil {
		t.Fatal(err)
	}
	save := steer.Reflex{Name: "hush", TriggerKind: steer.TriggerFocus, Trigger: json.RawMessage(`{"view":"notes"}`), Cooldown: 3600,
		ActionKind: "save_state", Action: json.RawMessage(`{"where":"notes"}`)}
	// steer refuses a halt on an event (there is no message to answer):
	// it is skipped with a warning and never fires.
	halt := steer.Reflex{Name: "halts", TriggerKind: steer.TriggerEvent, Trigger: json.RawMessage(`{"topic":"x"}`), Enabled: true,
		ActionKind: steer.ActionHalt, Action: json.RawMessage(`{"message":"no"}`)}
	steeringFor(t, e, []steer.Reflex{save, halt}, nil)
	e.cfg.Steering.Vocabulary = vocab

	// "hush" is active only in the focus preset.
	if got, err := e.SteerFocus(t.Context(), id, "notes"); err != nil || len(got.Fired) != 0 {
		t.Fatalf("fired outside its mode: %+v %v", got, err)
	}
	if err := e.SetMode(t.Context(), id, "focus"); err != nil {
		t.Fatal(err)
	}
	got, err := e.SteerFocus(t.Context(), id, "notes")
	if err != nil || len(got.Actions) != 1 || got.Actions[0].Kind != "save_state" || string(got.Actions[0].Spec) != `{"where":"notes"}` {
		t.Fatalf("focus action: %+v %v", got, err)
	}
	e.steerers = map[string]*steering{} // as after a restart
	if got, err := e.SteerFocus(t.Context(), id, "notes"); err != nil || len(got.Fired) != 0 {
		t.Fatalf("a restart re-fired a cooling reflex: %+v %v", got, err)
	}
	if got, err := e.SteerFocus(t.Context(), id, "mail"); err != nil || len(got.Fired) != 0 {
		t.Fatalf("another view fired it: %+v %v", got, err)
	}
	if got, err := e.SteerEvent(t.Context(), id, "x"); err != nil || len(got.Fired) != 0 {
		t.Fatalf("halt on an event: %+v %v", got, err)
	}
	if _, err := e.SteerEvent(t.Context(), id, " "); err == nil {
		t.Fatal("an empty topic was accepted")
	}
	if _, err := e.SteerFocus(t.Context(), id, ""); err == nil {
		t.Fatal("an empty view was accepted")
	}
}

func TestSteerEventNeedsSteeringAndARoot(t *testing.T) {
	e, db, id := setupEngine(t, &watchedProvider{Provider: fake.New()}, "")
	if _, err := e.SteerEvent(t.Context(), id, "a"); !errors.Is(err, ErrNoSteering) {
		t.Fatalf("without steering: %v", err)
	}
	steeringFor(t, e, nil, nil)
	child := store.NewID(time.Now().UTC())
	if err := db.CreateConversation(t.Context(), store.Conversation{ID: child, ParentID: id, Role: "worker", Status: "active", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.SteerFocus(t.Context(), child, "notes"); err == nil {
		t.Fatal("a subtask's conversation was steered")
	}
}

// Nanite posts its own compaction as an event: a reflex on it fires during
// the turn (its action reaches the host as a Steered event with Event set),
// and its reminder reaches the next person's message.
func TestCompactionPostsAnEvent(t *testing.T) {
	p := overflowThen(fake.Text("after compaction"), fake.Text("next"))
	e, db, id := setupEngine(t, p, "")
	seedLongHistory(t, db, id)
	vocab := steer.NewVocabulary()
	if err := vocab.Register("save_state", nil); err != nil {
		t.Fatal(err)
	}
	remind := steer.Reflex{Name: "remind-saved", TriggerKind: steer.TriggerEvent, Trigger: json.RawMessage(`{"topic":"nanite"}`), Enabled: true,
		ActionKind: steer.ActionRemind, Action: json.RawMessage(`{"body":"COMPACTED-REMINDER"}`)}
	save := steer.Reflex{Name: "save", TriggerKind: steer.TriggerEvent, Trigger: json.RawMessage(`{"topic":"nanite.compacted"}`), Enabled: true,
		ActionKind: "save_state", Action: json.RawMessage(`{}`)}
	steeringFor(t, e, []steer.Reflex{remind, save}, nil)
	e.cfg.Steering.Vocabulary = vocab

	got, _ := drain(t, e, id, Input{Text: "go"})
	var hosted *events.Steered
	for i := range got {
		if got[i].Event != "" {
			hosted = &got[i]
		}
	}
	if hosted == nil || hosted.Event != TopicCompacted || !slices.Equal(hosted.Fired, []string{"remind-saved", "save"}) ||
		len(hosted.Actions) != 1 || hosted.Actions[0].Kind != "save_state" {
		t.Fatalf("compaction event: %+v", got)
	}
	drain(t, e, id, Input{Text: "and now"})
	// The turn's work context (with its reminders) is in the user message.
	if !strings.Contains(lastUserText(p.requests[len(p.requests)-1]), "COMPACTED-REMINDER") {
		t.Fatal("the next person's turn lacks the reminder")
	}
}
