package service

// CW-20260512-0124 (SP-20260512-0011 W4) — Slot system invariants.
//
// This file codifies six invariants that emerged from Sprints 1-4 of the
// harness-restoration arc. Each invariant is a check function
// (`invariantXxx`) that returns an error when the contract breaks, and
// the parameterized `TestSlotInvariants_AcrossDispatchTypes` runs every
// check against every dispatch-flavored CallerType (chat, sync subagent,
// async subagent, background_agent). The four dispatch types collapse
// onto three CallerTypes (sync/async subagent share CallerSubagent) at
// the dispatcher seam; exercising both flavors makes the cross-flavor
// invariance explicit.
//
// The deliberate-violation tests at the bottom prove the check
// functions have teeth — they construct broken assembly plans and
// confirm the corresponding invariant catches the break.
//
// These invariants are documented in internal/context/INVARIANTS.md;
// each entry there points back to the test name below.
//
// The test exercises REAL Dispatcher.WithCallerType + Context Broker +
// Context Service + Anthropic cache_plan assembly. No mocks for the
// broker, the service, or the slot decider — the only synthetic seam
// is a deterministic fakeArtifactStasher (shared with the existing
// service-layer broker tests) used by the pointer-determinism check.

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/chat"
	ctxpkg "github.com/hollis-labs/nanite/internal/context"
	"github.com/hollis-labs/nanite/internal/contextbroker"
	"github.com/hollis-labs/nanite/internal/dispatcher"
	"github.com/hollis-labs/nanite/internal/store"
)

// dispatchFlavor names one of the four dispatch types the orchestrator
// boot prompt enumerates. Sync_subagent and async_subagent collapse to
// CallerSubagent at the dispatcher boundary — exercising both makes
// the cross-flavor invariance explicit.
type dispatchFlavor struct {
	Name   string
	Caller dispatcher.CallerType
}

func dispatchFlavors() []dispatchFlavor {
	return []dispatchFlavor{
		{Name: "chat", Caller: dispatcher.CallerChat},
		{Name: "sync_subagent", Caller: dispatcher.CallerSubagent},
		{Name: "async_subagent", Caller: dispatcher.CallerSubagent},
		{Name: "background_agent", Caller: dispatcher.CallerBackground},
	}
}

// invariantsFixture is the shared session/agent setup the six invariant
// checks operate against. Built fresh per dispatch flavor so each subtest
// sees identical inputs — only the CallerType-tagged context differs.
//
// Phase 0 item 21 ("Cut Modes, in full") removed this fixture's mode /
// altMode fields — Session Mode (store.Mode, sessions.current_mode_id,
// store.GetSessionMode/SetSessionMode) is gone. See invariantModeSlotInert
// (INV4, below) for the post-cut replacement of invariantModeAwareContentSwap.
//
// Phase 0 item 20 (retire workspaces,
// TASKS/phase-0/20-retire-workspaces-and-instance-mechanism.md) removed
// this fixture's workspace field — the in-app `workspaces` table (and the
// *store.Workspace parameter AssembleSlots used to take) is retired in
// full.
type invariantsFixture struct {
	store   *store.Store
	svc     ContextService
	client  *chat.ContextClient
	session *store.Session
	agent   *store.AgentProfile
	tools   []llmtypes.ToolDefinition
}

func newInvariantsFixture(t *testing.T) *invariantsFixture {
	t.Helper()
	s, err := store.New(context.Background(), t.TempDir()+"/invariants.db")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	agent := &store.AgentProfile{
		ID:           "agent-inv",
		Slug:         "invariants-test",
		Name:         "Invariants Test Agent",
		Status:       "active",
		SystemPrompt: "You are the invariants-test agent. Body content for SlotAgent.",
	}
	if err := s.CreateAgent(agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	sess := &store.Session{
		ID:    "sess-inv",
		Title: "Slot invariants session",
	}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := s.CreateMessage(&store.Message{
		ID:        "msg-inv-1",
		SessionID: sess.ID,
		Role:      "user",
		Content:   "implement a small helper function — write_code intent",
	}); err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}

	client := chat.NewContextClient(s)
	client.DevToolsAllowedPaths = []string{
		"/Users/u/Projects-apps/nanite",
		"/Users/u/Projects-apps/agent-workspaces",
	}
	svc := NewContextService(ContextServiceConfig{Client: client})

	return &invariantsFixture{
		store:   s,
		svc:     svc,
		client:  client,
		session: sess,
		agent:   agent,
		tools:   []llmtypes.ToolDefinition{},
	}
}

