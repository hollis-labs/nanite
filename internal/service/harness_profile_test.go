package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/dispatcher"
	"github.com/hollis-labs/nanite/internal/harnessprofile"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
	"github.com/hollis-labs/nanite/internal/truncate"
)

func harnessSvc(t *testing.T) *chatServiceImpl {
	t.Helper()
	return &chatServiceImpl{}
}

// The default profile must reproduce what the loop compiled in before profiles
// existed, for every constraint an agent can carry and for both callers.
func TestHarnessDefaultProfileParity(t *testing.T) {
	constraintCases := []chat.AgentConstraints{
		{},
		{HardCeiling: 50},
		{ConsecutiveFailCap: 5, RunawayFailCap: 8},
		{ConsecutiveFailCap: 8, RunawayFailCap: 4}, // runaway raised to the warning cap
		{IdleTimeoutSeconds: 120},
		{HardCeiling: 30, ConsecutiveFailCap: 2, RunawayFailCap: 6, IdleTimeoutSeconds: 45},
	}
	for _, sub := range []bool{false, true} {
		caller := dispatcher.CallerType("")
		if sub {
			caller = dispatcher.CallerSubagent
		}
		for i, c := range constraintCases {
			want := resolveIterationLimits(c, caller)
			res, err := harnessSvc(t).resolveHarness(context.Background(), &store.Session{}, c, "claude-opus-5")
			if err != nil {
				t.Fatal(err)
			}
			ls := newLoopState(c, nil, false, caller)
			applyHarness(ls, res, sub)
			got := ls.limits
			if got.hardCeiling != want.hardCeiling || got.consecutiveFailCap != want.consecutiveFailCap ||
				got.runawayFailCap != want.runawayFailCap || got.idleTimeout != want.idleTimeout ||
				got.defaultPerToolCap != want.defaultPerToolCap {
				t.Errorf("subagent=%v case %d: got %+v, want %+v", sub, i, got, want)
			}
		}
	}
}

func TestHarnessDefaultsMatchCompiledConstants(t *testing.T) {
	if harnessprofile.DefaultHardCeiling != defaultHardCeiling ||
		harnessprofile.DefaultConsecutiveFailCap != defaultConsecutiveFailCap ||
		harnessprofile.DefaultRunawayFailCap != defaultRunawayFailCap ||
		harnessprofile.DefaultIdleTimeout != defaultIdleTimeoutSeconds*time.Second ||
		harnessprofile.DefaultSubagentIdleTimeout != subagentIdleTimeoutSeconds*time.Second ||
		harnessprofile.DefaultCompactPreviewBytes != CompactPreviewBudgetBytes ||
		harnessprofile.DefaultPreviewMinBytes != truncate.MaxChars ||
		harnessprofile.DefaultPreviewMaxBytes != truncate.MaxCharsCeiling {
		t.Error("harnessprofile defaults drifted from the compiled-in constants")
	}
}

func TestHarnessPreviewBudgetParity(t *testing.T) {
	res, err := harnessSvc(t).resolveHarness(context.Background(), &store.Session{}, chat.AgentConstraints{}, "")
	if err != nil {
		t.Fatal(err)
	}
	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	applyHarness(ls, res, false)
	for _, model := range []string{"", "no-such-model", "claude-sonnet-4-20250514", "claude-opus-5"} {
		if got, want := ls.previewBudget(model), truncate.BudgetForModel(model); got != want {
			t.Errorf("preview budget for %q = %d, want %d", model, got, want)
		}
	}
	if ls.compactPreviewBudget() != CompactPreviewBudgetBytes {
		t.Errorf("compact = %d", ls.compactPreviewBudget())
	}
	// A loop state built without a profile falls back to the same numbers.
	bare := newLoopState(chat.AgentConstraints{}, nil, false)
	if bare.previewBudget("claude-opus-5") != truncate.BudgetForModel("claude-opus-5") || bare.compactPreviewBudget() != CompactPreviewBudgetBytes {
		t.Error("nil-profile fallback differs")
	}
}

