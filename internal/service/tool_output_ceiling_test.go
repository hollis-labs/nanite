package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	llmtypes "github.com/hollis-labs/substrate/llm-core/llmtypes"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/harnessprofile"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
	"github.com/hollis-labs/nanite/internal/tool"
)

func loopWithProfile(t *testing.T, sess *store.Session, window int) *loopState {
	t.Helper()
	res, err := harnessSvc(t).resolveHarness(context.Background(), sess, chat.AgentConstraints{}, "claude-opus-5")
	if err != nil {
		t.Fatal(err)
	}
	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	ls.windowTokens = window
	applyHarness(ls, res, false)
	return ls
}

// A model with no window information, and a 200K one, keep today's 24 KiB; a
// 1M window scales up; the value is what the loop uses.
func TestToolOutputCeilingDefaultsAndParity(t *testing.T) {
	for _, tc := range []struct {
		window int
		want   int
	}{{0, 24 * 1024}, {200_000, 24 * 1024}, {1_000_000, 120_000}} {
		ls := loopWithProfile(t, &store.Session{}, tc.window)
		if ls.turnResultCeiling != tc.want {
			t.Errorf("window %d: ceiling %d, want %d", tc.window, ls.turnResultCeiling, tc.want)
		}
	}
	if DefaultTurnResultCeilingBytes != 24*1024 || loopWithProfile(t, &store.Session{}, 0).turnResultCeiling != DefaultTurnResultCeilingBytes {
		t.Error("the no-window ceiling must equal the historical constant")
	}
	// A loop state without a profile keeps the constant.
	if newLoopState(chat.AgentConstraints{}, nil, false).turnResultCeiling != DefaultTurnResultCeilingBytes {
		t.Error("nil-profile loop state lost the default ceiling")
	}
}

// The ceiling follows the context that remains, refreshed each iteration, and a
// single result never exceeds what the remaining context can take.
func TestToolOutputCeilingFollowsRemainingContext(t *testing.T) {
	ls := loopWithProfile(t, &store.Session{}, 1_000_000)
	if ls.turnResultCeiling != 120_000 {
		t.Fatalf("start: %d", ls.turnResultCeiling)
	}
	ls.setRemainingContext(800_000, 790_000) // 10K tokens free -> 10,000 B at 25%
	if ls.turnResultCeiling != 10_000 {
		t.Errorf("ceiling with 10K tokens left = %d, want 10000", ls.turnResultCeiling)
	}
	if got := ls.previewBudget("claude-opus-5"); got != 10_000 {
		t.Errorf("per-result budget = %d, want the remaining cap 10000 (preview would be 16000)", got)
	}
	ls.setRemainingContext(800_000, 799_900) // nearly full -> floor 4 KiB
	if ls.turnResultCeiling != 4096 || ls.previewBudget("claude-opus-5") != 4096 {
		t.Errorf("nearly full: ceiling %d preview %d, want 4096 both", ls.turnResultCeiling, ls.previewBudget("claude-opus-5"))
	}
	ls.setRemainingContext(0, 0) // unknown ceiling: back to unknown remaining
	if ls.remainingTokens != -1 || ls.turnResultCeiling != 120_000 || ls.previewBudget("claude-opus-5") != 16_000 {
		t.Errorf("unknown remaining: %d %d %d", ls.remainingTokens, ls.turnResultCeiling, ls.previewBudget("claude-opus-5"))
	}
}

// The per-result cap is independent of the cumulative ceiling: however large
// the ceiling, one result never exceeds the preview budget; and with the
// ceiling disabled the remaining-context protection still bounds a result.
func TestPerResultCapIsIndependentOfTheCeiling(t *testing.T) {
	sess := &store.Session{Metadata: `{"harness_overrides":{"limits":{"tool_output_bytes":50000000}}}`}
	ls := loopWithProfile(t, sess, 1_000_000)
	if ls.turnResultCeiling != 50_000_000 {
		t.Fatalf("explicit ceiling = %d", ls.turnResultCeiling)
	}
	if got := ls.previewBudget("claude-opus-5"); got > 32_000 {
		t.Errorf("per-result budget %d exceeds preview_max_bytes with a huge ceiling", got)
	}
	off := loopWithProfile(t, &store.Session{Metadata: `{"harness_overrides":{"limits":{"tool_output_bytes":0}}}`}, 1_000_000)
	if off.turnResultCeiling != 0 {
		t.Fatalf("zero must disable the ceiling: %d", off.turnResultCeiling)
	}
	off.setRemainingContext(800_000, 795_000) // 5K tokens left
	if off.turnResultCeiling != 0 {
		t.Errorf("ceiling came back with the remaining budget: %d", off.turnResultCeiling)
	}
	if got := off.previewBudget("claude-opus-5"); got != 5_000 {
		t.Errorf("with the ceiling off a single result must still be bounded by the remaining context: %d", got)
	}
}

