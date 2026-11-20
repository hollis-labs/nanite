package warden

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

// A cached token is returned at once while another audience's mint is stalled
// at Warden: the waiter sweep skips the busy entry instead of waiting for it.
func TestCachedTokenDoesNotWaitForAnotherMint(t *testing.T) {
	stalled := make(chan struct{})
	release := make(chan struct{})
	srv, _ := tokenServer(t, func(w http.ResponseWriter, r *http.Request, n int) {
		if r.Form.Get("audience") == "mcp-gateway" {
			close(stalled)
			<-release
		}
		ok(w, n)
	})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	s := source(t, srv.URL, credential(t, "parent-0"), nil, nil)
	if _, err := s.Token(t.Context(), "llm-gateway", nil); err != nil {
		t.Fatal(err)
	}
	slow := make(chan error, 1)
	go func() { _, err := s.Token(context.Background(), "mcp-gateway", nil); slow <- err }()
	<-stalled
	fast := make(chan error, 1)
	go func() { _, err := s.Token(t.Context(), "llm-gateway", nil); fast <- err }()
	select {
	case err := <-fast:
		if err != nil {
			t.Fatalf("cached Token = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cached Token waited for another audience's mint")
	}
	close(release)
	if err := <-slow; err != nil {
		t.Fatalf("stalled mint = %v", err)
	}
}

// An empty, pending or unknown status is not a decision: the pending request
// is kept, so the next token request does not file a second one.
func TestDecidedIgnoresStatusesThatAreNotDecisions(t *testing.T) {
	f, srv := newFake(t)
	c := &clock{now: time.Unix(1_800_000_000, 0)}
	s := source(t, srv.URL, credential(t, "parent-0"), c, nil)
	var missing *MissingGrantError
	if _, err := s.Token(t.Context(), "mcp-gateway", nil); !errors.As(err, &missing) {
		t.Fatalf("Token = %v, want a MissingGrantError", err)
	}
	for _, status := range []string{"", "pending", " Pending ", "later"} {
		s.Decided("req-1", status)
	}
	if _, err := s.Token(t.Context(), "mcp-gateway", nil); !errors.As(err, &missing) {
		t.Fatalf("second Token = %v", err)
	}
	f.mu.Lock()
	requests := f.requests
	f.mu.Unlock()
	if requests != 1 {
		t.Fatalf("%d grant requests filed, want 1 (a non-decision dropped the pending request)", requests)
	}
	s.Decided("req-1", " DENIED ")
	if _, err := s.Token(t.Context(), "mcp-gateway", nil); !errors.As(err, &missing) {
		t.Fatalf("Token after denial = %v", err)
	}
	f.mu.Lock()
	requests = f.requests
	f.mu.Unlock()
	if requests != 2 {
		t.Fatalf("%d grant requests after a denial, want 2", requests)
	}
}