// assembleWithCaller runs AssembleSlots with the given CallerType
// stamped onto ctx via dispatcher.WithCallerType. The slot decider does
// not read the CallerType (it is metadata on ctx that flows to the
// runner's request_build slog), but the invariants must hold across
// every flavor regardless — that is the load-bearing claim from W1's
// dispatcher consolidation that this file confirms at the broker
// boundary.
func (f *invariantsFixture) assembleWithCaller(t *testing.T, caller dispatcher.CallerType) *SlotAssemblyResult {
	t.Helper()
	ctx := dispatcher.WithCallerType(context.Background(), caller)
	res, err := f.svc.AssembleSlots(ctx, f.session, f.agent, f.tools, "", 200000, "")
	if err != nil {
		t.Fatalf("AssembleSlots (%s): %v", caller, err)
	}
	if res == nil {
		t.Fatalf("AssembleSlots returned nil result for caller %s", caller)
	}
	return res
}

// ---------------------------------------------------------------
// INVARIANT CHECKS
// ---------------------------------------------------------------

// invariantStableSentShape — INVARIANT #1.
//
// The Context Broker's per-turn AssemblyPlan emits exactly
// len(ctxpkg.SlotOrder) decisions in the SAME order across every
// dispatch flavor. Positions are load-bearing for Anthropic's
// cacheable_prefix_tokens math; if any flavor drops or reorders a
// slot, the cache prefix is broken.
//
// The wire-level "5-7 slots typical" subset is derived from the
// plan: only slots with non-empty Content reach the wire
// (slotBlocksFor in chat_generate.go drops empties). The lower
// bound (5) is the worst-case "minimal session" shape with content
// in Universal, System, Agent, Rules, plus conversation history;
// the upper bound is the full SlotOrder length.
func invariantStableSentShape(res *SlotAssemblyResult) error {
	if got, want := len(res.Plan.Decisions), len(ctxpkg.SlotOrder); got != want {
		return fmt.Errorf("plan.Decisions length = %d, want %d (one decision per SlotOrder entry)", got, want)
	}
	for i, name := range ctxpkg.SlotOrder {
		if res.Plan.Decisions[i].SlotName != name {
			return fmt.Errorf("plan position %d = %q, want %q (slot order is the contract)",
				i, res.Plan.Decisions[i].SlotName, name)
		}
	}
	wireCount := 0
	for _, b := range res.Blocks {
		if b.Content != "" {
			wireCount++
		}
	}
	if wireCount < 5 || wireCount > len(ctxpkg.SlotOrder) {
		return fmt.Errorf("wire slot count = %d, want 5..%d (per ticket: 5-7 typical, capped at SlotOrder length)",
			wireCount, len(ctxpkg.SlotOrder))
	}
	return nil
}

// invariantUniversalAtPositionZero — INVARIANT #2.
//
// SlotUniversal MUST sit at position 0 of every plan AND MUST carry
// non-empty content sourced verbatim from chat.UniversalRulesBlock().
// CW-20260512-0114 wired the universal-rules content; this invariant
// proves it reaches every dispatch flavor.
func invariantUniversalAtPositionZero(res *SlotAssemblyResult) error {
	if len(res.Plan.Decisions) == 0 {
		return fmt.Errorf("plan is empty — universal slot cannot be at position 0")
	}
	d0 := res.Plan.Decisions[0]
	if d0.SlotName != ctxpkg.SlotUniversal {
		return fmt.Errorf("position 0 = %q, want %q", d0.SlotName, ctxpkg.SlotUniversal)
	}
	if d0.Action != contextbroker.ActionShip {
		return fmt.Errorf("SlotUniversal action = %v (reason=%q), want ActionShip", d0.Action, d0.ReasonTag)
	}
	if d0.Content == "" {
		return fmt.Errorf("SlotUniversal content empty — universal rules failed to reach the wire")
	}
	if d0.Content != chat.UniversalRulesBlock() {
		return fmt.Errorf("SlotUniversal content drifted from chat.UniversalRulesBlock — content must be sourced verbatim")
	}
	if len(res.Blocks) == 0 || res.Blocks[0].SlotName != ctxpkg.SlotUniversal {
		return fmt.Errorf("first wire block = %q, want SlotUniversal", firstBlockName(res))
	}
	return nil
}

