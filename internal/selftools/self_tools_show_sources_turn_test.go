package selftools

import (
	"context"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/mcp"
)

// TestCallShowCard_Sources_AcceptsTurnToolUseIDs is the happy path for
// CW-20260429-0024: with the per-turn tool_use_id set stamped on ctx and a
// sources entry citing one of those IDs, the card validates and emits an
// envelope.
func TestCallShowCard_Sources_AcceptsTurnToolUseIDs(t *testing.T) {
	st := newSelfTools(t)
	ctx := mcp.WithTurnToolUseIDs(context.Background(), []string{"toolu_real"})
	args := map[string]any{
		"type":    "report-card",
		"data":    cloneMap(validShowCardPayloads["report-card"]),
		"sources": `[{"tool_use_id":"toolu_real","tool_name":"clockwork_task_list"}]`,
	}
	res, err := st.callShowCard(ctx, args)
	if err != nil {
		t.Fatalf("callShowCard returned transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success with cited turn tool_use_id, got error: %s", readToolText(t, res))
	}
	env := extractEnvelopeJSON(t, readToolText(t, res))
	if got := env["type"]; got != "report-card" {
		t.Fatalf("envelope type: want report-card got %v", got)
	}
}

// TestCallShowCard_Sources_RejectsFabricatedToolUseID is the c112 regression:
// the agent fabricated `tool_use_id: "demo_generation"`. With the turn set
// stamped on ctx the handler must reject the call with an actionable error.
func TestCallShowCard_Sources_RejectsFabricatedToolUseID(t *testing.T) {
	st := newSelfTools(t)
	ctx := mcp.WithTurnToolUseIDs(context.Background(), []string{"toolu_real"})
	args := map[string]any{
		"type":    "report-card",
		"data":    cloneMap(validShowCardPayloads["report-card"]),
		"sources": `[{"tool_use_id":"demo_generation","tool_name":"internal_demo"}]`,
	}
	res, err := st.callShowCard(ctx, args)
	if err != nil {
		t.Fatalf("callShowCard returned transport error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected fabricated-tool_use_id rejection, got success: %s", readToolText(t, res))
	}
	body := readToolText(t, res)
	// Error must cite the offending source index, the bad ID, and the known
	// turn IDs so the agent has actionable repair info.
	for _, want := range []string{"source[0]", "demo_generation", "not from this turn", "toolu_real"} {
		if !strings.Contains(body, want) {
			t.Errorf("error message missing %q: %s", want, body)
		}
	}
}

// TestCallShowCard_Sources_NoCtxStampPreservesExistingBehavior asserts that
// the existing tests (which use bare context.Background()) and subagent
// paths keep working — when no turn set is stamped on ctx, the deep check
// is skipped and only the shallow shape check runs.
func TestCallShowCard_Sources_NoCtxStampPreservesExistingBehavior(t *testing.T) {
	st := newSelfTools(t)
	// No WithTurnToolUseIDs — TurnToolUseIDsFromContext returns nil.
	args := map[string]any{
		"type": "report-card",
		"data": cloneMap(validShowCardPayloads["report-card"]),
		// Even an obviously fabricated ID is fine without ctx stamping —
		// the existing show_card tests rely on this fallback.
		"sources": `[{"tool_use_id":"demo_generation","tool_name":"internal_demo"}]`,
	}
	res, err := st.callShowCard(context.Background(), args)
	if err != nil {
		t.Fatalf("callShowCard returned transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success when ctx is not stamped, got error: %s", readToolText(t, res))
	}
}

// TestCallShowCard_Sources_AllowsToolNameOnlyEntries documents that an entry
// carrying only a tool_name (no tool_use_id) bypasses the deep check —
// validating tool_name against a registry is explicitly out of scope for
// CW-20260429-0024 (the ticket calls it out separately).
func TestCallShowCard_Sources_AllowsToolNameOnlyEntries(t *testing.T) {
	st := newSelfTools(t)
	ctx := mcp.WithTurnToolUseIDs(context.Background(), []string{"toolu_real"})
	args := map[string]any{
		"type":    "report-card",
		"data":    cloneMap(validShowCardPayloads["report-card"]),
		"sources": `[{"tool_name":"some_tool","note":"name-only citation"}]`,
	}
	res, err := st.callShowCard(ctx, args)
	if err != nil {
		t.Fatalf("callShowCard returned transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected name-only sources to pass deep check, got: %s", readToolText(t, res))
	}
}

