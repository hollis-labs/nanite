package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
	"github.com/hollis-labs/nanite/internal/tool/intent"
	"github.com/hollis-labs/nanite/internal/tool/stash"
)

// fixedOverrideStore returns a fixed override regardless of session ID.
type fixedOverrideStore struct{ o intent.Override }

func (f *fixedOverrideStore) Get(string) intent.Override { return f.o }

// scriptedClassifier returns a fixed Result for every call; records the input.
type scriptedClassifier struct {
	result intent.Result
	err    error
	calls  int
	last   intent.Input
}

func (s *scriptedClassifier) Classify(_ context.Context, in intent.Input) (intent.Result, error) {
	s.calls++
	s.last = in
	return s.result, s.err
}

func newStubbedContextService(t *testing.T, classifier intent.Classifier, overrides ToolCacheOverrideStore, cacheEnabled bool) (*contextServiceImpl, *store.Store) {
	t.Helper()
	s, err := storetest.New(t, context.Background(), t.TempDir()+"/test.db")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	if err := s.Seed(context.Background()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if !cacheEnabled {
		// Turn off the default (enabled=1) to exercise the S3a passthrough.
		if err := s.UpdateUserSettings(context.Background(), &store.UserSettings{ToolCacheEnabled: false}); err != nil {
			t.Fatalf("UpdateUserSettings: %v", err)
		}
	}

	client := chat.NewContextClient(s)
	stashMgr := stash.NewManager(stash.BuiltinCategorizer())

	svc := NewContextService(ContextServiceConfig{
		Client:       client,
		StashManager: stashMgr,
		Classifier:   classifier,
		Overrides:    overrides,
		SettingsFunc: func() *store.UserSettings {
			us, err := s.GetUserSettings(context.Background())
			if err != nil {
				return nil
			}
			return us
		},
	}).(*contextServiceImpl)
	return svc, s
}

func seedSession(t *testing.T, s *store.Store, userTurn string) (*store.Session, *store.AgentProfile) {
	t.Helper()
	sess := &store.Session{ID: "s-s3b-" + userTurn[:min(8, len(userTurn))], Title: "S3bTest"}
	if err := s.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := s.CreateMessage(context.Background(), &store.Message{
		ID:        "m-" + sess.ID,
		SessionID: sess.ID,
		Role:      "user",
		Content:   userTurn,
	}); err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	agent := &store.AgentProfile{
		ID: "a1", Name: "A1", Slug: "a1", SystemPrompt: "sys", Status: "active",
	}
	return sess, agent
}

func tools3() []llmtypes.ToolDefinition {
	return []llmtypes.ToolDefinition{
		{Name: "dev_grep", Description: "search files"},
		{Name: "dev_read", Description: "read a file"},
		{Name: "dev_bash", Description: "run bash"},
	}
}

func TestAssembleSlots_S3b_RulesHitHydrates(t *testing.T) {
	cls := &scriptedClassifier{
		result: intent.Result{Hydrate: true, Categories: nil, Source: intent.SourceRules, Reasoning: "run keyword"},
	}
	svc, s := newStubbedContextService(t, cls, nil, true)
	sess, agent := seedSession(t, s, "please run the tests")

	r, err := svc.AssembleSlots(context.Background(), sess, agent, tools3(), "", 200000, "")
	if err != nil {
		t.Fatalf("AssembleSlots: %v", err)
	}
	if cls.calls != 1 {
		t.Fatalf("classifier should have been invoked once, got %d", cls.calls)
	}
	if r.ToolCache == nil {
		t.Fatalf("expected ToolCache outcome to be populated")
	}
	if r.ToolCache.Next != StateFull {
		t.Fatalf("expected StateFull, got %v", r.ToolCache.Next)
	}

	// Tools slot should be full JSON.
	slot := r.Window.Slot("tools")
	var defs []llmtypes.ToolDefinition
	if err := json.Unmarshal([]byte(slot.Content), &defs); err != nil {
		t.Fatalf("hydrated slot should be JSON-marshaled defs: %v (raw=%q)", err, slot.Content)
	}
	if len(defs) != 3 {
		t.Fatalf("expected 3 defs in hydrated slot, got %d", len(defs))
	}
}

func TestAssembleSlots_S3b_NoIntentKeepsPointer(t *testing.T) {
	cls := &scriptedClassifier{
		result: intent.Result{Hydrate: false, Source: intent.SourceRules, Reasoning: "no intent"},
	}
	svc, s := newStubbedContextService(t, cls, nil, true)
	sess, agent := seedSession(t, s, "hello world")

	r, err := svc.AssembleSlots(context.Background(), sess, agent, tools3(), "", 200000, "")
	if err != nil {
		t.Fatalf("AssembleSlots: %v", err)
	}
	if r.ToolCache.Next != StatePointer {
		t.Fatalf("expected StatePointer, got %v", r.ToolCache.Next)
	}
	content := r.Window.Slot("tools").Content
	if !strings.Contains(content, "pointer") {
		t.Fatalf("pointer slot should contain summary text; got %q", content)
	}
	if strings.HasPrefix(content, "[") && strings.Contains(content, "\"name\"") {
		t.Fatalf("pointer slot should NOT contain JSON defs; got %q", content)
	}
}

func TestAssembleSlots_S3b_PartialHydration(t *testing.T) {
	cls := &scriptedClassifier{
		result: intent.Result{
			Hydrate:    true,
			Categories: []string{"search"}, // only hydrate search tools
			Source:     intent.SourceLLM,
			Reasoning:  "user asked to find something",
		},
	}
	svc, s := newStubbedContextService(t, cls, nil, true)
	sess, agent := seedSession(t, s, "find the thing")

	r, err := svc.AssembleSlots(context.Background(), sess, agent, tools3(), "", 200000, "")
	if err != nil {
		t.Fatalf("AssembleSlots: %v", err)
	}
	if r.ToolCache.Next != StatePartial {
		t.Fatalf("expected StatePartial, got %v", r.ToolCache.Next)
	}
	content := r.Window.Slot("tools").Content
	// Partial format: JSON defs then "\n\n" then summary lines.
	parts := strings.SplitN(content, "\n\n", 2)
	if len(parts) < 2 {
		t.Fatalf("partial content should have JSON + summary sections: %q", content)
	}
	var defs []llmtypes.ToolDefinition
	if err := json.Unmarshal([]byte(parts[0]), &defs); err != nil {
		t.Fatalf("partial JSON section invalid: %v", err)
	}
	// "search" category only includes dev_grep — so exactly 1 def hydrated.
	if len(defs) != 1 || defs[0].Name != "dev_grep" {
		t.Fatalf("expected only dev_grep in hydrated partial; got %+v", defs)
	}
	if !strings.Contains(parts[1], "pointer") {
		t.Fatalf("partial summary should reference pointer/categories; got %q", parts[1])
	}
}

func TestAssembleSlots_S3b_ExplicitOverrideOn(t *testing.T) {
	cls := &scriptedClassifier{
		result: intent.Result{Hydrate: false, Source: intent.SourceRules, Reasoning: "no intent"},
	}
	overrides := &fixedOverrideStore{o: intent.OverrideOn}
	svc, s := newStubbedContextService(t, cls, overrides, true)
	sess, agent := seedSession(t, s, "chat about stuff")

	r, err := svc.AssembleSlots(context.Background(), sess, agent, tools3(), "", 200000, "")
	if err != nil {
		t.Fatalf("AssembleSlots: %v", err)
	}
	// Override is surfaced via Input.Override; the scripted classifier ignores
	// it (doesn't use the BrokerClassifier). We verify the Input actually
	// carried the override, since that's the contract with T6.
	if cls.last.Override != intent.OverrideOn {
		t.Fatalf("classifier should receive OverrideOn from the store; got %v", cls.last.Override)
	}
	_ = r
}

func TestAssembleSlots_S3b_SelectionHashRebuild(t *testing.T) {
	cls := &scriptedClassifier{
		result: intent.Result{Hydrate: false, Source: intent.SourceRules, Reasoning: "no intent"},
	}
	svc, s := newStubbedContextService(t, cls, nil, true)
	sess, agent := seedSession(t, s, "hi")

	// First turn: 3 tools.
	first, err := svc.AssembleSlots(context.Background(), sess, agent, tools3(), "", 200000, "")
	if err != nil {
		t.Fatalf("AssembleSlots first: %v", err)
	}
	firstHash := first.ToolCache.SelectionHash

	// Second turn: selection changes (drop dev_bash).
	reduced := tools3()[:2]
	second, err := svc.AssembleSlots(context.Background(), sess, agent, reduced, "", 200000, "")
	if err != nil {
		t.Fatalf("AssembleSlots second: %v", err)
	}
	if second.ToolCache.SelectionHash == firstHash {
		t.Fatalf("selection hash should change when tool set changes")
	}
}

func TestAssembleSlots_S3b_DisabledFallsBackToS3a(t *testing.T) {
	cls := &scriptedClassifier{
		result: intent.Result{Hydrate: false, Source: intent.SourceRules},
	}
	svc, s := newStubbedContextService(t, cls, nil, false /* cacheEnabled */)
	sess, agent := seedSession(t, s, "run the tests")

	r, err := svc.AssembleSlots(context.Background(), sess, agent, tools3(), "", 200000, "")
	if err != nil {
		t.Fatalf("AssembleSlots: %v", err)
	}
	if r.ToolCache != nil {
		t.Fatalf("S3a fallback should not produce a ToolCache outcome; got %+v", r.ToolCache)
	}
	if cls.calls != 0 {
		t.Fatalf("classifier must not be invoked when cache is disabled; calls=%d", cls.calls)
	}
	// Tools slot must be full JSON (S3a behavior).
	var defs []llmtypes.ToolDefinition
	if err := json.Unmarshal([]byte(r.Window.Slot("tools").Content), &defs); err != nil {
		t.Fatalf("S3a slot should be JSON-marshaled defs: %v", err)
	}
	if len(defs) != 3 {
		t.Fatalf("expected 3 defs in S3a slot, got %d", len(defs))
	}
}

func TestAssembleSlots_S3b_StateTransitionTracking(t *testing.T) {
	cls := &scriptedClassifier{
		result: intent.Result{Hydrate: false, Source: intent.SourceRules},
	}
	svc, s := newStubbedContextService(t, cls, nil, true)
	sess, agent := seedSession(t, s, "hi")

	first, _ := svc.AssembleSlots(context.Background(), sess, agent, tools3(), "", 200000, "")
	if first.ToolCache.Prev != StatePointer || first.ToolCache.Next != StatePointer {
		t.Fatalf("fresh session should go pointer→pointer; got %v→%v", first.ToolCache.Prev, first.ToolCache.Next)
	}

	// Next turn: classifier flips to hydrate.
	cls.result = intent.Result{Hydrate: true, Source: intent.SourceRules, Reasoning: "run"}
	second, _ := svc.AssembleSlots(context.Background(), sess, agent, tools3(), "", 200000, "")
	if second.ToolCache.Prev != StatePointer || second.ToolCache.Next != StateFull {
		t.Fatalf("transition should be pointer→full; got %v→%v", second.ToolCache.Prev, second.ToolCache.Next)
	}
}

// TestAssembleSlots_S3b_TokensBeforeReflectsPriorSlot locks in the fix for
// Copilot review #3095049986. Prior implementation reported the pointer-
// summary size as TokensBefore even on full→pointer transitions, producing
// misleading "tokens saved" deltas. The test stores per-session slot sizes
// and asserts every transition reports the actual prior slot's tokens.
func TestAssembleSlots_S3b_TokensBeforeReflectsPriorSlot(t *testing.T) {
	cls := &scriptedClassifier{
		result: intent.Result{Hydrate: true, Categories: nil, Source: intent.SourceRules, Reasoning: "hydrate all"},
	}
	svc, s := newStubbedContextService(t, cls, nil, true)
	sess, agent := seedSession(t, s, "run tests")

	// Turn 1: full hydration. Records the full-slot size as prior-state.
	first, err := svc.AssembleSlots(context.Background(), sess, agent, tools3(), "", 200000, "")
	if err != nil {
		t.Fatalf("turn 1: %v", err)
	}
	if first.ToolCache.Next != StateFull {
		t.Fatalf("turn 1 expected StateFull, got %v", first.ToolCache.Next)
	}
	fullTokens := first.ToolCache.TokensAfter

	// Turn 2: classifier flips to no-hydrate. TokensBefore must be the full
	// slot's size (what we previously emitted), not the pointer summary's —
	// that's the exact bug the fix addresses.
	cls.result = intent.Result{Hydrate: false, Source: intent.SourceRules, Reasoning: "ambient"}
	second, err := svc.AssembleSlots(context.Background(), sess, agent, tools3(), "", 200000, "")
	if err != nil {
		t.Fatalf("turn 2: %v", err)
	}
	if second.ToolCache.Prev != StateFull || second.ToolCache.Next != StatePointer {
		t.Fatalf("turn 2 expected full→pointer; got %v→%v", second.ToolCache.Prev, second.ToolCache.Next)
	}
	if second.ToolCache.TokensBefore != fullTokens {
		t.Fatalf("TokensBefore on full→pointer should equal prior slot tokens (%d); got %d", fullTokens, second.ToolCache.TokensBefore)
	}

	// Turn 3: back to full. TokensBefore should now equal the pointer size
	// from turn 2.
	pointerTokens := second.ToolCache.TokensAfter
	cls.result = intent.Result{Hydrate: true, Categories: nil, Source: intent.SourceRules, Reasoning: "hydrate all"}
	third, err := svc.AssembleSlots(context.Background(), sess, agent, tools3(), "", 200000, "")
	if err != nil {
		t.Fatalf("turn 3: %v", err)
	}
	if third.ToolCache.Prev != StatePointer || third.ToolCache.Next != StateFull {
		t.Fatalf("turn 3 expected pointer→full; got %v→%v", third.ToolCache.Prev, third.ToolCache.Next)
	}
	if third.ToolCache.TokensBefore != pointerTokens {
		t.Fatalf("TokensBefore on pointer→full should equal prior pointer tokens (%d); got %d", pointerTokens, third.ToolCache.TokensBefore)
	}
}

func TestAssembleSlots_S3b_ClassifierErrorStillRenders(t *testing.T) {
	// When the classifier returns an error, contextServiceImpl currently still
	// honors the returned Result (BrokerClassifier's fail-open pattern applies
	// upstream). Here we exercise the scripted case where the classifier
	// returns a fail-open Result with err=non-nil.
	cls := &scriptedClassifier{
		result: intent.Result{Hydrate: true, Categories: nil, Source: intent.SourceFallback, Reasoning: "Fail-open: boom"},
		err:    errors.New("simulated downstream"),
	}
	svc, s := newStubbedContextService(t, cls, nil, true)
	sess, agent := seedSession(t, s, "find something")

	r, err := svc.AssembleSlots(context.Background(), sess, agent, tools3(), "", 200000, "")
	if err != nil {
		t.Fatalf("AssembleSlots must not fail on classifier error: %v", err)
	}
	if r.ToolCache == nil || r.ToolCache.Source != intent.SourceFallback {
		t.Fatalf("fail-open source should surface; got %+v", r.ToolCache)
	}
	if r.ToolCache.Next != StateFull {
		t.Fatalf("fail-open must hydrate all; got %v", r.ToolCache.Next)
	}
}
