package service

import (
	"context"
	"testing"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/contextbroker"
	ctxpkg "github.com/hollis-labs/nanite/internal/context"
	"github.com/hollis-labs/nanite/internal/store"
)

// Test_ModeIsSessionAttribute_SameAgentDifferentModes_DifferentSlotContent is
// the load-bearing CW-20260512-0115 acceptance test: two sessions sharing
// the SAME agent profile but pointing at DIFFERENT session-level modes
// produce DIFFERENT SlotMode content in the assembled plan. This codifies
// the architectural intent that mode is a SESSION attribute, not an agent
// attribute — the same agent identity can operate in different modes in
// different sessions concurrently (`feedback_multi_session_default`).
//
// SP-20260512-0009 W5 (CW-20260512-0115).
func Test_ModeIsSessionAttribute_SameAgentDifferentModes_DifferentSlotContent(t *testing.T) {
	s, err := store.New(context.Background(), t.TempDir()+"/test.db")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}

	// Seed two distinct modes with distinguishable PromptAddendum text. We
	// don't use SeedBuiltinModes because the assertion is on SlotMode content
	// equality — bespoke addenda make the diff unambiguous.
	buildMode := &store.Mode{
		ID:             "mode-build-115",
		Slug:           "build-115",
		Name:           "Build (115 test)",
		PromptAddendum: "MODE_BUILD_SENTINEL: focus on edits, write code aggressively.",
	}
	if err := s.CreateMode(buildMode); err != nil {
		t.Fatalf("CreateMode build: %v", err)
	}
	readMode := &store.Mode{
		ID:             "mode-read-115",
		Slug:           "read-115",
		Name:           "Read (115 test)",
		PromptAddendum: "MODE_READ_SENTINEL: read-only — refuse to mutate state.",
	}
	if err := s.CreateMode(readMode); err != nil {
		t.Fatalf("CreateMode read: %v", err)
	}

	// One agent — identity-only. The session-level pointer alone drives
	// SlotMode content. (The prior `DefaultMode` profile field was deleted
	// in migration 063 as part of CW-20260512-0115 review round 1.)
	agent := &store.AgentProfile{
		ID:           "agent-shared-115",
		Slug:         "shared-115",
		Status:       "active",
		SystemPrompt: "shared agent identity",
	}

	// Session A → build mode.
	sessA := &store.Session{ID: "sess-A-115"}
	if err := s.CreateSession(sessA); err != nil {
		t.Fatalf("CreateSession A: %v", err)
	}
	if err := s.SetSessionMode(sessA.ID, buildMode.ID); err != nil {
		t.Fatalf("SetSessionMode A: %v", err)
	}
	// Reload — CreateSession returns a sparse pointer; the wired CurrentModeID
	// only lands on subsequent GetSession reads, which is the production path.
	sessA, err = s.GetSession(sessA.ID)
	if err != nil {
		t.Fatalf("GetSession A: %v", err)
	}

	// Session B → read mode.
	sessB := &store.Session{ID: "sess-B-115"}
	if err := s.CreateSession(sessB); err != nil {
		t.Fatalf("CreateSession B: %v", err)
	}
	if err := s.SetSessionMode(sessB.ID, readMode.ID); err != nil {
		t.Fatalf("SetSessionMode B: %v", err)
	}
	sessB, err = s.GetSession(sessB.ID)
	if err != nil {
		t.Fatalf("GetSession B: %v", err)
	}

	client := chat.NewContextClient(s)
	svc := NewContextService(ContextServiceConfig{Client: client})

	// Production wiring resolves *store.Mode from sessions.current_mode_id
	// inside chat_generate; in the unit test we resolve it the same way
	// (chat_generate.go:423-431) and pass it through AssembleSlots.
	modeA, err := s.GetSessionMode(sessA.ID)
	if err != nil {
		t.Fatalf("GetSessionMode A: %v", err)
	}
	if modeA == nil || modeA.ID != buildMode.ID {
		t.Fatalf("session A mode mismatch: got %#v, want %s", modeA, buildMode.ID)
	}
	modeB, err := s.GetSessionMode(sessB.ID)
	if err != nil {
		t.Fatalf("GetSessionMode B: %v", err)
	}
	if modeB == nil || modeB.ID != readMode.ID {
		t.Fatalf("session B mode mismatch: got %#v, want %s", modeB, readMode.ID)
	}

	resA, err := svc.AssembleSlots(context.Background(), sessA, agent, nil, nil, nil, "", 200000, modeA, "")
	if err != nil {
		t.Fatalf("AssembleSlots A: %v", err)
	}
	resB, err := svc.AssembleSlots(context.Background(), sessB, agent, nil, nil, nil, "", 200000, modeB, "")
	if err != nil {
		t.Fatalf("AssembleSlots B: %v", err)
	}

	slotModeA := findSlotDecision(resA.Plan.Decisions, ctxpkg.SlotMode)
	slotModeB := findSlotDecision(resB.Plan.Decisions, ctxpkg.SlotMode)

	if slotModeA.SlotName == "" || slotModeB.SlotName == "" {
		t.Fatalf("SlotMode decision missing from plan: A=%+v B=%+v", slotModeA, slotModeB)
	}

	if slotModeA.Content == slotModeB.Content {
		t.Fatalf("SAME agent + DIFFERENT session modes must produce DIFFERENT SlotMode content; got identical: %q", slotModeA.Content)
	}
	if slotModeA.Content != buildMode.PromptAddendum {
		t.Errorf("session A SlotMode content = %q, want build addendum %q", slotModeA.Content, buildMode.PromptAddendum)
	}
	if slotModeB.Content != readMode.PromptAddendum {
		t.Errorf("session B SlotMode content = %q, want read addendum %q", slotModeB.Content, readMode.PromptAddendum)
	}

	// Position-stability invariant — both plans walk SlotOrder, so the
	// SlotMode index is identical even though the content differs. This is
	// the cache-prefix-stable guarantee from CW-20260512-0109.
	idxA := indexOfSlot(resA.Plan.Decisions, ctxpkg.SlotMode)
	idxB := indexOfSlot(resB.Plan.Decisions, ctxpkg.SlotMode)
	if idxA != idxB {
		t.Errorf("SlotMode position drifted between sessions: A=%d B=%d", idxA, idxB)
	}
}

