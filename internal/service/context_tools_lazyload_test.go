package service

import (
	"context"
	"strings"
	"testing"

	ctxpkg "github.com/hollis-labs/nanite/internal/context"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/tool/intent"
)

// G-HOT-SWAP-DEAD activation tests at the slot-assembly seam.
// PartitionTools itself is unit-tested in chat/tool_partition_test.go;
// these tests verify the AssembleSlots-side wiring: toolsLazyHint is
// appended to the Tools slot, CacheKey shifts when the hint changes,
// and the empty-hint case is a no-op.

func TestAssembleSlots_LazyHintAppendedToToolsSlot(t *testing.T) {
	// S3a path (tool-cache disabled) keeps the test deterministic — we want
	// to assert the hint-append, not interact with the classifier.
	svc, s := newStubbedContextService(t, nil, nil, false)
	sess, agent := seedSession(t, s, "test lazy append")

	hint := "[Tool catalog (lazy): 5 tools available. Use `request_tools` ...]"
	r, err := svc.AssembleSlots(context.Background(), sess, agent, &store.AgentMode{}, nil, tools3(), "", 200000, nil, hint)
	if err != nil {
		t.Fatalf("AssembleSlots: %v", err)
	}

	var toolsBlock string
	for _, b := range r.Blocks {
		if b.SlotName == "tools" {
			toolsBlock = b.Content
			break
		}
	}
	if toolsBlock == "" {
		t.Fatal("tools block missing from assembled output")
	}
	if !strings.Contains(toolsBlock, hint) {
		t.Errorf("tools slot should contain lazy hint; got %q", toolsBlock)
	}
	if !strings.Contains(toolsBlock, "dev_grep") {
		t.Errorf("tools slot should still contain inline tool defs; got %q", toolsBlock)
	}
}

func TestAssembleSlots_EmptyLazyHintIsNoOp(t *testing.T) {
	svc, s := newStubbedContextService(t, nil, nil, false)
	sess, agent := seedSession(t, s, "test empty hint")

	// Two runs: with empty hint and with no hint should produce identical
	// Tools-slot content. Empty string must not introduce a trailing "\n\n".
	rA, err := svc.AssembleSlots(context.Background(), sess, agent, &store.AgentMode{}, nil, tools3(), "", 200000, nil, "")
	if err != nil {
		t.Fatalf("AssembleSlots A: %v", err)
	}
	rB, err := svc.AssembleSlots(context.Background(), sess, agent, &store.AgentMode{}, nil, tools3(), "", 200000, nil, "")
	if err != nil {
		t.Fatalf("AssembleSlots B: %v", err)
	}
	contentA := slotContent(rA.Blocks, "tools")
	contentB := slotContent(rB.Blocks, "tools")
	if contentA != contentB {
		t.Errorf("empty-hint runs should produce identical content; got %q vs %q", contentA, contentB)
	}
	if strings.HasSuffix(contentA, "\n\n") {
		t.Errorf("empty hint must not introduce trailing newlines; got %q", contentA)
	}
}

func TestAssembleSlots_LazyHintInvalidatesCacheKey(t *testing.T) {
	// Toggling the hint between turns must shift the Tools slot's CacheKey
	// — providers' prompt cache should NOT serve stale shape.
	svc, s := newStubbedContextService(t, nil, nil, false)
	sess, agent := seedSession(t, s, "test cache key")

	// Turn 1: no hint.
	r1, err := svc.AssembleSlots(context.Background(), sess, agent, &store.AgentMode{}, nil, tools3(), "", 200000, nil, "")
	if err != nil {
		t.Fatalf("AssembleSlots t1: %v", err)
	}
	t1Key := slotCacheKey(r1.Blocks, "tools")

	// Turn 2: same tools, different (non-empty) hint.
	r2, err := svc.AssembleSlots(context.Background(), sess, agent, &store.AgentMode{}, nil, tools3(), "", 200000, nil, "[lazy hint v1]")
	if err != nil {
		t.Fatalf("AssembleSlots t2: %v", err)
	}
	t2Key := slotCacheKey(r2.Blocks, "tools")
	if t1Key == t2Key {
		t.Errorf("cache key should change when lazy hint toggles on; got %q == %q", t1Key, t2Key)
	}

	// Turn 3: same hint as turn 2 (cache key matches t2).
	r3, err := svc.AssembleSlots(context.Background(), sess, agent, &store.AgentMode{}, nil, tools3(), "", 200000, nil, "[lazy hint v1]")
	if err != nil {
		t.Fatalf("AssembleSlots t3: %v", err)
	}
	t3Key := slotCacheKey(r3.Blocks, "tools")
	if t2Key != t3Key {
		t.Errorf("cache key should be stable across identical hints; got %q != %q", t2Key, t3Key)
	}

	// Turn 4: different hint string → key shifts again.
	r4, err := svc.AssembleSlots(context.Background(), sess, agent, &store.AgentMode{}, nil, tools3(), "", 200000, nil, "[lazy hint v2]")
	if err != nil {
		t.Fatalf("AssembleSlots t4: %v", err)
	}
	t4Key := slotCacheKey(r4.Blocks, "tools")
	if t3Key == t4Key {
		t.Errorf("cache key should change when lazy hint changes; got %q == %q", t3Key, t4Key)
	}
}

func TestAssembleSlots_LazyHintWithS3bPointerStillAppends(t *testing.T) {
	// When S3b classifier picks pointer mode and a lazy hint is also
	// supplied, both pointer + hint should appear (pointer first, hint
	// after a blank line).
	cls := &scriptedClassifier{result: intent.Result{Hydrate: false, Source: intent.SourceRules}}
	svc, s := newStubbedContextService(t, cls, nil, true)
	sess, agent := seedSession(t, s, "trigger pointer mode")

	hint := "[lazy partition hint]"
	r, err := svc.AssembleSlots(context.Background(), sess, agent, &store.AgentMode{}, nil, tools3(), "", 200000, nil, hint)
	if err != nil {
		t.Fatalf("AssembleSlots: %v", err)
	}
	if r.ToolCache == nil || r.ToolCache.Next != StatePointer {
		t.Fatalf("expected StatePointer outcome, got %v", r.ToolCache)
	}
	content := slotContent(r.Blocks, "tools")
	if !strings.Contains(content, hint) {
		t.Errorf("pointer + lazy-hint compose: missing hint; got %q", content)
	}
	// S3b's pointer summary should still be present (the pointer comes
	// from stash.SummaryText; presence is verified by non-empty content
	// before the hint).
	hintIdx := strings.Index(content, hint)
	if hintIdx <= 0 {
		t.Errorf("hint should be appended after S3b pointer content, not at start; idx=%d content=%q", hintIdx, content)
	}
}

// helpers

func slotContent(blocks []ctxpkg.SlotBlock, name string) string {
	for _, b := range blocks {
		if b.SlotName == name {
			return b.Content
		}
	}
	return ""
}

func slotCacheKey(blocks []ctxpkg.SlotBlock, name string) string {
	for _, b := range blocks {
		if b.SlotName == name {
			return b.CacheKey
		}
	}
	return ""
}
