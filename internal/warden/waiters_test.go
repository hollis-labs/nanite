package warden

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
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
	f := newFake(t)
	srv := f.Server
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
	f.Grant("mcp-gateway")
	f.Decide("req-1", "approved")
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
	srv := newFake(t).Server
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

func TestBadWaiterStateDoesNotPreventStartup(t *testing.T) {
	for i, raw := range []string{`{"version":2,"entries":{}}`, `broken`} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			srv := newFake(t).Server
			s := source(t, srv.URL, credential(t, "parent-0"), nil, nil)
			var logs bytes.Buffer
			s.cfg.Logger = slog.New(slog.NewTextHandler(&logs, nil))
			state := &waiterMemory{value: raw}
			if err := s.UseWaiterStore(t.Context(), state); err != nil {
				t.Fatal(err)
			}
			if len(s.tokens) != 0 || !strings.Contains(logs.String(), "ignoring saved waiters") {
				t.Fatalf("bad state was not ignored: %s", logs.String())
			}
			if _, err := s.Token(WithConversation(t.Context(), "chat"), "mcp-gateway", nil); err == nil {
				t.Fatal("missing grant succeeded")
			}
			if state.value == raw || !strings.Contains(state.value, "req-1") {
				t.Fatalf("next save did not replace bad state: %s", state.value)
			}
		})
	}
}

func TestPendingRequestSurvivesSourceRestart(t *testing.T) {
	f := newFake(t)
	srv := f.Server
	file := credential(t, "parent-0")
	state := &waiterMemory{}
	first := source(t, srv.URL, file, nil, nil)
	if err := first.UseWaiterStore(t.Context(), state); err != nil {
		t.Fatal(err)
	}
	ctx := WithConversation(t.Context(), "chat")
	if _, err := first.Token(ctx, "mcp-gateway", nil); err == nil {
		t.Fatal("missing grant succeeded")
	}
	second := source(t, srv.URL, file, nil, nil)
	if err := second.UseWaiterStore(t.Context(), state); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Token(ctx, "mcp-gateway", nil); err == nil {
		t.Fatal("pending request succeeded")
	}
	filed := len(f.Requests())
	if filed != 1 {
		t.Fatalf("restart filed %d requests, want 1", filed)
	}
}

func TestApprovedWaitersAgeOutOnLoadAndSave(t *testing.T) {
	for _, reload := range []bool{false, true} {
		t.Run(fmt.Sprint(reload), func(t *testing.T) {
			f := newFake(t)
			srv := f.Server
			clock := &clock{now: time.Unix(1800000000, 0)}
			f.Clock(clock.Now)
			file := credential(t, "parent-0")
			state := &waiterMemory{}
			s := source(t, srv.URL, file, clock, nil)
			if err := s.UseWaiterStore(t.Context(), state); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Token(WithConversation(t.Context(), "abandoned-chat"), "mcp-gateway", nil); err == nil {
				t.Fatal("missing grant succeeded")
			}
			s.Decided("req-1", "approved")
			clock.add(24*time.Hour + time.Second)
			if reload {
				s = source(t, srv.URL, file, clock, nil)
				if err := s.UseWaiterStore(t.Context(), state); err != nil {
					t.Fatal(err)
				}
				if len(s.entry("mcp-gateway", nil).waiters) != 0 {
					t.Fatal("load retained old waiter")
				}
			}
			// A different request causes a save while the approved chat never retries.
			if _, err := s.Token(WithConversation(t.Context(), "new-chat"), "another-service", nil); err == nil {
				t.Fatal("missing grant succeeded")
			}
			raw, _ := state.Setting(t.Context(), "", waiterStateKey)
			if strings.Contains(raw, "abandoned-chat") || !strings.Contains(raw, "new-chat") {
				t.Fatalf("bad saved waiters: %s", raw)
			}
			if len(s.entry("mcp-gateway", nil).waiters) != 0 {
				t.Fatal("save retained old in-memory waiter")
			}
			f.Grant("mcp-gateway")
			granted := false
			s.cfg.OnGranted = func(Waiting) { granted = true }
			if _, err := s.Token(WithConversation(t.Context(), "abandoned-chat"), "mcp-gateway", nil); err != nil {
				t.Fatal(err)
			}
			if granted {
				t.Fatal("expired waiter emitted a grant callback")
			}
		})
	}
}