// Test_ModeChangeMidSession_NextDispatchReflectsNewMode codifies the
// mode-aware content swap: when a session's mode changes, the NEXT call to
// AssembleSlots produces SlotMode content for the new mode. No restart, no
// session re-creation — just a re-read of sessions.current_mode_id at slot
// assembly time.
//
// SP-20260512-0009 W5 (CW-20260512-0115).
func Test_ModeChangeMidSession_NextDispatchReflectsNewMode(t *testing.T) {
	s, err := store.New(context.Background(), t.TempDir()+"/test.db")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}

	beforeMode := &store.Mode{
		ID:             "mode-before-115",
		Slug:           "before-115",
		Name:           "Before",
		PromptAddendum: "BEFORE_SENTINEL: pre-swap mode addendum.",
	}
	if err := s.CreateMode(beforeMode); err != nil {
		t.Fatalf("CreateMode before: %v", err)
	}
	afterMode := &store.Mode{
		ID:             "mode-after-115",
		Slug:           "after-115",
		Name:           "After",
		PromptAddendum: "AFTER_SENTINEL: post-swap mode addendum.",
	}
	if err := s.CreateMode(afterMode); err != nil {
		t.Fatalf("CreateMode after: %v", err)
	}

	agent := &store.AgentProfile{ID: "agent-swap-115", Slug: "swap-115", Status: "active", SystemPrompt: "swap"}
	sess := &store.Session{ID: "sess-swap-115"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if err := s.SetSessionMode(sess.ID, beforeMode.ID); err != nil {
		t.Fatalf("SetSessionMode before: %v", err)
	}
	sessReloaded, err := s.GetSession(sess.ID)
	if err != nil {
		t.Fatalf("GetSession (before): %v", err)
	}

	client := chat.NewContextClient(s)
	svc := NewContextService(ContextServiceConfig{Client: client})

	modeBefore, err := s.GetSessionMode(sessReloaded.ID)
	if err != nil {
		t.Fatalf("GetSessionMode before: %v", err)
	}
	res1, err := svc.AssembleSlots(context.Background(), sessReloaded, agent, nil, nil, nil, "", 200000, modeBefore, "")
	if err != nil {
		t.Fatalf("AssembleSlots before: %v", err)
	}

	// Swap the session's mode pointer mid-session. Production path goes
	// through the PATCH /api/sessions/{id}/mode handler which calls
	// SetSessionMode + emits a session_mode_changed event; the next
	// dispatch picks up the new pointer because chat_generate reloads
	// session.CurrentModeID at the top of every turn.
	if err := s.SetSessionMode(sess.ID, afterMode.ID); err != nil {
		t.Fatalf("SetSessionMode after: %v", err)
	}
	sessAfter, err := s.GetSession(sess.ID)
	if err != nil {
		t.Fatalf("GetSession (after): %v", err)
	}
	modeAfter, err := s.GetSessionMode(sessAfter.ID)
	if err != nil {
		t.Fatalf("GetSessionMode after: %v", err)
	}
	res2, err := svc.AssembleSlots(context.Background(), sessAfter, agent, nil, nil, nil, "", 200000, modeAfter, "")
	if err != nil {
		t.Fatalf("AssembleSlots after: %v", err)
	}

	slotMode1 := findSlotDecision(res1.Plan.Decisions, ctxpkg.SlotMode)
	slotMode2 := findSlotDecision(res2.Plan.Decisions, ctxpkg.SlotMode)

	if slotMode1.Content != beforeMode.PromptAddendum {
		t.Errorf("pre-swap SlotMode content = %q, want %q", slotMode1.Content, beforeMode.PromptAddendum)
	}
	if slotMode2.Content != afterMode.PromptAddendum {
		t.Errorf("post-swap SlotMode content = %q, want %q (no restart required)", slotMode2.Content, afterMode.PromptAddendum)
	}
	if slotMode1.Content == slotMode2.Content {
		t.Fatalf("SlotMode content did not swap on mode change: %q (broken: mode swap requires restart)", slotMode1.Content)
	}

	// Cache-prefix-stable invariant — SlotMode position must not move
	// between turns. Anthropic's cacheable_prefix_tokens math depends on
	// positional stability; only mode-INDEPENDENT slots (SlotUniversal,
	// SlotSystem) carry cache markers (see anthropic/cache_plan.go's
	// stablePrefixSlotPriority), so the cacheable prefix survives a mode
	// swap by construction.
	if len(res1.Plan.Decisions) != len(res2.Plan.Decisions) {
		t.Fatalf("decision count changed across mode swap: %d → %d", len(res1.Plan.Decisions), len(res2.Plan.Decisions))
	}
	for i := range res1.Plan.Decisions {
		if res1.Plan.Decisions[i].SlotName != res2.Plan.Decisions[i].SlotName {
			t.Errorf("slot position drifted across mode swap at index %d: %q → %q (cache prefix broken)",
				i, res1.Plan.Decisions[i].SlotName, res2.Plan.Decisions[i].SlotName)
		}
	}
}

