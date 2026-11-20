package warden

import (
	"context"
	"strings"
	"sync"
	"testing"
)

type waiterMemory struct {
	mu    sync.Mutex
	value string
}

func (m *waiterMemory) Setting(context.Context, string, string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.value, nil
}
func (m *waiterMemory) SetSetting(_ context.Context, _, _ string, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.value = value
	return nil
}

func TestWaitersSurviveRestartAndClearIndividually(t *testing.T) {
	f, srv := newFake(t)
	file := credential(t, "parent-0")
	state := &waiterMemory{}
	open := func() *Source {
		s := source(t, srv.URL, file, nil, nil)
		if err := s.UseWaiterStore(t.Context(), state); err != nil {
			t.Fatal(err)
		}
		return s
	}
	first := open()
	scopes := []string{"read", "write"}
	for _, id := range []string{"chat-a", "chat-b"} {
		if _, err := first.Token(WithConversation(t.Context(), id), "mcp-gateway", scopes); err == nil {
			t.Fatal("missing grant succeeded")
		}
	}
	second := open()
	var granted []Waiting
	second.cfg.OnGranted = func(w Waiting) { granted = append(granted, w) }
	f.mu.Lock()
	f.allowed["mcp-gateway"] = true
	f.reqStatus = "approved"
	f.mu.Unlock()
	// The canonical audience/scope key must match despite order/duplicates.
	if _, err := second.Token(WithConversation(t.Context(), "chat-a"), "mcp-gateway", []string{"write", "read", "read"}); err != nil {
		t.Fatal(err)
	}
	if len(granted) != 1 || granted[0].Conversation != "chat-a" || granted[0].RequestID != "req-1" {
		t.Fatalf("granted %+v", granted)
	}
	raw, _ := state.Setting(t.Context(), "", waiterStateKey)
	if strings.Contains(raw, "chat-a") || !strings.Contains(raw, "chat-b") {
		t.Fatalf("waiting set %s", raw)
	}
	for _, secret := range []string{"parent-", "tok-", "access_token", "refresh_token"} {
		if strings.Contains(raw, secret) {
			t.Fatal("waiter state contains credential material")
		}
	}
	third := open()
	var seen []Waiting
	third.cfg.OnGranted = func(w Waiting) { seen = append(seen, w) }
	for _, id := range []string{"chat-a", "chat-b", "chat-b"} {
		if _, err := third.Token(WithConversation(t.Context(), id), "mcp-gateway", scopes); err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 1 || seen[0].Conversation != "chat-b" {
		t.Fatalf("granted after second restart %+v", seen)
	}
	if raw, _ := state.Setting(t.Context(), "", waiterStateKey); raw != "" {
		t.Fatalf("empty state retained: %s", raw)
	}
}

func TestWaiterRefusalPersistsAcrossRestart(t *testing.T) {
	_, srv := newFake(t)
	file := credential(t, "parent-0")
	state := &waiterMemory{}
	s := source(t, srv.URL, file, nil, nil)
	if err := s.UseWaiterStore(t.Context(), state); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Token(WithConversation(t.Context(), "chat-a"), "mcp-gateway", nil); err == nil {
		t.Fatal("missing grant succeeded")
	}
	s.Decided("req-1", "denied")
	restored := source(t, srv.URL, file, nil, nil)
	if err := restored.UseWaiterStore(t.Context(), state); err != nil {
		t.Fatal(err)
	}
	if len(restored.entry("mcp-gateway", nil).waiters) != 0 {
		t.Fatal("refused request restored")
	}
}

func TestWaiterStateRejectsUnknownVersion(t *testing.T) {
	_, srv := newFake(t)
	for _, raw := range []string{`{"version":2,"entries":{}}`, `broken`} {
		s := source(t, srv.URL, credential(t, "parent-0"), nil, nil)
		state := &waiterMemory{value: raw}
		if err := s.UseWaiterStore(t.Context(), state); err == nil {
			t.Fatal("invalid waiter state accepted")
		}
		if state.value != raw {
			t.Fatal("invalid waiter state overwritten")
		}
	}
}
