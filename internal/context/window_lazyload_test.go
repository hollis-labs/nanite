package context

import (
	"strings"
	"testing"
)

// Glass-5 (CW-20260502-0012) — slot-level LazyLoad/LoadHint primitive
// tests. The actual skill use case (chat package) inlines the LoadHint
// inside the Agent slot's content; these tests pin the forward
// infrastructure so future tickets that flip whole slots to lazy-load
// (e.g., Memory or Context in pointer mode) get a clean swap.

func TestAssemble_LazyLoadShipsLoadHint(t *testing.T) {
	cw := NewContextWindow(100_000, nil)
	cw.SetContent(SlotMemory, strings.Repeat("memory body ", 1000))
	cw.SetFlags(SlotMemory, SlotFlags{
		LazyLoad: true,
		LoadHint: "[memory pointer — call tesseract_recall(query:\"<term>\") to load]",
	})

	blocks := cw.Assemble()
	var memBlock *SlotBlock
	for i := range blocks {
		if blocks[i].SlotName == SlotMemory {
			memBlock = &blocks[i]
			break
		}
	}
	if memBlock == nil {
		t.Fatal("memory block missing from assembled output")
	}
	if !strings.HasPrefix(memBlock.Content, "[memory pointer") {
		t.Errorf("expected LoadHint in shipped content, got: %q", memBlock.Content)
	}
	if strings.Contains(memBlock.Content, "memory body") {
		t.Errorf("LazyLoad must not ship full content, got: %q", memBlock.Content)
	}
	if memBlock.CacheKey != ComputeCacheKey(memBlock.Content) {
		t.Errorf("cache key must be derived from rendered content")
	}
}

func TestAssemble_LazyLoadEmptyHintFallsThrough(t *testing.T) {
	// LazyLoad=true with empty LoadHint is a no-op — the slot still ships
	// its full content. (Defensive: callers must set both.)
	cw := NewContextWindow(100_000, nil)
	cw.SetContent(SlotMemory, "actual memory body")
	cw.SetFlags(SlotMemory, SlotFlags{LazyLoad: true, LoadHint: ""})

	blocks := cw.Assemble()
	for _, b := range blocks {
		if b.SlotName != SlotMemory {
			continue
		}
		if b.Content != "actual memory body" {
			t.Errorf("expected full content fallthrough when LoadHint empty, got: %q", b.Content)
		}
		return
	}
	t.Fatal("memory block missing")
}

func TestAssemble_LazyLoadEmitsBlockEvenWhenContentEmpty(t *testing.T) {
	// A slot that's only ever lazy-loaded (no underlying Content set yet)
	// still emits when a LoadHint is present — the agent gets the pointer.
	cw := NewContextWindow(100_000, nil)
	cw.SetFlags(SlotMemory, SlotFlags{
		LazyLoad: true,
		LoadHint: "[memory pointer]",
	})

	blocks := cw.Assemble()
	for _, b := range blocks {
		if b.SlotName == SlotMemory {
			if b.Content != "[memory pointer]" {
				t.Errorf("expected LoadHint when content empty, got: %q", b.Content)
			}
			return
		}
	}
	t.Fatal("memory block missing — LazyLoad with empty Content should still emit")
}

func TestAssemble_LazyLoadCacheInvalidatesOnToggle(t *testing.T) {
	// Toggling LazyLoad on/off across turns must invalidate the slot
	// cache so the cache layer doesn't ship stale shape.
	cw := NewContextWindow(100_000, nil)
	cw.SetContent(SlotMemory, "full memory body")

	// Turn 1 — full content shipped.
	blocks := cw.Assemble()
	var t1Key string
	for _, b := range blocks {
		if b.SlotName == SlotMemory {
			t1Key = b.CacheKey
		}
	}
	if t1Key == "" {
		t.Fatal("memory block missing on turn 1")
	}

	// Turn 2 — flip LazyLoad on, same underlying content.
	cw.SetFlags(SlotMemory, SlotFlags{LazyLoad: true, LoadHint: "[pointer]"})
	blocks = cw.Assemble()
	for _, b := range blocks {
		if b.SlotName == SlotMemory {
			if b.CacheKey == t1Key {
				t.Errorf("cache key should change when LazyLoad toggles on")
			}
			if !b.Changed {
				t.Errorf("slot should report Changed=true on LazyLoad toggle")
			}
		}
	}
}
