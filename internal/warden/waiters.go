package warden

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"time"
)

// Engine state is outside the host. namespace exposed by SetHostState.
const waiterStateKey = "auth.source-waiters"
const maxWaiterAge = 24 * time.Hour

type WaiterStore interface {
	Setting(context.Context, string, string) (string, error)
	SetSetting(context.Context, string, string, string) error
}
type waiter struct {
	RequestID string    `json:"request_id"`
	Since     time.Time `json:"since"`
}
type savedWaiterEntry struct {
	Request      string            `json:"request,omitempty"`
	RequestSince time.Time         `json:"request_since,omitempty"`
	Waiters      map[string]waiter `json:"waiters,omitempty"`
}
type waiterState struct {
	Version int                         `json:"version"`
	Entries map[string]savedWaiterEntry `json:"entries"`
}

func expiredWaiter(since, now time.Time) bool {
	return since.IsZero() || !since.Add(maxWaiterAge).After(now)
}
func pruneSavedWaiters(value savedWaiterEntry, now time.Time) savedWaiterEntry {
	for c, w := range value.Waiters {
		if expiredWaiter(w.Since, now) {
			delete(value.Waiters, c)
		}
	}
	if expiredWaiter(value.RequestSince, now) {
		value.Request = ""
		value.RequestSince = time.Time{}
	}
	return value
}
func pruneLiveWaiters(e *entry, now time.Time) bool {
	before := len(e.waiters)
	request := e.request
	value := pruneSavedWaiters(savedWaiterEntry{e.request, e.requestSince, e.waiters}, now)
	e.request, e.requestSince = value.Request, value.RequestSince
	return before != len(e.waiters) || request != e.request
}

// UseWaiterStore runs before token requests. Bad state is non-fatal and is
// replaced on the next save, just like an unreadable host watch snapshot.
func (s *Source) UseWaiterStore(ctx context.Context, store WaiterStore) error {
	state := waiterState{Version: 1, Entries: map[string]savedWaiterEntry{}}
	raw, err := store.Setting(ctx, "", waiterStateKey)
	if err == nil && raw != "" {
		err = json.Unmarshal([]byte(raw), &state)
		if err == nil && state.Version != 1 {
			err = fmt.Errorf("unsupported waiter state version %d", state.Version)
		}
	}
	if err != nil {
		s.cfg.Logger.Warn("warden: ignoring saved waiters", "error", err)
		state = waiterState{Version: 1, Entries: map[string]savedWaiterEntry{}}
	}
	s.waiterStore = store
	s.savedWaiters = state.Entries
	if s.savedWaiters == nil {
		s.savedWaiters = map[string]savedWaiterEntry{}
	}
	for k, value := range s.savedWaiters {
		value = pruneSavedWaiters(value, s.cfg.Now())
		if len(value.Waiters) == 0 && value.Request == "" {
			delete(s.savedWaiters, k)
			continue
		}
		s.savedWaiters[k] = value
		s.tokens[k] = &entry{request: value.Request, requestSince: value.RequestSince, waiters: maps.Clone(value.Waiters)}
	}
	return nil
}

// saveWaiters is called with e.mu held. Copies avoid reading another entry
// while it changes; the save lock orders snapshots. Every save drops old IDs.
func (s *Source) saveWaiters(k string, e *entry) {
	if s.waiterStore == nil {
		return
	}
	s.waiterMu.Lock()
	defer s.waiterMu.Unlock()
	now := s.cfg.Now()
	pruneLiveWaiters(e, now)
	s.savedWaiters[k] = savedWaiterEntry{e.request, e.requestSince, maps.Clone(e.waiters)}
	for key, value := range s.savedWaiters {
		value = pruneSavedWaiters(value, now)
		if len(value.Waiters) == 0 && value.Request == "" {
			delete(s.savedWaiters, key)
		} else {
			s.savedWaiters[key] = value
		}
	}
	var value string
	if len(s.savedWaiters) > 0 {
		raw, err := json.Marshal(waiterState{Version: 1, Entries: s.savedWaiters})
		if err != nil {
			s.cfg.Logger.Warn("warden: encode waiters", "error", err)
			return
		}
		value = string(raw)
	}
	if err := s.waiterStore.SetSetting(context.Background(), "", waiterStateKey, value); err != nil {
		s.cfg.Logger.Warn("warden: save waiters", "error", err)
	}
}

// sweepWaiters releases abandoned in-memory associations too. It runs outside
// token-entry locks, so concurrent token requests never acquire each other's
// locks while retaining their own.
func (s *Source) sweepWaiters() {
	s.mu.Lock()
	entries := make(map[string]*entry, len(s.tokens))
	for k, e := range s.tokens {
		entries[k] = e
	}
	s.mu.Unlock()
	for k, e := range entries {
		e.mu.Lock()
		if pruneLiveWaiters(e, s.cfg.Now()) {
			s.saveWaiters(k, e)
		}
		e.mu.Unlock()
	}
}