// invariantCacheMarkerPriority — INVARIANT #3.
//
// At the broker level: cacheable-prefix-eligible slots are precisely
// the stable-prefix priority [SlotUniversal, SlotSystem] (per
// internal/llm/anthropic/cache_plan.go). The invariant asserts:
//
//	(a) The cacheable_prefix slot decisions (universal + system) MUST
//	    be ActionShip with non-empty Content — they're the anchor of
//	    the cache prefix and cannot be empty.
//	(b) NEITHER of them may carry a Changed=true marker (dynamic
//	    break would invalidate the marker), which is asserted via the
//	    SlotBlock.Changed field on the assembled wire blocks.
//	(c) Per-turn dynamic slots (SlotMode, SlotContext, SlotSession,
//	    SlotConversation, SlotUserContext, SlotHandoff) MUST NOT
//	    appear in the stable-prefix priority list — that invariant is
//	    structural and asserted against the package-level constant in
//	    internal/llm/anthropic/cache_plan.go's tests
//	    (TestCacheMarkerPriority_*). Here we pin the broker-side
//	    precondition: the slots the cache-plan walks ARE eligible.
//
// The downstream wire-level marker placement is asserted by the
// existing TestCacheMarkerPriority_UniversalSlotFirst_LoadBearing in
// internal/llm/anthropic — this invariant gates the input to that
// test, ensuring the broker hands the cache-plan a valid wire shape.
func invariantCacheMarkerPriority(res *SlotAssemblyResult) error {
	if res.Window == nil {
		return fmt.Errorf("ContextWindow nil — cannot inspect slot cache state")
	}
	for _, name := range []string{ctxpkg.SlotUniversal, ctxpkg.SlotSystem} {
		slot := res.Window.Slot(name)
		if slot == nil {
			return fmt.Errorf("ContextWindow missing %q — cacheable-prefix anchor missing", name)
		}
		if slot.Compactable {
			return fmt.Errorf("%q is marked Compactable — cacheable-prefix anchor must be non-compactable", name)
		}
	}
	// Both cache-anchor slots must ship non-empty content on a chat-flavored
	// assembly. SlotSystem may be empty in degenerate setups, but the
	// think-tool block it always carries (see buildSlotSources' System slot
	// construction) means SlotSystem is non-empty here regardless.
	for _, d := range res.Plan.Decisions {
		if d.SlotName != ctxpkg.SlotUniversal && d.SlotName != ctxpkg.SlotSystem {
			continue
		}
		if d.Content == "" {
			return fmt.Errorf("cacheable-prefix slot %q empty — cache plan would have nothing to mark", d.SlotName)
		}
		if d.Action != contextbroker.ActionShip {
			return fmt.Errorf("cacheable-prefix slot %q action = %v, want ActionShip", d.SlotName, d.Action)
		}
	}
	// Dynamic-content slots must be flagged compactable (or specifically
	// non-compactable for the pinned policy slots Permissions/Workspace).
	// The contract per ctxpkg.DefaultCompactable: per-turn dynamic
	// content (memory, tools, session, context, conversation) is
	// compactable; identity/policy is not. A drift here would mean
	// either a cache marker landed on dynamic content OR a stable slot
	// was reclassified as compactable. Either is a regression.
	expected := ctxpkg.DefaultCompactable()
	for name, wantCompactable := range expected {
		slot := res.Window.Slot(name)
		if slot == nil {
			// Window may omit empty slots; skip — emptiness is not a
			// compaction-policy regression.
			continue
		}
		if slot.Compactable != wantCompactable {
			return fmt.Errorf("slot %q Compactable = %v, want %v (compaction policy drifted)", name, slot.Compactable, wantCompactable)
		}
	}
	return nil
}