// TestCallShowCard_Sources_RejectsMixedRealAndFakeIDs confirms the gate
// flags the first bad entry even when a sibling entry is valid — partial
// real-citation must not launder fabricated IDs.
func TestCallShowCard_Sources_RejectsMixedRealAndFakeIDs(t *testing.T) {
	st := newSelfTools(t)
	ctx := mcp.WithTurnToolUseIDs(context.Background(), []string{"toolu_a", "toolu_b"})
	args := map[string]any{
		"type": "report-card",
		"data": cloneMap(validShowCardPayloads["report-card"]),
		"sources": `[
			{"tool_use_id":"toolu_a","tool_name":"x"},
			{"tool_use_id":"fake_one","tool_name":"y"}
		]`,
	}
	res, err := st.callShowCard(ctx, args)
	if err != nil {
		t.Fatalf("callShowCard returned transport error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected mixed-source rejection, got success: %s", readToolText(t, res))
	}
	body := readToolText(t, res)
	if !strings.Contains(body, "source[1]") || !strings.Contains(body, "fake_one") {
		t.Errorf("error should cite source[1] / fake_one, got: %s", body)
	}
}

// TestTurnToolUseIDsFromContext_NilWithoutStamp is a unit-level check on
// the ctx helper itself: callers must be able to distinguish "not stamped"
// (returns nil) from "stamped with empty set" (which WithTurnToolUseIDs
// folds into "not stamped" by design).
func TestTurnToolUseIDsFromContext_NilWithoutStamp(t *testing.T) {
	if got := mcp.TurnToolUseIDsFromContext(context.Background()); got != nil {
		t.Errorf("bare ctx must return nil, got %v", got)
	}
	if got := mcp.TurnToolUseIDsFromContext(nil); got != nil {
		t.Errorf("nil ctx must return nil, got %v", got)
	}
	// Empty input → ctx unchanged → still nil out.
	ctx := mcp.WithTurnToolUseIDs(context.Background(), nil)
	if got := mcp.TurnToolUseIDsFromContext(ctx); got != nil {
		t.Errorf("WithTurnToolUseIDs(nil) must leave ctx un-stamped, got %v", got)
	}
	ctx = mcp.WithTurnToolUseIDs(context.Background(), []string{})
	if got := mcp.TurnToolUseIDsFromContext(ctx); got != nil {
		t.Errorf("WithTurnToolUseIDs(empty) must leave ctx un-stamped, got %v", got)
	}
}

// TestTurnToolUseIDsFromContext_DefensiveCopy asserts WithTurnToolUseIDs
// snapshots the input slice — mutating the caller's slice after stamping
// must not leak through to the ctx value (executeToolBatch builds the
// slice once per dispatch but the ls.toolCallRefs underlying array can be
// resliced by appends in concurrent goroutines).
func TestTurnToolUseIDsFromContext_DefensiveCopy(t *testing.T) {
	src := []string{"toolu_a"}
	ctx := mcp.WithTurnToolUseIDs(context.Background(), src)
	src[0] = "tampered"
	got := mcp.TurnToolUseIDsFromContext(ctx)
	if len(got) != 1 || got[0] != "toolu_a" {
		t.Errorf("ctx value must be a defensive copy, got %v", got)
	}
}

// TestTurnToolNamesFromContext_RoundTrip is the smoke test for the sister
// helper CW-20260429-0025 will lean on.
func TestTurnToolNamesFromContext_RoundTrip(t *testing.T) {
	if got := mcp.TurnToolNamesFromContext(context.Background()); got != nil {
		t.Errorf("bare ctx must return nil, got %v", got)
	}
	ctx := mcp.WithTurnToolNames(context.Background(), []string{"tool_describe", "dev_read"})
	got := mcp.TurnToolNamesFromContext(ctx)
	if len(got) != 2 || got[0] != "tool_describe" || got[1] != "dev_read" {
		t.Errorf("unexpected stamped names: %v", got)
	}
}
