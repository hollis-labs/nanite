package service

import (
	"context"
	"errors"
	"testing"

	"github.com/hollis-labs/nanite/internal/runtimekind"
	"github.com/hollis-labs/nanite/internal/store"
)

// A prior authorized fresh journal may retain an older runtime token. This
// does not promote a historical profile or issue an actor.
func TestDurableAgentStart_PriorAuthorizedJournalWithLegacyRuntimeToken(t *testing.T) {
	ctx := context.Background()
	st := newDurableAgentServiceTestStore(t)
	profile := &store.AgentProfile{Name: "Legacy Agent", Slug: "legacy-agent", SystemPrompt: "x"}
	if err := persistTestActor(ctx, st, profile); err != nil {
		t.Fatalf("persist prior actor: %v", err)
	}
	svc := NewDurableAgentService(st)
	inst := &store.DurableAgentInstance{
		Name:             "Legacy Instance",
		Slug:             "legacy-instance",
		ProfileID:        profile.ID,
		LifecycleClass:   store.DurableAgentClassAdvisor,
		Provider:         "codex",
		RuntimeKind:      string(runtimekind.SubprocessPerTurn),
		LaunchSourceType: store.DurableAgentLaunchDurableAdvisor,
	}
	if err := persistTestDurableInstance(ctx, st, inst); err != nil {
		t.Fatalf("persist prior instance: %v", err)
	}
	// Private prior journal with an older runtime spelling.
	if _, err := st.DB.ExecContext(ctx, `UPDATE actor_instances SET runtime_kind = 'subprocess' WHERE id = ?`, inst.ID); err != nil {
		t.Fatalf("write legacy runtime_kind: %v", err)
	}

	got, err := svc.Start(ctx, inst.ID, DurableAgentStartRequest{})
	if err != nil {
		t.Fatalf("Start with a legacy 'subprocess' row: %v", err)
	}
	if got.Instance.RuntimeKind != "subprocess" {
		t.Fatalf("fixture row runtime_kind = %q, want the legacy 'subprocess'", got.Instance.RuntimeKind)
	}
	if got.Policy.RuntimeKind != string(runtimekind.SubprocessPerTurn) {
		t.Fatalf("launch policy runtime_kind = %q, want %q", got.Policy.RuntimeKind, runtimekind.SubprocessPerTurn)
	}
	if got.Session == nil || got.Instance.Status != store.DurableAgentStatusActive {
		t.Fatalf("Start result = %+v, want an active instance with a session", got)
	}
}

// Every pre-v0.12.0 spelling resolves to its current kind in the launch
// policy, and a terminal-only kind is still refused.
func TestDurableAgentLaunchPolicy_LegacyRuntimeKinds(t *testing.T) {
	for raw, want := range map[string]runtimekind.Kind{
		"subprocess": runtimekind.SubprocessPerTurn,
		"serve-http": runtimekind.HTTPSSE,
		"app-server": runtimekind.JSONRPCStdio,
		"api":        runtimekind.API,
		"":           runtimekind.API,
	} {
		inst := &store.DurableAgentInstance{ID: "i", LifecycleClass: store.DurableAgentClassAdvisor, RuntimeKind: raw}
		policy, err := durableAgentLaunchPolicyFor(inst, DurableAgentWakePayload{Reason: DurableAgentWakeManual})
		if err != nil {
			t.Fatalf("runtime_kind %q: %v", raw, err)
		}
		if policy.RuntimeKind != string(want) {
			t.Errorf("runtime_kind %q -> policy %q, want %q", raw, policy.RuntimeKind, want)
		}
	}
	for _, raw := range []string{"pty-debug", "pty", "bogus"} {
		inst := &store.DurableAgentInstance{ID: "i", LifecycleClass: store.DurableAgentClassAdvisor, RuntimeKind: raw}
		if _, err := durableAgentLaunchPolicyFor(inst, DurableAgentWakePayload{Reason: DurableAgentWakeManual}); !errors.Is(err, ErrDurableAgentUnsupportedLaunchPlan) {
			t.Errorf("runtime_kind %q: err = %v, want ErrDurableAgentUnsupportedLaunchPlan", raw, err)
		}
	}
}