func TestHarnessSessionSelectionChangesTheLimits(t *testing.T) {
	svc := harnessSvc(t)
	sess := &store.Session{Metadata: `{"other":1,"harness_profile":"conservative"}`}
	res, err := svc.resolveHarness(context.Background(), sess, chat.AgentConstraints{}, "claude-opus-5")
	if err != nil {
		t.Fatal(err)
	}
	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	applyHarness(ls, res, false)
	if ls.limits.hardCeiling != 100 || ls.limits.runawayFailCap != 5 || ls.limits.defaultPerToolCap != 50 || ls.limits.idleTimeout != 300*time.Second {
		t.Errorf("limits = %+v", ls.limits)
	}
	if res.Profile != "conservative" || ls.harness.Sources["hard_ceiling"].Layer != "profile:conservative" {
		t.Errorf("recorded profile/source = %q %+v", res.Profile, ls.harness.Sources["hard_ceiling"])
	}
	// The agent's own constraint outranks the profile.
	res, _ = svc.resolveHarness(context.Background(), sess, chat.AgentConstraints{HardCeiling: 42}, "")
	if res.Values.HardCeiling != 42 || res.Sources["hard_ceiling"].Layer != "agent" {
		t.Errorf("agent layer: %d from %q", res.Values.HardCeiling, res.Sources["hard_ceiling"].Layer)
	}
	// Per-launch overrides come from the session metadata and outrank the agent.
	sess = &store.Session{Metadata: `{"harness_profile":"dev","harness_overrides":{"harness":{"hard_ceiling":7}}}`}
	res, err = svc.resolveHarness(context.Background(), sess, chat.AgentConstraints{HardCeiling: 42}, "")
	if err != nil || res.Values.HardCeiling != 7 || res.Sources["hard_ceiling"].Layer != "launch" {
		t.Errorf("launch overrides: %v %d %q", err, res.Values.HardCeiling, res.Sources["hard_ceiling"].Layer)
	}
}

func TestHarnessBadSelectionsFailTheTurn(t *testing.T) {
	svc := harnessSvc(t)
	for _, meta := range []string{
		`{"harness_profile":"nope"}`,
		`{"harness_profile":7}`,
		`{"harness_overrides":{"harness":{"hard_ceiling":-3}}}`,
		`{"harness_overrides":{"harness":{"hard_celing":3}}}`,
	} {
		if _, err := svc.resolveHarness(context.Background(), &store.Session{Metadata: meta}, chat.AgentConstraints{}, ""); err == nil {
			t.Errorf("%s: expected an error", meta)
		}
	}
	// Unrelated or malformed metadata never breaks a turn.
	for _, meta := range []string{"", "{}", "not json", `{"a":1}`} {
		if _, err := svc.resolveHarness(context.Background(), &store.Session{Metadata: meta}, chat.AgentConstraints{}, ""); err != nil {
			t.Errorf("%q: %v", meta, err)
		}
	}
}

func TestHarnessPerToolCapFromUserSettings(t *testing.T) {
	ctx := context.Background()
	st, err := storetest.New(t, ctx, filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(ctx) })
	if _, execErr := st.DB.Exec(`INSERT OR IGNORE INTO user_settings (id) VALUES (1)`); execErr != nil {
		t.Fatal(execErr)
	}
	us, usErr := st.GetUserSettings(ctx)
	if usErr != nil {
		t.Fatal(usErr)
	}
	us.ToolPerTurnCap = 77
	if updErr := st.UpdateUserSettings(ctx, us); updErr != nil {
		t.Fatal(updErr)
	}
	svc := &chatServiceImpl{store: st}
	res, err := svc.resolveHarness(ctx, &store.Session{}, chat.AgentConstraints{}, "")
	if err != nil || res.Values.PerToolCap != 77 || res.Sources["per_tool_cap"].Layer != "app-settings" {
		t.Errorf("default profile: %v %d %q", err, res.Values.PerToolCap, res.Sources["per_tool_cap"].Layer)
	}
	res, _ = svc.resolveHarness(ctx, &store.Session{Metadata: `{"harness_profile":"conservative"}`}, chat.AgentConstraints{}, "")
	if res.Values.PerToolCap != 50 || res.Sources["per_tool_cap"].Layer != "profile:conservative" {
		t.Errorf("a profile that states the cap wins: %d %q", res.Values.PerToolCap, res.Sources["per_tool_cap"].Layer)
	}
}

func TestValidateAndMergeHarnessSelection(t *testing.T) {
	meta, err := MergeHarnessSelection(`{"keep":true}`, "dev", map[string]any{"harness": map[string]any{"hard_ceiling": 9}})
	if err != nil || !strings.Contains(meta, `"keep":true`) || !strings.Contains(meta, `"harness_profile":"dev"`) {
		t.Fatalf("meta = %s (%v)", meta, err)
	}
	if err := ValidateHarnessSelection(nil, meta); err != nil {
		t.Errorf("valid selection rejected: %v", err)
	}
	for _, bad := range []string{`{"harness_profile":"nope"}`, `{"harness_overrides":{"limits":{"idle_timeout_ms":-1}}}`} {
		if err := ValidateHarnessSelection(nil, bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	if err := ValidateHarnessSelection(nil, ""); err != nil {
		t.Errorf("empty metadata: %v", err)
	}
	if _, err := MergeHarnessSelection("[1]", "dev", nil); err == nil {
		t.Error("non-object metadata accepted")
	}
}