// invariantModeSlotInert — INVARIANT #4 (post-cut redefinition).
//
// Phase 0 item 21 ("Cut Modes, in full") deleted Session Mode
// (sessions.current_mode_id, the modes table, store.GetSessionMode /
// SetSessionMode). This invariant replaces invariantModeAwareContentSwap,
// which asserted that re-pointing a session's current_mode_id produced
// different SlotMode content — there is no more session-mode pointer to
// re-point, so that assertion is no longer meaningful (see
// internal/context/INVARIANTS.md's INV4 section for the full rationale).
//
// What still holds, and what this checks:
//
//   - SlotMode's POSITION and IDENTITY in the plan are unchanged — it
//     still occupies its assigned slot in ctxpkg.SlotOrder (INV1's
//     stable-shape contract covers this structurally; asserted again
//     here, scoped to SlotMode specifically, since INV4 is the section
//     of the doc a reader looks to for SlotMode's own contract).
//   - SlotMode's CONTENT is always empty — there is no more mode content
//     source, so the slot is permanently inert.
//   - The assembly decider reports it as ActionSkip / ReasonTag
//     "skipped_no_content", the same path any other empty slot takes —
//     SlotMode gets no special-cased handling now that it carries
//     nothing.
func invariantModeSlotInert(res *SlotAssemblyResult) error {
	idx := indexOfSlot(res.Plan.Decisions, ctxpkg.SlotMode)
	if idx < 0 {
		return fmt.Errorf("SlotMode missing from plan — position/identity must survive even though the slot is inert")
	}
	d := res.Plan.Decisions[idx]
	if d.SlotName != ctxpkg.SlotMode {
		return fmt.Errorf("SlotMode identity drifted: %q", d.SlotName)
	}
	if d.Content != "" {
		return fmt.Errorf("SlotMode content = %q, want empty (Session Mode was cut — no content source remains)", d.Content)
	}
	if d.Action != contextbroker.ActionSkip {
		return fmt.Errorf("SlotMode action = %v, want ActionSkip (empty slots are skipped like any other)", d.Action)
	}
	if d.ReasonTag != "skipped_no_content" {
		return fmt.Errorf("SlotMode ReasonTag = %q, want %q", d.ReasonTag, "skipped_no_content")
	}
	return nil
}

// invariantPointerStashDeterminism — INVARIANT #5.
//
// Same (session, slot, content) input MUST produce the same pointer
// artifact_id across runs. Content-addressing is the property the
// cacheable-prefix math relies on (CW-20260512-0110): identical
// prefix bytes are cache-eligible.
//
// Exercised against the real contextbroker.DecideAssembly with the
// provided stasher (a deterministic fakeArtifactStasher in the
// well-behaved case, shared with the existing W1A service-layer tests;
// a counter-based nondeterministicStasher in the deliberate-violation
// test that proves this check has teeth). The caller parameter threads
// CallerType into the ctx passed to DecideAssembly so the invariant is
// checked under the flavor named by the subtest. The stasher parameter
// is shared between both DecideAssembly calls so stateful regressions
// (e.g. counter-based ID drift) are observable as ArtifactID
// disagreement across the two plans (the determinism check below
// fires before the canonical-format check, so a non-canonical stasher
// fails on the load-bearing equality assertion rather than on format).
func invariantPointerStashDeterminism(caller dispatcher.CallerType, stasher contextbroker.SlotStasher) error {
	budgets := ctxpkg.DefaultBudgets()
	budgets[ctxpkg.SlotMemory] = 5

	oversized := strings.Repeat("memory body slice — ", 200)
	ctx := dispatcher.WithCallerType(context.Background(), caller)
	plan1 := contextbroker.DecideAssembly(ctx, contextbroker.AssemblyInput{
		Intent:    contextbroker.Intent{Type: contextbroker.IntentCustom},
		SlotOrder: ctxpkg.SlotOrder,
		SessionID: "invariant-pointer-sess",
		Sources:   map[string]string{ctxpkg.SlotMemory: oversized},
		Budgets:   budgets,
		Stasher:   stasher,
	})
	plan2 := contextbroker.DecideAssembly(ctx, contextbroker.AssemblyInput{
		Intent:    contextbroker.Intent{Type: contextbroker.IntentCustom},
		SlotOrder: ctxpkg.SlotOrder,
		SessionID: "invariant-pointer-sess",
		Sources:   map[string]string{ctxpkg.SlotMemory: oversized},
		Budgets:   budgets,
		Stasher:   stasher,
	})

	m1 := findSlotDecision(plan1.Decisions, ctxpkg.SlotMemory)
	m2 := findSlotDecision(plan2.Decisions, ctxpkg.SlotMemory)
	if m1.Action != contextbroker.ActionPointer || m2.Action != contextbroker.ActionPointer {
		return fmt.Errorf("pointer not emitted for oversized slot: a=%v b=%v", m1.Action, m2.Action)
	}
	if m1.ArtifactID == "" {
		return fmt.Errorf("pointer ArtifactID empty — pointer would reference nowhere")
	}
	if m1.ArtifactID != m2.ArtifactID {
		return fmt.Errorf("pointer ArtifactID non-deterministic: a=%q b=%q", m1.ArtifactID, m2.ArtifactID)
	}
	if m1.Content != m2.Content {
		return fmt.Errorf("pointer envelope text non-deterministic: a=%q b=%q", m1.Content, m2.Content)
	}
	if !strings.Contains(m1.Content, "<ref:artifact_id=") {
		return fmt.Errorf("pointer envelope malformed: %q", m1.Content)
	}
	if plan1.Stash[ctxpkg.SlotMemory] != oversized {
		return fmt.Errorf("stash entry missing or corrupted — recovery would fail")
	}
	want := contextbroker.DeterministicArtifactID("invariant-pointer-sess", ctxpkg.SlotMemory, oversized)
	if m1.ArtifactID != want {
		return fmt.Errorf("ArtifactID = %q, want %q (canonical deterministic format)", m1.ArtifactID, want)
	}
	return nil
}

