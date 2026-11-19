package engine

import (
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/provider/fake"
)

func TestRecordedExchangeReachesTheNextTurn(t *testing.T) {
	p := &watchedProvider{Provider: fake.New(fake.Text("model reply"))}
	e, db, id := setupEngine(t, p, "")
	if err := e.Record(t.Context(), id, "what is on my desk", "Three open cards."); err != nil {
		t.Fatal(err)
	}
	if len(p.requests) != 0 {
		t.Fatal("recording an exchange called the model")
	}
	run, err := e.Send(t.Context(), id, Input{Text: "and the first one?"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	messages := p.requests[0].Messages
	if len(messages) != 3 || messages[0].Role != "user" || messages[1].Role != "assistant" || messages[2].Role != "user" {
		t.Fatalf("request carried %d messages: %+v", len(messages), messages)
	}
	if got := messages[1].ContentBlocks[0].Text; got != "Three open cards." {
		t.Fatalf("recorded answer reached the model as %q", got)
	}
	history, err := db.History(t.Context(), id)
	if err != nil || len(history) != 4 {
		t.Fatalf("history has %d messages, %v", len(history), err)
	}
	if left, err := db.Interrupted(t.Context(), id); err != nil || left != nil {
		t.Fatalf("recorded turn left unfinished: %+v, %v", left, err)
	}
}

func TestRecordRejectsIncompleteExchange(t *testing.T) {
	e, _, id := setupEngine(t, &watchedProvider{Provider: fake.New()}, "")
	for _, pair := range [][2]string{{"", "answer"}, {"question", " "}} {
		if err := e.Record(t.Context(), id, pair[0], pair[1]); err == nil || !strings.Contains(err.Error(), "both") {
			t.Fatalf("%q/%q: %v", pair[0], pair[1], err)
		}
	}
	if err := e.Record(t.Context(), "no-such-conversation", "q", "a"); err == nil {
		t.Fatal("unknown conversation accepted")
	}
}
