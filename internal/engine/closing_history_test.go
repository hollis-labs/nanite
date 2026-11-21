package engine

import (
	"strings"
	"testing"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/recovery"
)

func closingNote() llmtypes.ChatMessage {
	return llmtypes.ChatMessage{Role: "user", ContentBlocks: []llmtypes.ContentBlock{{Type: "text", Name: recovery.ClosingPromptPrefix + "max_steps", Text: "Close the turn now."}}}
}

func TestClosingPromptIsNotCompactionTurnStart(t *testing.T) {
	note := closingNote()
	if ownerMessage(note) {
		t.Fatal("closing note starts a user turn")
	}
	// A small keep budget used to cut immediately before the closing note.
	// It must instead cut at an assistant step within the same actual turn.
	msgs := []llmtypes.ChatMessage{text("user", strings.Repeat("request ", 500)), text("assistant", "work"), note, text("assistant", "closing reply")}
	ids := []string{"request", "work", "note", "reply"}
	if got := keepCut(msgs, msgs, ids, messagesTokens(msgs[2:])); got != 3 {
		t.Fatalf("cut = %d, want final assistant step 3", got)
	}
	if got := keepCut(msgs, msgs, ids, messagesTokens(msgs)); got != 0 {
		t.Fatalf("fitting whole turn cut at %d", got)
	}
	// Matching text alone is ordinary user input, never an internal marker.
	if !ownerMessage(text("user", recovery.ClosingPromptPrefix+"max_steps")) {
		t.Fatal("literal user text hidden")
	}
}

func TestClosingPromptSummaryLabelsHostNote(t *testing.T) {
	got := summaryInput([]llmtypes.ChatMessage{text("user", "original request"), closingNote(), text("assistant", "closing reply")}, 10000)[0].Content
	for _, want := range []string{"Person: original request", "Host note: Close the turn now.", "Agent: closing reply"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, "Person: Close the turn now.") {
		t.Fatal("summary attributes host instruction to person")
	}
}