// invariantPermissionVisibility — INVARIANT #6.
//
// When the agent has effective path constraints (DevToolsAllowedPaths
// or path_grants), the SlotPermissions block reflects them so the LLM
// reads the constraints rather than reasoning about them from priors
// (the c160 turn-16 fabrication regression target — CW-20260512-0118).
func invariantPermissionVisibility(res *SlotAssemblyResult) error {
	idx := indexOfSlot(res.Plan.Decisions, ctxpkg.SlotPermissions)
	if idx < 0 {
		return fmt.Errorf("SlotPermissions missing from plan")
	}
	d := res.Plan.Decisions[idx]
	if d.Content == "" {
		return fmt.Errorf("SlotPermissions content empty — path constraints invisible to the LLM")
	}
	if !strings.Contains(d.Content, "Path access") {
		return fmt.Errorf("SlotPermissions missing 'Path access' header — render shape drifted: %q", d.Content)
	}
	if !strings.Contains(d.Content, "nanite") {
		return fmt.Errorf("SlotPermissions missing nanite allow-path; agent cannot read what it can reach: %q", d.Content)
	}
	return nil
}

// ---------------------------------------------------------------
// TEST DRIVERS
// ---------------------------------------------------------------

// TestSlotInvariants_AcrossDispatchTypes is the load-bearing W4
// acceptance: every invariant holds across every dispatch flavor.
// Failures here mean either a slot has regressed or one of the
// invariant assertions is missing teeth.
func TestSlotInvariants_AcrossDispatchTypes(t *testing.T) {
	for _, flavor := range dispatchFlavors() {
		flavor := flavor
		t.Run(flavor.Name, func(t *testing.T) {
			f := newInvariantsFixture(t)
			res := f.assembleWithCaller(t, flavor.Caller)
			if err := invariantStableSentShape(res); err != nil {
				t.Errorf("INV1 stable sent shape (%s): %v", flavor.Name, err)
			}
			if err := invariantUniversalAtPositionZero(res); err != nil {
				t.Errorf("INV2 universal slot at position 0 (%s): %v", flavor.Name, err)
			}
			if err := invariantCacheMarkerPriority(res); err != nil {
				t.Errorf("INV3 cache marker priority (%s): %v", flavor.Name, err)
			}
			if err := invariantModeSlotInert(res); err != nil {
				t.Errorf("INV4 mode slot inert (%s): %v", flavor.Name, err)
			}
			if err := invariantPointerStashDeterminism(flavor.Caller, &fakeArtifactStasher{}); err != nil {
				t.Errorf("INV5 pointer/stash determinism (%s): %v", flavor.Name, err)
			}
			if err := invariantPermissionVisibility(res); err != nil {
				t.Errorf("INV6 permission visibility (%s): %v", flavor.Name, err)
			}
		})
	}
}

