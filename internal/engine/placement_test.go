package engine

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/provider/failure"
	"github.com/hollis-labs/nanite/internal/provider/fake"
	"github.com/hollis-labs/nanite/internal/slots"
)

func TestPerTurnContextKeepsThePrefixStable(t *testing.T) {
	p := &watchedProvider{Provider: fake.New(fake.Text("one"), fake.Text("two"))}
	e, db, id := setupEngine(t, p, "")
	send := func(text, context string) {
		t.Helper()
		run, err := e.Send(t.Context(), id, Input{Text: text, Slots: []slots.Update{{Slot: slots.UserContext, Entry: slots.Entry{Owner: "host", Key: "desk", Content: context}}}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := run.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	send("first", "FOCUS-ONE on the desk")
	send("second", "FOCUS-TWO on the desk")
	first, second := p.requests[0], p.requests[1]

	// What is sent: each block's name, content and cache key. (Changed is
	// a flag about the previous turn, true for everything on the first.)
	systemOf := func(r llmtypes.ChatRequest) string {
		var b strings.Builder
		for _, block := range r.SlotBlocks {
			b.WriteString(block.Name + "\x00" + block.Content + "\x00" + block.CacheKey + "\x00")
		}
		return b.String()
	}
	if systemOf(first) != systemOf(second) {
		t.Fatalf("system blocks changed between turns:\n%s\n%s", systemOf(first), systemOf(second))
	}
	if slices.ContainsFunc(first.SlotBlocks, func(b llmtypes.SlotBlock) bool { return b.Name == string(slots.UserContext) }) {
		t.Fatal("user_context is in the system blocks")
	}
	m1, _ := json.Marshal(first.Messages[0])
	m2, _ := json.Marshal(second.Messages[0])
	if string(m1) != string(m2) {
		t.Fatalf("the first message changed between turns:\n%s\n%s", m1, m2)
	}
	if !strings.Contains(string(m1), "FOCUS-ONE") {
		t.Fatal("the first turn's context is not with its message")
	}
	newest := second.Messages[len(second.Messages)-1]
	if len(newest.ContentBlocks) != 2 || newest.ContentBlocks[0].Name != TurnContextBlock || !strings.Contains(newest.ContentBlocks[0].Text, "FOCUS-TWO") || newest.ContentBlocks[1].Text != "second" {
		t.Fatalf("newest message %+v", newest.ContentBlocks)
	}
	history, err := db.History(t.Context(), id)
	if err != nil || history[0].Blocks[0].Name != TurnContextBlock {
		t.Fatalf("stored message %+v, %v", history[0].Blocks, err)
	}
}

func TestSlotPlacementOverride(t *testing.T) {
	p := &watchedProvider{Provider: fake.New(fake.Text("ok"))}
	e, _, id := setupEngine(t, p, "")
	e.cfg.SlotPlacement = map[slots.Name]slots.Placement{slots.UserContext: slots.InSystem, slots.Context: slots.InTurn, slots.Universal: slots.InTurn}
	run, err := e.Send(t.Context(), id, Input{Text: "hi", Slots: []slots.Update{
		{Slot: slots.UserContext, Entry: slots.Entry{Owner: "host", Key: "u", Content: "USER-CTX"}},
		{Slot: slots.Context, Entry: slots.Entry{Owner: "host", Key: "c", Content: "HOST-CTX"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	req := p.requests[0]
	inSystem := func(name slots.Name) bool {
		return slices.ContainsFunc(req.SlotBlocks, func(b llmtypes.SlotBlock) bool { return b.Name == string(name) })
	}
	if !inSystem(slots.UserContext) || inSystem(slots.Context) || !inSystem(slots.Universal) {
		t.Fatalf("placement not applied: %v", req.SlotBlocks)
	}
	if first := req.Messages[0].ContentBlocks[0]; first.Name != TurnContextBlock || !strings.Contains(first.Text, "HOST-CTX") {
		t.Fatalf("context not in the turn: %+v", first)
	}
}

func TestStoredContextIsNotRepeated(t *testing.T) {
	// Context a host keeps in the slot store (not passed per turn) goes in
	// the turn when it changes, not on every message.
	p := &watchedProvider{Provider: fake.New(fake.Text("one"), fake.Text("two"), fake.Text("three"))}
	e, _, id := setupEngine(t, p, "")
	put := func(content string) {
		t.Helper()
		if err := e.cfg.Slots.Put(slots.UserContext, slots.Entry{Owner: "host", Key: "desk", Content: content}); err != nil {
			t.Fatal(err)
		}
	}
	send := func(text string) {
		t.Helper()
		run, err := e.Send(t.Context(), id, Input{Text: text})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := run.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	put("STORED-ONE")
	send("first")
	send("second")
	put("STORED-TWO")
	send("third")
	contextOf := func(r llmtypes.ChatRequest) string {
		newest := r.Messages[len(r.Messages)-1]
		if len(newest.ContentBlocks) > 0 && newest.ContentBlocks[0].Name == TurnContextBlock {
			return newest.ContentBlocks[0].Text
		}
		return ""
	}
	if !strings.Contains(contextOf(p.requests[0]), "STORED-ONE") || contextOf(p.requests[1]) != "" || !strings.Contains(contextOf(p.requests[2]), "STORED-TWO") {
		t.Fatalf("turn contexts: %q / %q / %q", contextOf(p.requests[0]), contextOf(p.requests[1]), contextOf(p.requests[2]))
	}
}

func TestStoredContextReturnsAfterCompaction(t *testing.T) {
	// Turn one answers normally; turn two overflows, compacts, and answers.
	overflow := fake.Response{Err: &failure.Error{Class: failure.Overflow, Cause: errors.New("typed overflow")}}
	p := &watchedProvider{Provider: fake.New(fake.Text("one"), overflow, fake.Text("after compaction"), fake.Text("third"))}
	e, db, id := setupEngine(t, p, "")
	seedLongHistory(t, db, id)
	if err := e.cfg.Slots.Put(slots.UserContext, slots.Entry{Owner: "host", Key: "desk", Content: "STORED-DESK"}); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"first", "second", "third"} {
		run, err := e.Send(t.Context(), id, Input{Text: text})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := run.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	newest := func(r llmtypes.ChatRequest) llmtypes.ChatMessage { return r.Messages[len(r.Messages)-1] }
	carries := func(m llmtypes.ChatMessage) bool {
		return len(m.ContentBlocks) > 0 && m.ContentBlocks[0].Name == TurnContextBlock && strings.Contains(m.ContentBlocks[0].Text, "STORED-DESK")
	}
	last := p.requests[len(p.requests)-1]
	if p.summaries == 0 {
		t.Fatal("no compaction happened")
	}
	if !carries(newest(last)) {
		t.Fatalf("after compaction the stored context was not sent again: %+v", newest(last).ContentBlocks)
	}
	if carries(newest(p.requests[1])) {
		t.Fatal("unchanged context was repeated before any compaction")
	}
}
