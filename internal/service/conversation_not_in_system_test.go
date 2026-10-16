package service

import (
	"strings"
	"testing"
	"time"

	ctxpkg "github.com/hollis-labs/libs/agent/core/contextwindow"
	"github.com/hollis-labs/nanite/internal/lifecycle"
)

// The conversation travels in the messages payload. A second copy in the
// system blocks sends every turn twice and, on the live Anthropic path,
// preceded intermittent stop_reason=refusal (CW-20261011-0040).
func TestProviderRequestDoesNotRepeatConversationInSystemBlocks(t *testing.T) {
	f := newCharacterizationFixture(t, []characterizationProviderStep{
		{events: doneEvents("first answer")},
		{events: doneEvents("second answer")},
	})
	f.svc.activeGen = make(map[string]*inFlightGen)
	f.svc.lifecycle = lifecycle.NewManager("test.no-dup-conversation")
	t.Cleanup(func() { _ = f.svc.lifecycle.Shutdown(5 * time.Second) })
	bindTestDefinedConfiguration(t, f)

	for _, text := range []string{"zebra question one", "yak question two"} {
		id, err := f.svc.SubmitCognitiveTurn(t.Context(), f.session, text)
		if err != nil {
			t.Fatal(err)
		}
		stream, _ := f.svc.streams.GetStream(id)
		drainStream(stream)
	}

	requests := f.provider.requestsSnapshot()
	if len(requests) != 2 {
		t.Fatalf("provider requests = %d", len(requests))
	}
	last := requests[1]
	inMessages := false
	for _, m := range last.Messages {
		if strings.Contains(m.Content, "zebra question one") {
			inMessages = true
		}
	}
	if !inMessages {
		t.Fatal("prior turn missing from messages payload")
	}
	for _, b := range last.SlotBlocks {
		if b.Name == ctxpkg.SlotConversation {
			t.Fatalf("conversation slot shipped as a system block")
		}
		if strings.Contains(b.Content, "zebra question one") || strings.Contains(b.Content, "first answer") {
			t.Fatalf("slot %q repeats the conversation: %q", b.Name, b.Content)
		}
	}
}