// Test_ModeChangeMidSession_CacheableSlotsUnchanged is the cache-implications
// regression pin for CW-20260512-0115: only mode-INDEPENDENT slots
// (SlotUniversal, SlotSystem) carry cache markers (per
// internal/llm/anthropic/cache_plan.go's stablePrefixSlotPriority). Their
// content must NOT change when only the session-mode pointer swaps, so the
// cacheable prefix survives the swap.
//
// SP-20260512-0009 W5 (CW-20260512-0115).
func Test_ModeChangeMidSession_CacheableSlotsUnchanged(t *testing.T) {
	s, err := store.New(context.Background(), t.TempDir()+"/test.db")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}

	beforeMode := &store.Mode{
		ID:             "mode-cache-before-115",
		Slug:           "cache-before-115",
		Name:           "Cache Before",
		PromptAddendum: "CACHE_BEFORE: pre-swap.",
	}
	if err := s.CreateMode(beforeMode); err != nil {
		t.Fatalf("CreateMode before: %v", err)
	}
	afterMode := &store.Mode{
		ID:             "mode-cache-after-115",
		Slug:           "cache-after-115",
		Name:           "Cache After",
		PromptAddendum: "CACHE_AFTER: post-swap.",
	}
	if err := s.CreateMode(afterMode); err != nil {
		t.Fatalf("CreateMode after: %v", err)
	}

	agent := &store.AgentProfile{ID: "agent-cache-115", Slug: "cache-115", Status: "active", SystemPrompt: "cache test"}
	sess := &store.Session{ID: "sess-cache-115"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if err := s.SetSessionMode(sess.ID, beforeMode.ID); err != nil {
		t.Fatalf("SetSessionMode before: %v", err)
	}
	sessBefore, err := s.GetSession(sess.ID)
	if err != nil {
		t.Fatalf("GetSession before: %v", err)
	}

	client := chat.NewContextClient(s)
	svc := NewContextService(ContextServiceConfig{Client: client})
	modeBefore, err := s.GetSessionMode(sessBefore.ID)
	if err != nil {
		t.Fatalf("GetSessionMode before: %v", err)
	}
	if modeBefore == nil {
		t.Fatal("GetSessionMode before returned nil mode")
	}
	res1, err := svc.AssembleSlots(context.Background(), sessBefore, agent, nil, nil, nil, "", 200000, modeBefore, "")
	if err != nil {
		t.Fatalf("AssembleSlots before: %v", err)
	}

	if err := s.SetSessionMode(sess.ID, afterMode.ID); err != nil {
		t.Fatalf("SetSessionMode after: %v", err)
	}
	sessAfter, err := s.GetSession(sess.ID)
	if err != nil {
		t.Fatalf("GetSession after: %v", err)
	}
	modeAfter, err := s.GetSessionMode(sessAfter.ID)
	if err != nil {
		t.Fatalf("GetSessionMode after: %v", err)
	}
	if modeAfter == nil {
		t.Fatal("GetSessionMode after returned nil mode")
	}
	res2, err := svc.AssembleSlots(context.Background(), sessAfter, agent, nil, nil, nil, "", 200000, modeAfter, "")
	if err != nil {
		t.Fatalf("AssembleSlots after: %v", err)
	}

	for _, slot := range []string{ctxpkg.SlotUniversal, ctxpkg.SlotSystem} {
		c1 := findSlotDecision(res1.Plan.Decisions, slot).Content
		c2 := findSlotDecision(res2.Plan.Decisions, slot).Content
		if c1 != c2 {
			t.Errorf("%s content drifted across mode swap: cache marker invalidated.\n  pre=%q\n  post=%q",
				slot, c1, c2)
		}
	}
}

// Test_AgentDefaultMode_NotConsultedDuringSession was deleted in
// CW-20260512-0115 review round 1: migration 063 drops the
// `agent_profiles.default_mode` column the test pinned, so the
// "default_mode never bleeds into runtime" invariant collapses to "the
// field doesn't exist." The schema-level guarantee is now load-bearing in
// place of the runtime regression test (per the original test's own doc
// note that it would become obsolete on column drop).

// findSlotDecision returns the SlotDecision for the named slot, or a
// zero-value SlotDecision if the slot is absent. Callers test the returned
// SlotName for emptiness to detect absence.
func findSlotDecision(decisions []contextbroker.SlotDecision, name string) contextbroker.SlotDecision {
	for _, d := range decisions {
		if d.SlotName == name {
			return d
		}
	}
	return contextbroker.SlotDecision{}
}

// indexOfSlot returns the position of the named slot in the decisions slice,
// or -1 if absent.
func indexOfSlot(decisions []contextbroker.SlotDecision, name string) int {
	for i, d := range decisions {
		if d.SlotName == name {
			return i
		}
	}
	return -1
}