// Through the real post-processing path: a near-full context bounds one large
// result to the remaining cap even though the cumulative ceiling was never hit.
func TestPostProcessBoundsAResultByRemainingContext(t *testing.T) {
	ctx := context.Background()
	st, err := storetest.New(t, ctx, filepath.Join(t.TempDir(), "ceiling.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(ctx) })
	svc := makeErrorHonestyService()
	svc.resultCache = tool.NewResultCache(st.DB, tool.ResultCacheConfig{})

	ls := loopWithProfile(t, &store.Session{}, 1_000_000)
	ls.setRemainingContext(800_000, 797_000) // 3K tokens left -> floor 4096
	body := strings.Repeat("row of a very large result\n", 2000)
	tu := llmtypes.ToolUseBlock{ID: "big", Name: "portfolio_source", Input: map[string]any{}}
	ch := make(chan chat.StreamEvent, 8)
	blocks, _ := svc.postProcessToolResults(ctx,
		[]toolPlan{{tu: tu, status: toolPlanReady}},
		[]toolExecResult{{rawOutput: body, ref: chat.ToolCallRef{ID: tu.ID, Name: tu.Name}}},
		ls, ch, "s", "agent", "msg", "claude-opus-5")
	if len(blocks) != 1 || !strings.Contains(blocks[0].Content, "tool_result://") {
		t.Fatalf("result not cached behind a pointer: %.200s", blocks[0].Content)
	}
	if len(blocks[0].Content) > 4096+800 { // preview budget plus the recovery notice
		t.Errorf("delivered %d bytes, want the ~4 KiB remaining cap plus the notice", len(blocks[0].Content))
	}
}

// The original ext setting and env var keep working, with the layer order the
// profile mechanism defines: setting < profile < env.
func TestLegacyCeilingSettingAndEnvPrecedence(t *testing.T) {
	ctx := context.Background()
	st, err := storetest.New(t, ctx, filepath.Join(t.TempDir(), "legacy.db"))
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
	us.ExtSettings = map[string]any{"tool_turn_ceiling_bytes": float64(30000)}
	if updErr := st.UpdateUserSettings(ctx, us); updErr != nil {
		t.Fatal(updErr)
	}
	svc := &chatServiceImpl{store: st}

	res, err := svc.resolveHarness(ctx, &store.Session{}, chat.AgentConstraints{}, "")
	if err != nil || res.Values.ToolOutputBytes == nil || *res.Values.ToolOutputBytes != 30000 || res.Sources["tool_output_bytes"].Layer != "app-settings" {
		t.Fatalf("setting alone: %v %+v %v", err, res.Sources["tool_output_bytes"], res.Values.ToolOutputBytes)
	}
	// A profile or launch that states the ceiling outranks the setting.
	res, _ = svc.resolveHarness(ctx, &store.Session{Metadata: `{"harness_overrides":{"limits":{"tool_output_bytes":40000}}}`}, chat.AgentConstraints{}, "")
	if *res.Values.ToolOutputBytes != 40000 || res.Sources["tool_output_bytes"].Layer != "launch" {
		t.Errorf("launch over setting: %d %q", *res.Values.ToolOutputBytes, res.Sources["tool_output_bytes"].Layer)
	}
	// The original environment variable outranks both.
	t.Setenv("NANITE_TOOL_TURN_CEILING_BYTES", "50000")
	res, _ = svc.resolveHarness(ctx, &store.Session{Metadata: `{"harness_overrides":{"limits":{"tool_output_bytes":40000}}}`}, chat.AgentConstraints{}, "")
	if *res.Values.ToolOutputBytes != 50000 || res.Sources["tool_output_bytes"].Layer != "env:NANITE_TOOL_TURN_CEILING_BYTES" {
		t.Errorf("legacy env: %d %q", *res.Values.ToolOutputBytes, res.Sources["tool_output_bytes"].Layer)
	}
	t.Setenv("NANITE_HARNESS_TOOL_OUTPUT_BYTES", "60000")
	res, _ = svc.resolveHarness(ctx, &store.Session{}, chat.AgentConstraints{}, "")
	if *res.Values.ToolOutputBytes != 60000 {
		t.Errorf("new env name must win over the legacy one: %d", *res.Values.ToolOutputBytes)
	}
	// A zero setting still disables the ceiling.
	t.Setenv("NANITE_TOOL_TURN_CEILING_BYTES", "")
	t.Setenv("NANITE_HARNESS_TOOL_OUTPUT_BYTES", "")
	us.ExtSettings = map[string]any{"tool_turn_ceiling_bytes": float64(0)}
	if updErr := st.UpdateUserSettings(ctx, us); updErr != nil {
		t.Fatal(updErr)
	}
	res, _ = svc.resolveHarness(ctx, &store.Session{}, chat.AgentConstraints{}, "")
	if res.Values.ToolOutputBytes == nil || *res.Values.ToolOutputBytes != 0 || res.Values.TurnCeiling(200_000, -1) != 0 {
		t.Errorf("zero setting: %v", res.Values.ToolOutputBytes)
	}
}

// ---- review notes from the first ceiling PR ----------------------------------

func bigResult(tag string) string { return strings.Repeat(tag+" a row of a large tool result\n", 4000) }

// Several results delivered in one iteration are bounded together: each is sized
// against what the earlier ones left, not against the same pre-iteration figure.
func TestParallelResultsInOneIterationAreBoundedInSum(t *testing.T) {
	ctx := context.Background()
	st, err := storetest.New(t, ctx, filepath.Join(t.TempDir(), "sum.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(ctx) })
	svc := makeErrorHonestyService()
	svc.resultCache = tool.NewResultCache(st.DB, tool.ResultCacheConfig{})

	ls := loopWithProfile(t, &store.Session{}, 1_000_000)
	const remainingTokens = 8_000 // 32,000 bytes of context left
	ls.setRemainingContext(800_000, 800_000-remainingTokens)

	const n = 8
	var plans []toolPlan
	var results []toolExecResult
	for i := 0; i < n; i++ {
		tu := llmtypes.ToolUseBlock{ID: fmt.Sprintf("p%d", i), Name: "portfolio_source", Input: map[string]any{}}
		plans = append(plans, toolPlan{tu: tu, status: toolPlanReady})
		results = append(results, toolExecResult{rawOutput: bigResult(fmt.Sprint(i)), ref: chat.ToolCallRef{ID: tu.ID, Name: tu.Name}})
	}
	blocks, _ := svc.postProcessToolResults(ctx, plans, results, ls, make(chan chat.StreamEvent, 64), "s", "agent", "msg", "claude-opus-5")
	if len(blocks) != n {
		t.Fatalf("blocks = %d", len(blocks))
	}
	sum := 0
	for _, b := range blocks {
		sum += len(b.Content)
	}
	// Each block carries a recovery notice on top of its preview budget.
	if limit := remainingTokens*4 + n*1500; sum > limit {
		t.Errorf("iteration delivered %d bytes, want at most %d (remaining context %d B)", sum, limit, remainingTokens*4)
	}
	if first, last := len(blocks[0].Content), len(blocks[n-1].Content); last >= first {
		t.Errorf("later results were not sized against what earlier ones used: first %d, last %d", first, last)
	}
	if ls.remainingTokens >= remainingTokens {
		t.Errorf("delivered results were not taken out of the remaining context: %d", ls.remainingTokens)
	}
}

// consumeRemainingContext only spends a known remainder, never below zero, and
// lowers the ceiling with it.
func TestConsumeRemainingContext(t *testing.T) {
	ls := loopWithProfile(t, &store.Session{}, 1_000_000)
	ls.consumeRemainingContext(10_000) // remaining unknown: nothing to spend
	if ls.remainingTokens != -1 {
		t.Errorf("unknown remaining changed: %d", ls.remainingTokens)
	}
	ls.setRemainingContext(800_000, 799_000) // 1,000 tokens free
	ls.consumeRemainingContext(2_000)        // 500 tokens
	if ls.remainingTokens != 500 {
		t.Errorf("remaining = %d, want 500", ls.remainingTokens)
	}
	ls.consumeRemainingContext(1_000_000)
	if ls.remainingTokens != 0 || ls.turnResultCeiling != 4096 {
		t.Errorf("exhausted: remaining %d ceiling %d, want 0 and the 4096 floor", ls.remainingTokens, ls.turnResultCeiling)
	}
}

// A missing breakdown makes the remaining context unknown again instead of
// keeping an earlier iteration's figure.
func TestNilBreakdownResetsRemainingContext(t *testing.T) {
	ls := loopWithProfile(t, &store.Session{}, 1_000_000)
	ls.setRemainingFromBreakdown(&chat.TokenBreakdown{Ceiling: 800_000, Total: 795_000})
	if ls.remainingTokens != 5_000 || ls.turnResultCeiling >= 120_000 {
		t.Fatalf("measured: remaining %d ceiling %d", ls.remainingTokens, ls.turnResultCeiling)
	}
	ls.setRemainingFromBreakdown(nil)
	if ls.remainingTokens != -1 || ls.turnResultCeiling != 120_000 {
		t.Errorf("after a nil breakdown: remaining %d ceiling %d, want unknown and the unrestricted 120000", ls.remainingTokens, ls.turnResultCeiling)
	}
	// A breakdown without a ceiling (unknown window) is also "unknown".
	ls.setRemainingFromBreakdown(&chat.TokenBreakdown{Ceiling: 0, Total: 5_000})
	if ls.remainingTokens != -1 {
		t.Errorf("ceiling 0: remaining %d", ls.remainingTokens)
	}
}

// The step-down preview is a result too: a configured compact size larger than
// the context can take is limited to the remaining cap.
func TestCompactPreviewIsClampedToTheRemainingCap(t *testing.T) {
	sess := &store.Session{Metadata: `{"harness_overrides":{"harness":{"compact_preview_bytes":16000}}}`}
	ls := loopWithProfile(t, sess, 1_000_000)
	if got := ls.compactPreviewBudget(); got != 16_000 {
		t.Fatalf("unrestricted compact preview = %d", got)
	}
	ls.setRemainingContext(800_000, 797_000) // 3,000 tokens free -> the 4096 floor
	if got := ls.compactPreviewBudget(); got != 4096 {
		t.Errorf("compact preview with a near-full context = %d, want 4096", got)
	}
	ls.setRemainingContext(800_000, 792_000) // 8,000 tokens -> 8,000 B cap
	if got := ls.compactPreviewBudget(); got != 8_000 {
		t.Errorf("compact preview = %d, want the 8000 remaining cap", got)
	}
	// The default 512 is already below any cap and is left alone.
	def := loopWithProfile(t, &store.Session{}, 1_000_000)
	def.setRemainingContext(800_000, 797_000)
	if got := def.compactPreviewBudget(); got != 512 {
		t.Errorf("default compact preview = %d", got)
	}
}

// The ext user setting is the lowest layer: a profile that states the ceiling
// outranks it, and with no such profile the setting applies.
func TestCeilingSettingIsBelowTheProfile(t *testing.T) {
	ctx := context.Background()
	st, err := storetest.New(t, ctx, filepath.Join(t.TempDir(), "prec.db"))
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
	us.ExtSettings = map[string]any{"tool_turn_ceiling_bytes": float64(30000)}
	if updErr := st.UpdateUserSettings(ctx, us); updErr != nil {
		t.Fatal(updErr)
	}
	dir := t.TempDir()
	if wErr := os.WriteFile(filepath.Join(dir, "capped.yaml"), []byte("name: capped\nlimits: {tool_output_bytes: 40000}\n"), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	reg, err := harnessprofile.NewRegistry(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	svc := &chatServiceImpl{store: st, harnessProfiles: reg}

	res, err := svc.resolveHarness(ctx, &store.Session{}, chat.AgentConstraints{}, "")
	if err != nil || *res.Values.ToolOutputBytes != 30000 || res.Sources["tool_output_bytes"].Layer != "app-settings" {
		t.Fatalf("setting alone: %v %v %+v", err, res.Values.ToolOutputBytes, res.Sources["tool_output_bytes"])
	}
	res, err = svc.resolveHarness(ctx, &store.Session{Metadata: `{"harness_profile":"capped"}`}, chat.AgentConstraints{}, "")
	if err != nil || *res.Values.ToolOutputBytes != 40000 || res.Sources["tool_output_bytes"].Layer != "profile:capped" {
		t.Errorf("a profile that states the ceiling must outrank the setting: %v %v %+v", err, res.Values.ToolOutputBytes, res.Sources["tool_output_bytes"])
	}
	// A profile that does not state it leaves the setting in force.
	res, _ = svc.resolveHarness(ctx, &store.Session{Metadata: `{"harness_profile":"dev"}`}, chat.AgentConstraints{}, "")
	if *res.Values.ToolOutputBytes != 30000 {
		t.Errorf("a profile silent on the ceiling displaced the setting: %d", *res.Values.ToolOutputBytes)
	}
}
