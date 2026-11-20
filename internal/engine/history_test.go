package engine

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/provider/fake"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/tools"
)

// messagesText joins every text block a request sent, to look for a note.
func messagesText(req llmtypes.ChatRequest) string {
	var b strings.Builder
	for _, m := range req.Messages {
		b.WriteString(m.Content)
		for _, block := range m.ContentBlocks {
			b.WriteString(block.Text)
			b.WriteString(block.Content)
		}
	}
	return b.String()
}

func requestTools(req llmtypes.ChatRequest) []string {
	var names []string
	for _, tool := range req.Tools {
		names = append(names, tool.Name)
	}
	return names
}

// A root conversation finds and reads a message compaction summarized away,
// and the compacted summary tells it how.
func TestRootConversationReadsMessagesCompactionSummarized(t *testing.T) {
	p := overflowThen(
		callWithID("history_search", "call-search", map[string]any{"query": "codename"}),
		callWithID("history_get", "call-get", map[string]any{"from": 1, "to": 1}),
		fake.Text("the codename is ALBATROSS"))
	e, db, id := setupEngine(t, p, "")
	seed := []string{"remember: the codename is ALBATROSS"}
	for range 7 {
		seed = append(seed, strings.Repeat("older words ", 100))
	}
	appendParent(t, db, id, seed...)
	run, err := e.Send(t.Context(), id, Input{Text: "what was the codename?"})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(compactionEvents(t, run), "; "); got != "started; done shrank=true" {
		t.Fatalf("compaction events: %q", got)
	}
	if len(p.requests) < 2 {
		t.Fatalf("requests: %d", len(p.requests))
	}
	after := messagesText(p.requests[1])
	if strings.Contains(after, "remember: the codename") {
		t.Fatal("the old message was not compacted away")
	}
	if !strings.Contains(after, "history_search") || !strings.Contains(after, "history_get") || !strings.Contains(after, "(messages #1-#") {
		t.Fatalf("the summary does not point at the history tools and range:\n%s", after)
	}
	results := toolResults(t, db, id)
	if len(results) != 2 {
		t.Fatalf("tool results: %+v", results)
	}
	if got := results[0].Content; results[0].IsError || !strings.Contains(got, "[#1 user") || !strings.Contains(got, "[codename]") {
		t.Fatalf("search did not find message #1: %q", got)
	}
	if got := results[1].Content; results[1].IsError || !strings.Contains(got, "[#1 user] remember: the codename is ALBATROSS") {
		t.Fatalf("get did not return message #1: %q", got)
	}
}

