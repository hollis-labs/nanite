package agent

import (
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/provider/fake"
)

// The host API searches and reads one conversation's own transcript, never
// another's, and redacts what it returns.
func TestHistoryHostAPI(t *testing.T) {
	// Shaped like a key the redactor recognizes, built at run time so no
	// key-shaped literal sits in the source.
	key := "sk-" + "ant-api03-" + strings.Repeat("AbCdEfGhIjKlMnOpQrStUvWxYz0123456789", 2) + "-abcdefAA"
	dir := agentDirOfKind(t, "kind: embedded\nlifetime: application\napplication_id: history-test\n")
	a, err := Open(t.Context(), Options{Dir: dir, GlobalSkillsDir: "-", Provider: fake.New(fake.Text("noted"), fake.Text("noted"))})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	one, err := a.NewConversation(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	two, err := a.NewConversation(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for c, text := range map[*Conversation]string{one: "the heron key is " + key, two: "nothing to see"} {
		run, err := c.Send(t.Context(), Input{Text: text})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := run.Wait(); err != nil {
			t.Fatal(err)
		}
	}

	hits, err := one.SearchHistory(t.Context(), "heron", 5)
	if err != nil || len(hits) != 1 || hits[0].Seq != 1 || hits[0].ConversationID != one.ID() {
		t.Fatalf("one.SearchHistory: %+v, %v", hits, err)
	}
	if strings.Contains(hits[0].Snippet, key) || !strings.Contains(hits[0].Snippet, "[heron]") {
		t.Fatalf("snippet not redacted or not marked: %q", hits[0].Snippet)
	}
	if hits, err := two.SearchHistory(t.Context(), "heron", 5); err != nil || len(hits) != 0 {
		t.Fatalf("two.SearchHistory saw another conversation: %+v, %v", hits, err)
	}
	got, err := one.HistoryMessages(t.Context(), 1, 2)
	if err != nil || !strings.Contains(got, "[#1 user] the heron key is") || !strings.Contains(got, "[#2 assistant] noted") {
		t.Fatalf("HistoryMessages: %q, %v", got, err)
	}
	if strings.Contains(got, key) {
		t.Fatal("HistoryMessages returned the key")
	}
}
