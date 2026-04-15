package chat

import (
	"context"
	"sync"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestResponseHandler_DefaultPassthrough(t *testing.T) {
	h := LookupResponseHandler("unregistered-type")
	resp := ResponseV1{
		V: 1, Kind: "x", ID: "i", Status: StatusSubmitted,
		Data:    map[string]any{"k": "v"},
		Answers: []Answer{{QuestionID: "q1", Value: "a"}},
	}
	res, err := h.HandleResponse(context.Background(), store.EnvelopeInstance{}, resp)
	if err != nil {
		t.Fatalf("default handler err: %v", err)
	}
	if res.Silent {
		t.Fatal("default handler should not be silent")
	}
	if res.TranscriptData["k"] != "v" {
		t.Fatalf("data not passed through: %+v", res.TranscriptData)
	}
	if _, ok := res.TranscriptData["answers"]; !ok {
		t.Fatalf("answers not promoted into transcript data: %+v", res.TranscriptData)
	}
}

func TestResponseHandler_RegisterOverride(t *testing.T) {
	const kind = "test-register-override"
	t.Cleanup(func() { UnregisterResponseHandler(kind) })

	called := false
	RegisterResponseHandler(kind, HandlerFunc(func(_ context.Context, _ store.EnvelopeInstance, _ ResponseV1) (HandlerResult, error) {
		called = true
		return HandlerResult{Silent: true, FollowUp: "ok"}, nil
	}))

	h := LookupResponseHandler(kind)
	res, err := h.HandleResponse(context.Background(), store.EnvelopeInstance{}, ResponseV1{})
	if err != nil {
		t.Fatalf("registered handler err: %v", err)
	}
	if !called {
		t.Fatal("registered handler not invoked")
	}
	if !res.Silent || res.FollowUp != "ok" {
		t.Fatalf("result wrong: %+v", res)
	}
}

func TestResponseHandler_Unregister(t *testing.T) {
	const kind = "test-unregister"
	RegisterResponseHandler(kind, HandlerFunc(func(_ context.Context, _ store.EnvelopeInstance, _ ResponseV1) (HandlerResult, error) {
		return HandlerResult{FollowUp: "registered"}, nil
	}))
	UnregisterResponseHandler(kind)

	h := LookupResponseHandler(kind)
	res, _ := h.HandleResponse(context.Background(), store.EnvelopeInstance{}, ResponseV1{})
	if res.FollowUp == "registered" {
		t.Fatal("handler not unregistered")
	}
}

func TestResponseHandler_ConcurrentAccess(t *testing.T) {
	const kind = "test-concurrent"
	t.Cleanup(func() { UnregisterResponseHandler(kind) })

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			RegisterResponseHandler(kind, HandlerFunc(func(_ context.Context, _ store.EnvelopeInstance, _ ResponseV1) (HandlerResult, error) {
				return HandlerResult{}, nil
			}))
		}()
		go func() {
			defer wg.Done()
			_ = LookupResponseHandler(kind)
		}()
		go func() {
			defer wg.Done()
			UnregisterResponseHandler(kind)
		}()
	}
	wg.Wait()
}