// newConversation adds a conversation, a subtask's when parent is set.
func newConversation(t *testing.T, db *store.Store, parent string) string {
	t.Helper()
	now := time.Now().UTC()
	id := store.NewID(now)
	role := "chat"
	if parent != "" {
		role = "subagent"
	}
	if err := db.CreateConversation(t.Context(), store.Conversation{ID: id, ParentID: parent, Role: role, Status: "active", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	return id
}

// The history tools and API read only their own conversation: never
// another root conversation, and never a subtask's.
func TestHistoryStaysInItsConversation(t *testing.T) {
	p := &watchedProvider{Provider: fake.New()}
	e, db, here := setupEngine(t, p, "")
	other := newConversation(t, db, "")
	child := newConversation(t, db, here)
	appendParent(t, db, other, "the launch codename is HERON")
	appendParent(t, db, child, "a subtask saw the codename KESTREL")
	appendParent(t, db, here, "nothing about birds here")

	h := (&execution{engine: e, info: store.Conversation{ID: here}}).history()
	if got, err := h.Search(t.Context(), "codename", 5); err != nil || got != "No messages match." {
		t.Fatalf("search reached another conversation: %q, %v", got, err)
	}
	if got, err := h.Get(t.Context(), 1, 1); err != nil || !strings.Contains(got, "nothing about birds") {
		t.Fatalf("get: %q, %v", got, err)
	}
	// The host API refuses a subtask's conversation.
	if _, err := e.SearchHistory(t.Context(), child, "codename", 5); err == nil || !strings.Contains(err.Error(), "not a root conversation") {
		t.Fatalf("SearchHistory on a subtask: %v", err)
	}
	if _, err := e.HistoryMessages(t.Context(), child, 1, 1); err == nil {
		t.Fatal("HistoryMessages on a subtask succeeded")
	}
	if _, err := e.SearchHistory(t.Context(), "nope", "codename", 5); err == nil {
		t.Fatal("SearchHistory on an unknown conversation succeeded")
	}
}

// A model cannot point history_get at another conversation: neither tool
// takes a conversation or scope argument.
func TestHistoryGetTakesNoConversation(t *testing.T) {
	p := &watchedProvider{Provider: fake.New(
		callWithID("history_get", "call-get", map[string]any{"from": 1, "to": 1, "conversation": "x"}),
		fake.Text("done"))}
	e, db, here := setupEngine(t, p, "")
	other := newConversation(t, db, "")
	appendParent(t, db, other, "a private note: HERON")
	appendParent(t, db, here, "our own first message")
	run, err := e.Send(t.Context(), here, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	for _, tool := range p.requests[0].Tools {
		if strings.HasPrefix(tool.Name, "history_") {
			raw, _ := json.Marshal(tool.InputSchema)
			if strings.Contains(string(raw), "conversation") || strings.Contains(string(raw), "scope") {
				t.Errorf("%s reaches beyond its conversation: %s", tool.Name, raw)
			}
		}
	}
	for _, r := range toolResults(t, db, here) {
		if strings.Contains(r.Content, "HERON") {
			t.Fatalf("history_get read another conversation: %q", r.Content)
		}
	}
}

// A long message is redacted around the match, never cut through a secret
// the snippet shows.
func TestHistorySnippetsAreRedacted(t *testing.T) {
	p := &watchedProvider{Provider: fake.New()}
	e, db, id := setupEngine(t, p, "")
	short := "the heron key is " + testKey
	long := strings.Repeat("padding words ", 1000) + "the heron key is " + testKey + strings.Repeat(" trailing words", 1000)
	appendParent(t, db, id, short, "reply", long)
	hits, err := e.SearchHistory(t.Context(), id, "heron", 5)
	if err != nil || len(hits) != 2 {
		t.Fatalf("hits: %+v, %v", hits, err)
	}
	for _, h := range hits {
		if strings.Contains(h.Snippet, testKey) || strings.Contains(h.Snippet, testKey[:20]) || !strings.Contains(h.Snippet, "[heron]") {
			t.Fatalf("snippet #%d not redacted or not marked: %q", h.Seq, h.Snippet)
		}
	}
}

func TestHistoryLimits(t *testing.T) {
	p := &watchedProvider{Provider: fake.New()}
	e, db, id := setupEngine(t, p, "")
	var texts []string
	for i := range 30 {
		texts = append(texts, fmt.Sprintf("entry %d about herons", i))
	}
	appendParent(t, db, id, texts...)
	hits, err := e.SearchHistory(t.Context(), id, "herons", 100)
	if err != nil || len(hits) != maxHistoryHits {
		t.Fatalf("hits = %d, %v; want %d", len(hits), err, maxHistoryHits)
	}
	if hits, err := e.SearchHistory(t.Context(), id, "herons", 0); err != nil || len(hits) != 5 {
		t.Fatalf("default k: %d, %v", len(hits), err)
	}
	if _, err := e.SearchHistory(t.Context(), id, "  !! ", 5); err == nil {
		t.Fatal("a query with no words was accepted")
	}
	out, err := e.HistoryMessages(t.Context(), id, 1, 30)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "only 20 messages at a time; continue from 21") || strings.Contains(out, "entry 20 ") {
		t.Fatalf("range not capped:\n%s", out)
	}
	if _, err := e.HistoryMessages(t.Context(), id, 5, 2); err == nil {
		t.Fatal("a backwards range was accepted")
	}
	big := newConversation(t, db, "")
	var bigs []string
	for range 10 {
		bigs = append(bigs, strings.Repeat("y", historyBlockChars-100))
	}
	appendParent(t, db, big, bigs...)
	out, err = e.HistoryMessages(t.Context(), big, 1, 10)
	if err != nil || len(out) > maxHistoryOutput+200 || !strings.Contains(out, "output limit reached; continue from") {
		t.Fatalf("total output not capped: %d chars, %v", len(out), err)
	}
	long := strings.Repeat("x", historyBlockChars+100)
	other := newConversation(t, db, "")
	appendParent(t, db, other, long)
	out, err = e.HistoryMessages(t.Context(), other, 1, 1)
	if err != nil || !strings.Contains(out, "[shortened]") {
		t.Fatalf("a long block was not shortened: %v", err)
	}
}

// The history group gates the tools, and the compaction note mentions them
// only when they are offered.
func TestHistoryToolsFollowTheBuiltinGroup(t *testing.T) {
	for _, offered := range []bool{true, false} {
		p := overflowThen(fake.Text("done"))
		e, db, id := setupEngine(t, p, "")
		if !offered {
			e.cfg.Builtins = tools.Builtins{tools.BuiltinNotes: true}
		}
		seedLongHistory(t, db, id)
		run, err := e.Send(t.Context(), id, Input{Text: "go"})
		if err != nil {
			t.Fatal(err)
		}
		_ = compactionEvents(t, run)
		names := requestTools(p.requests[len(p.requests)-1])
		has := slices.Contains(names, "history_search") && slices.Contains(names, "history_get")
		if has != offered {
			t.Errorf("offered=%t: tools %v", offered, names)
		}
		note := strings.Contains(messagesText(p.requests[len(p.requests)-1]), "history_search")
		if note != offered {
			t.Errorf("offered=%t: compaction note mentions history_search: %t", offered, note)
		}
	}
}

// A subtask child keeps the parent history tools and is not offered its own.
func TestChildrenUseParentHistoryNotHistory(t *testing.T) {
	p, _, _, _ := forkedRoot(t, nil, fake.New(fake.Text("report")))
	root, child := p.offered("", 0), p.offered("dig", 0)
	for _, name := range historyTools {
		if !slices.Contains(root, name) {
			t.Errorf("root conversation not offered %s: %v", name, root)
		}
		if slices.Contains(child, name) {
			t.Errorf("child offered %s", name)
		}
	}
	for _, name := range parentHistoryTools {
		if !slices.Contains(child, name) {
			t.Errorf("child not offered %s", name)
		}
	}
}