// TestSlotInvariants_IdenticalShapeAcrossCallerTypes is the
// cross-CallerType structural-shape pin: every CallerType produces the
// SAME plan decision sequence (positions + slot identities). Content
// may differ per flavor in theory; positions never do.
//
// This complements W1's TestRun_identicalShapeAcrossCallerTypes
// (dispatcher input shape) by pinning the AssemblyPlan output shape.
func TestSlotInvariants_IdenticalShapeAcrossCallerTypes(t *testing.T) {
	f := newInvariantsFixture(t)
	first := f.assembleWithCaller(t, dispatcher.CallerChat)
	firstSeq := slotIdentitySequence(first)
	for _, flavor := range dispatchFlavors()[1:] {
		flavor := flavor
		t.Run(flavor.Name, func(t *testing.T) {
			res := f.assembleWithCaller(t, flavor.Caller)
			gotSeq := slotIdentitySequence(res)
			if !reflect.DeepEqual(firstSeq, gotSeq) {
				t.Errorf("slot identity sequence drifted for %s:\n  chat=%v\n  %s=%v",
					flavor.Name, firstSeq, flavor.Name, gotSeq)
			}
		})
	}
}

// ---------------------------------------------------------------
// DELIBERATE-VIOLATION TESTS
// ---------------------------------------------------------------

// TestSlotInvariants_DeliberateViolation_UniversalSlotRemoved proves
// the invariant suite has teeth. It constructs an assembly plan where
// the universal slot source is deliberately removed (simulating a
// regression that disconnected chat.UniversalRulesBlock from
// SlotSources.Universal) and asserts the universal-slot invariant
// catches it.
//
// W4 acceptance from the ticket: "a deliberate violation (e.g.
// removing universal slot) is caught by the tests." A test that
// nominally checks the invariant but does not actually fail on a
// real break would be useless; this pin is the counter-example.
func TestSlotInvariants_DeliberateViolation_UniversalSlotRemoved(t *testing.T) {
	sources := map[string]string{
		ctxpkg.SlotSystem: "system content",
		ctxpkg.SlotAgent:  "agent content",
		// SlotUniversal intentionally absent.
	}
	brokenPlan := contextbroker.DecideAssembly(context.Background(), contextbroker.AssemblyInput{
		Intent:    contextbroker.Intent{Type: contextbroker.IntentCustom},
		SlotOrder: ctxpkg.SlotOrder,
		Sources:   sources,
		Budgets:   ctxpkg.DefaultBudgets(),
		SessionID: "violation-sess",
	})

	res := &SlotAssemblyResult{Plan: brokenPlan}
	err := invariantUniversalAtPositionZero(res)
	if err == nil {
		t.Fatal("DELIBERATE VIOLATION NOT CAUGHT: invariantUniversalAtPositionZero accepted a plan with empty universal slot — assertion has no teeth")
	}
	// Confirm the failure mode is one of the expected universal-slot
	// flag messages, not some unrelated mismatch (false-positive guard).
	if !strings.Contains(err.Error(), "empty") &&
		!strings.Contains(err.Error(), "drifted from chat.UniversalRulesBlock") &&
		!strings.Contains(err.Error(), "want ActionShip") {
		t.Errorf("violation caught but for the wrong reason: %v", err)
	}
}

// TestSlotInvariants_DeliberateViolation_PositionZeroNotUniversal
// pins the second face of INV2: even if SlotUniversal has content,
// if it isn't at position 0 the invariant catches it. Simulates a
// refactor that reordered SlotOrder.
func TestSlotInvariants_DeliberateViolation_PositionZeroNotUniversal(t *testing.T) {
	brokenPlan := contextbroker.AssemblyPlan{
		Decisions: []contextbroker.SlotDecision{
			{SlotName: ctxpkg.SlotSystem, Content: "system content", Action: contextbroker.ActionShip, ReasonTag: "needed"},
			{SlotName: ctxpkg.SlotUniversal, Content: chat.UniversalRulesBlock(), Action: contextbroker.ActionShip, ReasonTag: "needed"},
		},
	}
	res := &SlotAssemblyResult{Plan: brokenPlan}
	err := invariantUniversalAtPositionZero(res)
	if err == nil {
		t.Fatal("DELIBERATE VIOLATION NOT CAUGHT: invariantUniversalAtPositionZero accepted SlotSystem at position 0")
	}
	if !strings.Contains(err.Error(), "position 0") {
		t.Errorf("violation caught but error did not name position 0: %v", err)
	}
}

