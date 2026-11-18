package chatrunner

import (
	"testing"

	chatstream "github.com/hollis-labs/libs/ui/ui-go/chatstream"
)

func TestFinishReasons(t *testing.T) {
	for stop, want := range map[string]chatstream.FinishReason{
		"end_turn":   chatstream.FinishStop,
		"":           chatstream.FinishStop,
		"max_steps":  chatstream.FinishTurnLimit,
		"max_tokens": chatstream.FinishLength,
	} {
		if got := finishReason(stop); got != want {
			t.Errorf("stop reason %q maps to %q, want %q", stop, got, want)
		}
	}
}

func TestConversationIDIsSafeAsADirectoryName(t *testing.T) {
	if got := ConversationID("conv_01ABC"); got != "chat-conv_01ABC" {
		t.Errorf("a plain ID became %q", got)
	}
	for _, id := range []string{"../../etc", "a/b", "", "with space"} {
		if got := ConversationID(id); !safeID.MatchString(got) {
			t.Errorf("ConversationID(%q) = %q, not a safe name", id, got)
		}
	}
}
