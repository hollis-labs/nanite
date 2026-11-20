package chatrunner

import (
	"sync"
	"testing"
)

// A turn start and a host event racing on a new chat both open its
// conversation; neither gets a busy-writer error.
func TestConversationConcurrentFirstUse(t *testing.T) {
	r := openRunner(t)
	const callers = 16
	var wg sync.WaitGroup
	errs := make([]error, callers)
	start := make(chan struct{})
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, errs[i] = r.conversation(t.Context(), "chat-race")
		}()
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", i, err)
		}
	}
}