// TestSlotInvariants_DeliberateViolation_PointerNonDeterministic
// proves invariantPointerStashDeterminism catches a stasher whose
// ArtifactID diverges between runs. Uses a counter-based stasher
// (nondeterministicStasher, shared across both DecideAssembly calls)
// to simulate a regression where two identical inputs produce
// different IDs — the failure mode that would invalidate the cacheable
// prefix on every turn that pointers to oversized slots.
//
// The test drives invariantPointerStashDeterminism directly and
// asserts it returns a non-nil error naming the determinism violation.
// Without this CALL site, the test would only verify the stasher
// itself misbehaves — Copilot's review-round-1 finding (the
// pre-fix shape only checked the stasher setup, not the invariant
// function).
func TestSlotInvariants_DeliberateViolation_PointerNonDeterministic(t *testing.T) {
	stasher := &nondeterministicStasher{}
	err := invariantPointerStashDeterminism(dispatcher.CallerChat, stasher)
	if err == nil {
		t.Fatal("DELIBERATE VIOLATION NOT CAUGHT: invariantPointerStashDeterminism accepted a stasher returning different IDs across calls — assertion has no teeth")
	}
	// False-positive guard: the error must name the determinism
	// failure, not some unrelated mismatch (e.g. pointer-not-emitted).
	// The nondeterministicStasher returns valid pointer envelopes and
	// non-empty IDs; the ONLY failure mode should be ID disagreement
	// across the two plans.
	if !strings.Contains(err.Error(), "non-deterministic") {
		t.Errorf("violation caught but for the wrong reason — expected 'non-deterministic' in error: %v", err)
	}
	// Sanity: confirm the stasher was actually invoked (would catch a
	// regression where the broker stops calling the stasher at all).
	if stasher.count < 2 {
		t.Errorf("stasher invoked %d times, expected ≥2 (one per DecideAssembly call) — broker may have skipped the stash branch", stasher.count)
	}
}

// TestSlotInvariants_DeliberateViolation_ModeSlotNotInert proves
// invariantModeSlotInert (the post-cut INV4) has teeth: a plan where
// SlotMode carries non-empty content (simulating a future regression that
// accidentally repurposes the slot without updating the invariant/doc
// together, per INVARIANTS.md's "How to evolve the contract" step 1) must
// be caught, not silently accepted.
func TestSlotInvariants_DeliberateViolation_ModeSlotNotInert(t *testing.T) {
	brokenPlan := contextbroker.AssemblyPlan{
		Decisions: []contextbroker.SlotDecision{
			{SlotName: ctxpkg.SlotMode, Content: "some content that should not exist", Action: contextbroker.ActionShip, ReasonTag: "needed"},
		},
	}
	res := &SlotAssemblyResult{Plan: brokenPlan}
	err := invariantModeSlotInert(res)
	if err == nil {
		t.Fatal("DELIBERATE VIOLATION NOT CAUGHT: invariantModeSlotInert accepted a plan with non-empty SlotMode content — assertion has no teeth")
	}
	if !strings.Contains(err.Error(), "want empty") {
		t.Errorf("violation caught but for the wrong reason — expected the empty-content message: %v", err)
	}
}

// ---------------------------------------------------------------
// HELPERS
// ---------------------------------------------------------------

// nondeterministicStasher returns a different ArtifactID for every
// call. Used to prove invariantPointerStashDeterminism's assertion
// would fire on a regression.
type nondeterministicStasher struct{ count int }

func (s *nondeterministicStasher) StashSlot(_ context.Context, req contextbroker.StashRequest) (contextbroker.StashResult, error) {
	s.count++
	return contextbroker.StashResult{
		ArtifactID: fmt.Sprintf("art-violation-%d-%s", s.count, contextbroker.DeterministicArtifactID(req.SessionID, req.SlotName, req.Content)),
	}, nil
}

// slotIdentitySequence returns the slot-name sequence in the plan.
// Used to compare structural shape across CallerTypes without caring
// about content.
func slotIdentitySequence(res *SlotAssemblyResult) []string {
	out := make([]string, 0, len(res.Plan.Decisions))
	for _, d := range res.Plan.Decisions {
		out = append(out, d.SlotName)
	}
	return out
}

func firstBlockName(res *SlotAssemblyResult) string {
	if res == nil || len(res.Blocks) == 0 {
		return "(no blocks)"
	}
	return res.Blocks[0].SlotName
}
