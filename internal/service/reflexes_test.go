package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func newReflexTestService(t *testing.T) (*ReflexService, *store.Store, string) {
	t.Helper()
	st := newConfigTestStore(t)
	agent := &store.AgentProfile{Name: "Reflexes", Slug: "reflexes-agent", SystemPrompt: "x"}
	if err := persistTestActor(context.Background(), st, agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	return NewReflexService(st), st, agent.ID
}

func validReflexRow(agentID string) store.AgentReflex {
	return store.AgentReflex{
		AgentID:     agentID,
		Name:        "remind",
		TriggerKind: store.ReflexTriggerPredicate,
		TriggerSpec: `{"kind":"tool_calls_window","window":1,"op":"=","value":0}`,
		ActionKind:  store.ReflexActionInjectReminder,
		ActionSpec:  `{"text":"hi"}`,
		CreatedBy:   "operator",
	}
}

// Declaring a halt_session reflex at plugin tier is rejected with an error
// naming both the kind and the tier; the same row at system or operator
// tier has no provenance-tier error. No live plugin-tier insert path exists,
// so "plugin" is set on the row directly to exercise the gate.
func TestReflexValidateDefinitionProvenanceTierGate(t *testing.T) {
	svc, _, _ := newReflexTestService(t)
	ctx := context.Background()

	baseRow := func(tier string) store.AgentReflex {
		return store.AgentReflex{
			Name:           "halt-gate-test",
			TriggerKind:    store.ReflexTriggerPredicate,
			TriggerSpec:    `{"kind":"tool_calls_window","window":1,"op":"=","value":0}`,
			ActionKind:     store.ReflexActionHaltSession,
			ActionSpec:     `{"reason":"gate test"}`,
			Status:         store.ReflexStatusActive,
			ProvenanceTier: tier,
		}
	}

	t.Run("plugin tier rejected", func(t *testing.T) {
		errs := svc.ValidateDefinition(ctx, baseRow("plugin"))
		found := false
		for _, e := range errs {
			if strings.Contains(e, "plugin") && strings.Contains(e, "halt_session") {
				found = true
			}
		}
		if !found {
			t.Fatalf("ValidateDefinition(halt_session @ plugin) errs = %v, want an error naming both %q and %q", errs, "plugin", "halt_session")
		}
	})

	for _, tier := range []string{"system", "operator"} {
		t.Run(tier+" tier succeeds", func(t *testing.T) {
			for _, e := range svc.ValidateDefinition(ctx, baseRow(tier)) {
				if strings.Contains(e, "provenance tier") {
					t.Fatalf("ValidateDefinition(halt_session @ %s) err %q, want no provenance-tier error", tier, e)
				}
			}
		})
	}
}

// The validate endpoint serializes this result as "errors", and a valid
// definition has always produced null there, not [].
func TestReflexValidateDefinitionValidIsNil(t *testing.T) {
	svc, _, agentID := newReflexTestService(t)
	errs := svc.ValidateDefinition(context.Background(), validReflexRow(agentID))
	if errs != nil {
		t.Fatalf("errs = %#v, want nil", errs)
	}
	raw, _ := json.Marshal(errs)
	if string(raw) != "null" {
		t.Fatalf("marshaled %s, want null", raw)
	}
}

func TestReflexCreateRejectsInvalidDefinition(t *testing.T) {
	svc, _, agentID := newReflexTestService(t)
	row := validReflexRow(agentID)
	row.Name = ""
	_, err := svc.Create(context.Background(), row)
	var invalid *ReflexValidationError
	if !errors.As(err, &invalid) || len(invalid.Errors) == 0 {
		t.Fatalf("err = %v, want *ReflexValidationError", err)
	}
}

func TestMutableReflexWritesRefuseAndPreserveHistory(t *testing.T) {
	svc, st, actor := newReflexTestService(t)
	ctx := t.Context()
	if _, err := st.DB.ExecContext(ctx, `INSERT INTO agent_reflexes(id,name,class_tag,trigger_kind,trigger_spec,action_kind,action_spec,created_by) VALUES('retained','retained','advisor','event','{"name":"ready"}','inject_reminder','{"text":"retained body"}','system')`); err != nil {
		t.Fatal(err)
	}
	const query = `SELECT * FROM agent_reflexes ORDER BY id`
	before := immutableConfigSnapshot(t, st, query)
	valid := validReflexRow(actor)
	_, err := svc.Create(ctx, valid)
	if !errors.Is(err, store.ErrImmutableAgentProfile) {
		t.Fatalf("mutable create: %v", err)
	}
	name := "changed"
	_, err = svc.Patch(ctx, actor, "retained", ReflexPatch{Name: &name})
	if !errors.Is(err, store.ErrImmutableAgentProfile) {
		t.Fatalf("mutable patch: %v", err)
	}
	for _, err := range []error{svc.DeleteOwned(ctx, actor, "retained"), svc.SetOptOut(ctx, actor, "retained"), svc.ClearOptOut(ctx, actor, "retained")} {
		if !errors.Is(err, store.ErrImmutableAgentProfile) {
			t.Fatalf("mutable authority: %v", err)
		}
	}
	immutableConfigUnchanged(t, st, query, before)
}
