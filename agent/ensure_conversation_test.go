package agent

import (
	"sync"
	"testing"

	"github.com/hollis-labs/nanite/internal/provider/fake"
)

// First use of a conversation ID from several goroutines creates it once,
// and no caller fails because another created it first.
func TestEnsureConversationConcurrentFirstUse(t *testing.T) {
	dir := agentDirOfKind(t, "kind: embedded\nlifetime: application\napplication_id: ensure-test\n")
	a, err := Open(t.Context(), Options{Dir: dir, GlobalSkillsDir: "-", Provider: fake.New()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	const callers = 16
	var wg sync.WaitGroup
	errs := make([]error, callers)
	ids := make([]string, callers)
	start := make(chan struct{})
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			c, err := a.EnsureConversation(t.Context(), "chat-shared", func(info *ConversationInfo) { info.ID = "ignored" })
			errs[i] = err
			if c != nil {
				ids[i] = c.ID()
			}
		}()
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", i, err)
		}
		if ids[i] != "chat-shared" {
			t.Fatalf("caller %d got conversation %q", i, ids[i])
		}
	}
	list, err := a.Conversations(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != "chat-shared" {
		t.Fatalf("conversations = %+v, want only chat-shared", list)
	}
	if _, err := a.EnsureConversation(t.Context(), ""); err == nil {
		t.Fatal("empty ID accepted")
	}
}
