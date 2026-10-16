package reflexes

import (
	"errors"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

func TestRetiredEngineRefusesBeforeCollectionFiltersAndEffects(t *testing.T) {
	st := newReflexTestStore(t)
	ctx := t.Context()
	historical := &store.AgentProfile{ID: "private-retained-agent", Name: "Retained", Slug: "private-retained", SystemPrompt: "original body", Source: "internal"}
	if err := storetest.HistoricalProfile(ctx, st, historical); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.ExecContext(ctx, `INSERT INTO agent_reflexes(id,agent_id,name,trigger_kind,trigger_spec,action_kind,action_spec,created_by) VALUES('private-retained-rule',?,'Retained','event','{"name":"probe"}','inject_reminder','{"body":"retained"}','system')`, historical.ID); err != nil {
		t.Fatal(err)
	}
	var beforeEvents int
	if err := st.DB.QueryRowContext(ctx, `SELECT count(*) FROM event_log`).Scan(&beforeEvents); err != nil {
		t.Fatal(err)
	}
	hooks := &fakeReflexPluginHooks{}
	// Nil collector/executor make any attempt to use the retired path observable.
	engine := &Engine{Store: st, Plugins: hooks}
	for _, run := range []func() (AppliedActions, error){
		func() (AppliedActions, error) {
			return engine.Evaluate(ctx, "unknown-session", historical.ID, "advisor")
		},
		func() (AppliedActions, error) {
			return engine.EvaluateState(ctx, historical.ID, "advisor", State{Events: []EventSignal{{EventType: "probe"}}})
		},
	} {
		out, err := run()
		if !errors.Is(err, store.ErrImmutableAgentProfile) || len(out.Actions) != 0 || len(out.FiredReflexes) != 0 {
			t.Fatalf("out=%+v err=%v", out, err)
		}
	}
	if *hooks != (fakeReflexPluginHooks{}) {
		t.Fatalf("plugin effects=%+v", hooks)
	}
	var count int
	var body string
	var afterEvents int
	if err := st.DB.QueryRowContext(ctx, `SELECT fired_count,action_spec FROM agent_reflexes WHERE id='private-retained-rule'`).Scan(&count, &body); err != nil {
		t.Fatal(err)
	}
	if err := st.DB.QueryRowContext(ctx, `SELECT count(*) FROM event_log`).Scan(&afterEvents); err != nil {
		t.Fatal(err)
	}
	if count != 0 || body != `{"body":"retained"}` || afterEvents != beforeEvents {
		t.Fatalf("history changed fired=%d body=%s events=%d/%d", count, body, afterEvents, beforeEvents)
	}
}
