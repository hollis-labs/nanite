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
	if err := st.CreateAgent(context.Background(), agent); err != nil {
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

func TestReflexPatchOwnershipAndRecurrenceClear(t *testing.T) {
	ctx := context.Background()
	svc, st, agentID := newReflexTestService(t)

	row := validReflexRow(agentID)
	override := int64(600)
	row.RecurrenceOverrideSeconds = &override
	created, err := svc.Create(ctx, row)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	other := &store.AgentProfile{Name: "Other", Slug: "other-agent", SystemPrompt: "x"}
	if err = st.CreateAgent(ctx, other); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if _, err = svc.GetOwned(ctx, other.ID, created.ID); !errors.Is(err, ErrReflexNotOwned) {
		t.Fatalf("GetOwned by other agent err = %v, want ErrReflexNotOwned", err)
	}
	if err = svc.DeleteOwned(ctx, other.ID, created.ID); !errors.Is(err, ErrReflexNotOwned) {
		t.Fatalf("DeleteOwned by other agent err = %v, want ErrReflexNotOwned", err)
	}

	zero := int64(0)
	name := "renamed"
	got, err := svc.Patch(ctx, agentID, created.ID, ReflexPatch{Name: &name, RecurrenceOverrideSeconds: &zero})
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if got.Name != "renamed" || got.RecurrenceOverrideSeconds != nil {
		t.Fatalf("patched = %+v, want renamed with override cleared", got)
	}
	if got.ActionSpec != created.ActionSpec || got.TriggerSpec != created.TriggerSpec {
		t.Fatalf("unpatched columns changed: %+v", got)
	}

	if _, err := svc.GetOwned(ctx, agentID, "missing"); !errors.Is(err, store.ErrAgentReflexNotFound) {
		t.Fatalf("GetOwned missing err = %v, want store.ErrAgentReflexNotFound", err)
	}
}
