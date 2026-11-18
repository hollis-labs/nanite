package slots

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	ctxpkg "github.com/hollis-labs/libs/agent/core/context"
)

func TestNames(t *testing.T) {
	got := make([]string, 0, len(names()))
	for _, name := range names() {
		got = append(got, string(name))
	}
	if !slices.Equal(got, ctxpkg.SlotOrder) {
		t.Fatalf("order = %v, want %v", got, ctxpkg.SlotOrder)
	}
}

func TestConcurrentSnapshots(t *testing.T) {
	store := NewStore()
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := range 100 {
			if err := store.Replace(Context, "notes", Entry{Key: "a", Content: fmt.Sprint(i)}, Entry{Key: "b", Content: fmt.Sprint(i)}); err != nil {
				t.Error(err)
			}
		}
	})
	wg.Go(func() {
		for i := range 100 {
			if err := store.Put(Context, Entry{Owner: "files", Key: "entry", Content: fmt.Sprint(i)}); err != nil {
				t.Error(err)
			}
		}
	})
	wg.Go(func() {
		for range 100 {
			var notes []Entry
			for _, e := range store.Snapshot().Entries(Context) {
				if e.Owner == "notes" {
					notes = append(notes, e)
				}
			}
			if len(notes) != 0 && (len(notes) != 2 || notes[0].Content != notes[1].Content) {
				t.Errorf("partial replace: %v", notes)
			}
		}
	})
	wg.Wait()
}

func TestReservedSlots(t *testing.T) {
	store := NewStore()
	for _, slot := range []Name{Universal, Agent, Permissions, Handoff, Conversation} {
		if err := store.Put(slot, Entry{Owner: "host", Key: "test"}); err == nil {
			t.Errorf("host wrote %s", slot)
		}
		if err := store.Replace(slot, "host", Entry{Key: "test"}); err == nil {
			t.Errorf("host replaced %s", slot)
		}
	}
}

func TestSelectionAndChanges(t *testing.T) {
	store := NewStore()
	put := func(slot Name, e Entry) {
		t.Helper()
		if err := store.Put(slot, e); err != nil {
			t.Fatal(err)
		}
	}
	put(Universal, Entry{Owner: "engine", Key: "rules", Content: "universal rules"})
	put(Context, Entry{Owner: "notes", Key: "a", Content: strings.Repeat("word ", 100), Pointer: "pointer"})
	put(Context, Entry{Owner: "notes", Key: "b", Priority: 10, Content: "first"})
	put(Context, Entry{Owner: "notes", Key: "c", Content: strings.Repeat("word ", 100)})
	put(Agent, Entry{Owner: "file", Key: "agent", Content: strings.Repeat("instructions ", 9000)})
	compose := func(prev map[string]string) Assembled {
		t.Helper()
		snap := store.Snapshot()
		est := ctxpkg.DefaultEstimator{}
		plan, err := DefaultSelector(est).Select(context.Background(), TurnInfo{}, snap, map[Name]int{Context: 16})
		if err != nil {
			t.Fatal(err)
		}
		got, err := Assemble(context.Background(), snap, plan, prev, 0, est)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	first := compose(nil)
	modes := first.Report[Context].Decisions
	if len(modes) != 3 || modes[0].Key != "b" || modes[0].Mode != ModeShip || modes[1].Mode != ModePointer || modes[2].Mode != ModeSkip {
		t.Fatalf("decisions = %+v", modes)
	}
	if first.Blocks[0].SlotName != string(Universal) {
		t.Fatalf("first block = %v", first.Blocks[0])
	}
	for _, slot := range names() {
		state := first.Window.Slot(string(slot))
		if slot != Agent && state.MaxTokens > 0 && state.TokenCount > state.MaxTokens {
			t.Fatalf("oversized slot %s", slot)
		}
	}
	put(Memory, Entry{Owner: "notes", Key: "a", Content: "remember"})
	next := compose(first.Window.PrevHashes)
	if next.Blocks[0].Content != first.Blocks[0].Content {
		t.Fatal("universal changed")
	}
	for slot, detail := range next.Report {
		if detail.Changed != (slot == Memory) {
			t.Errorf("%s changed = %v", slot, detail.Changed)
		}
	}
	store.Remove(Memory, "notes", "a")
	if !compose(next.Window.PrevHashes).Report[Memory].Changed {
		t.Fatal("clearing a slot must report a change")
	}
}
