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
	got := make([]string, 0, len(Names()))
	for _, name := range Names() {
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
		got, err := Assemble(context.Background(), snap, plan, prev, 0, est, nil)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	first := compose(nil)
	modes := first.Report[Context].Decisions
	if len(modes) != 3 || modes[0].Key != "b" || modes[0].Disposition != Ship || modes[1].Disposition != Pointer || modes[2].Disposition != Skip {
		t.Fatalf("decisions = %+v", modes)
	}
	if first.Blocks[0].SlotName != string(Universal) {
		t.Fatalf("first block = %v", first.Blocks[0])
	}
	for _, slot := range Names() {
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

func TestDynamicToolsBudgetAndHashLifecycle(t *testing.T) {
	ctx := context.Background()
	budgets := make(map[Name]int)
	for name, budget := range ctxpkg.DefaultBudgets() {
		budgets[Name(name)] = budget
	}
	if budget, ok := budgets[Tools]; !ok || budget != 0 {
		t.Fatalf("expected a dynamic tools budget, got %d (present=%v)", budget, ok)
	}
	store := NewStore()
	var hashes map[string]string
	for _, tc := range []struct {
		text    string
		changed bool
	}{
		{"tools guidance", true}, {"tools guidance", false}, {"updated tools guidance", true}, {"", true}, {"", false},
	} {
		if err := store.Put(Tools, Entry{Owner: "host", Key: "tools", Content: tc.text}); err != nil {
			t.Fatal(err)
		}
		snap := store.Snapshot()
		plan, err := DefaultSelector(nil).Select(ctx, TurnInfo{}, snap, budgets)
		if err != nil {
			t.Fatal(err)
		}
		if got := plan.Decisions[Tools][0].Disposition; got != Ship {
			t.Fatalf("tools disposition = %v", got)
		}
		assembled, err := Assemble(ctx, snap, plan, hashes, 0, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := assembled.Report[Tools].Changed; got != tc.changed {
			t.Errorf("text %q changed = %v, want %v", tc.text, got, tc.changed)
		}
		if _, ok := assembled.Hashes[string(Tools)]; ok != (tc.text != "") {
			t.Fatalf("hashes = %v for text %q", assembled.Hashes, tc.text)
		}
		if tc.text != "" && (len(assembled.Blocks) != 1 || assembled.Blocks[0].Content != tc.text) {
			t.Fatalf("tools guidance absent: %+v", assembled.Blocks)
		}
		hashes = assembled.Hashes
	}
}

func TestSnapshotWith(t *testing.T) {
	store := NewStore()
	if err := store.Put(Context, Entry{Owner: "host", Key: "original", Content: "before"}); err != nil {
		t.Fatal(err)
	}
	orig := store.Snapshot()
	labels := map[string]string{"fixture": "before"}
	updated, err := orig.With(
		Update{Slot: Context, Entry: Entry{Owner: "host", Key: "original", Content: "after"}},
		Update{Slot: Context, Entry: Entry{Owner: "host", Key: "higher", Priority: 1, Labels: labels}},
	)
	if err != nil {
		t.Fatal(err)
	}
	labels["fixture"] = "changed"
	got := updated.Entries(Context)
	if len(got) != 2 || got[0].Key != "higher" || got[0].Labels["fixture"] != "before" || got[1].Content != "after" {
		t.Fatalf("updates = %+v", got)
	}
	if len(orig.Entries(Context)) != 1 || orig.Entries(Context)[0].Content != "before" {
		t.Fatalf("original changed: %+v", orig.Entries(Context))
	}
	if _, err := orig.With(Update{Slot: Agent, Entry: Entry{Owner: "host", Key: "reserved"}}); err == nil {
		t.Fatal("accepted a reserved slot update")
	}
	names := Names()
	names[0] = Context
	if Names()[0] != Universal {
		t.Fatal("Names returned shared storage")
	}
}

func TestOversizedEntryIsTrimmedNotDropped(t *testing.T) {
	est := ctxpkg.DefaultEstimator{}
	for _, slot := range []Name{Universal, Context, UserContext} {
		t.Run(string(slot), func(t *testing.T) {
			owner := "host"
			if slot == Universal {
				owner = "engine"
			}
			store := NewStore()
			snap, err := store.Snapshot().With(Update{Slot: slot, Entry: Entry{Owner: owner, Key: "big", Content: "BEGIN " + strings.Repeat("context word ", 2000)}})
			if err != nil {
				t.Fatal(err)
			}
			budgets := map[Name]int{slot: 200}
			plan, err := DefaultSelector(est).Select(context.Background(), TurnInfo{}, snap, budgets)
			if err != nil {
				t.Fatal(err)
			}
			got, err := Assemble(context.Background(), snap, plan, nil, 0, est, budgets)
			if err != nil {
				t.Fatal(err)
			}
			d := got.Report[slot].Decisions
			if len(d) != 1 || d[0].Disposition != Trim || d[0].Reason == "" {
				t.Fatalf("decisions %+v", d)
			}
			var content string
			for _, b := range got.Blocks {
				if b.SlotName == string(slot) {
					content = b.Content
				}
			}
			if !strings.HasPrefix(content, "BEGIN ") || !strings.Contains(content, "[trimmed to fit the "+string(slot)+" slot budget of 200 tokens]") || est.Estimate(content) > 200 {
				t.Fatalf("content (%d tokens): %.80q ... %q", est.Estimate(content), content, content[max(0, len(content)-80):])
			}
		})
	}
}

func TestNoRoomLeftIsReportedSkip(t *testing.T) {
	est := ctxpkg.DefaultEstimator{}
	snap, err := NewStore().Snapshot().With(
		Update{Slot: Context, Entry: Entry{Owner: "host", Key: "a", Priority: 10, Content: strings.Repeat("first ", 150)}},
		Update{Slot: Context, Entry: Entry{Owner: "host", Key: "b", Content: strings.Repeat("second ", 100)}},
	)
	if err != nil {
		t.Fatal(err)
	}
	budgets := map[Name]int{Context: 240}
	plan, err := DefaultSelector(est).Select(context.Background(), TurnInfo{}, snap, budgets)
	if err != nil {
		t.Fatal(err)
	}
	d := plan.Decisions[Context]
	if len(d) != 2 || d[0].Disposition != Ship || d[1].Disposition != Skip || d[1].Reason != "no room left in slot budget" {
		t.Fatalf("decisions %+v", d)
	}
}

func TestTrimEdgeCases(t *testing.T) {
	est := ctxpkg.DefaultEstimator{}
	assemble := func(t *testing.T, snap Snapshot, plan Plan, budgets map[Name]int, prev map[string]string) Assembled {
		t.Helper()
		got, err := Assemble(context.Background(), snap, plan, prev, 0, est, budgets)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	content := func(a Assembled, slot Name) string {
		for _, b := range a.Blocks {
			if b.SlotName == string(slot) {
				return b.Content
			}
		}
		return ""
	}
	budgets := map[Name]int{Context: 200}

	t.Run("invalid UTF-8 stays a byte prefix", func(t *testing.T) {
		raw := "BEGIN \xff\xfe " + strings.Repeat("bytes \xc3 ", 900)
		snap, _ := NewStore().Snapshot().With(Update{Slot: Context, Entry: Entry{Owner: "host", Key: "k", Content: raw}})
		plan, _ := DefaultSelector(est).Select(context.Background(), TurnInfo{}, snap, budgets)
		got := content(assemble(t, snap, plan, budgets, nil), Context)
		cut := got[:strings.Index(got, "\n[trimmed")]
		if !strings.HasPrefix(raw, cut) || len(cut) == 0 {
			t.Fatalf("shipped text is not a prefix of the original")
		}
	})

	t.Run("a Trim that fits ships unchanged", func(t *testing.T) {
		snap, _ := NewStore().Snapshot().With(Update{Slot: Context, Entry: Entry{Owner: "host", Key: "k", Content: "small"}})
		plan := Plan{Decisions: map[Name][]Decision{Context: {{Owner: "host", Key: "k", Disposition: Trim}}}}
		if got := content(assemble(t, snap, plan, budgets, nil), Context); got != "small" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("several entries with headers, stable across turns", func(t *testing.T) {
		snap, _ := NewStore().Snapshot().With(
			Update{Slot: Context, Entry: Entry{Owner: "host", Key: "a", Priority: 10, Content: strings.Repeat("alpha ", 60)}},
			Update{Slot: Context, Entry: Entry{Owner: "host", Key: "b", Content: strings.Repeat("beta ", 400)}},
		)
		plan, _ := DefaultSelector(est).Select(context.Background(), TurnInfo{}, snap, budgets)
		first := assemble(t, snap, plan, budgets, nil)
		text := content(first, Context)
		if !strings.Contains(text, "### host/a") || !strings.Contains(text, "### host/b") || !strings.Contains(text, "[trimmed") || est.Estimate(text) > 200 {
			t.Fatalf("content (%d tokens): %q", est.Estimate(text), text)
		}
		second := assemble(t, snap, plan, budgets, first.Hashes)
		if second.Report[Context].Changed || content(second, Context) != text {
			t.Fatal("a stable trim changed between turns")
		}
	})
}

// A reserved entry always ships at least its Pointer: higher-priority entries
// that would fill the slot are trimmed to leave room for it.
func TestReservedPointerAlwaysShips(t *testing.T) {
	est := ctxpkg.DefaultEstimator{}
	budgets := map[Name]int{Session: 240}
	assemble := func(t *testing.T, entries ...Entry) (Assembled, string) {
		t.Helper()
		var updates []Update
		for _, e := range entries {
			updates = append(updates, Update{Slot: Session, Entry: e})
		}
		snap, err := NewStore().Snapshot().With(updates...)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := DefaultSelector(est).Select(context.Background(), TurnInfo{}, snap, budgets)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Assemble(context.Background(), snap, plan, nil, 0, est, budgets)
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range got.Blocks {
			if b.SlotName == string(Session) {
				return got, b.Content
			}
		}
		return got, ""
	}
	mail := Entry{Owner: "engine", Key: "mail", Content: "MAIL " + strings.Repeat("body ", 400), Pointer: "[mail m1 withheld]", Reserve: true}
	filler := Entry{Owner: "host", Key: "filler", Priority: 10, Content: strings.Repeat("filler ", 300)}

	t.Run("crowded to under the trim minimum", func(t *testing.T) {
		got, content := assemble(t, filler, mail)
		d := got.Report[Session].Decisions
		if len(d) != 2 || d[0].Disposition != Trim || d[1].Disposition != Pointer {
			t.Fatalf("decisions %+v", d)
		}
		if !strings.Contains(content, "[mail m1 withheld]") || est.Estimate(content) > 240 {
			t.Fatalf("content (%d tokens) lacks pointer: %q", est.Estimate(content), content[max(0, len(content)-120):])
		}
	})
	t.Run("room for a trim prefers the trim", func(t *testing.T) {
		got, content := assemble(t, mail)
		d := got.Report[Session].Decisions
		if len(d) != 1 || d[0].Disposition != Trim || !strings.HasPrefix(content, "MAIL ") {
			t.Fatalf("decisions %+v content %.40q", d, content)
		}
	})
	t.Run("fits whole", func(t *testing.T) {
		small := mail
		small.Content = "MAIL short"
		got, content := assemble(t, filler, small)
		d := got.Report[Session].Decisions
		if d[1].Disposition != Ship || !strings.Contains(content, "MAIL short") {
			t.Fatalf("decisions %+v", d)
		}
	})
	t.Run("without reserve the mail is skipped", func(t *testing.T) {
		plain := mail
		plain.Reserve, plain.Pointer = false, ""
		got, _ := assemble(t, filler, plain)
		if d := got.Report[Session].Decisions; d[1].Disposition != Skip {
			t.Fatalf("decisions %+v", d)
		}
	})
}
