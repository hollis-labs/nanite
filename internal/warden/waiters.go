package warden

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
)

const waiterStateKey = "host.auth.source-waiters"

// WaiterStore holds only conversation/request associations, never credentials
// or minted tokens. It is the same settings store used for host watches.
type WaiterStore interface {
	Setting(context.Context, string, string) (string, error)
	SetSetting(context.Context, string, string, string) error
}

type waiterState struct {
	Version int                          `json:"version"`
	Entries map[string]map[string]string `json:"entries"`
}

// UseWaiterStore restores waiting conversations before this source is used.
// It must be called during initialization, before any token requests.
func (s *Source) UseWaiterStore(ctx context.Context, store WaiterStore) error {
	raw, err := store.Setting(ctx, "", waiterStateKey)
	if err != nil {
		return fmt.Errorf("warden: load waiters: %w", err)
	}
	state := waiterState{Version: 1, Entries: map[string]map[string]string{}}
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &state); err != nil {
			return fmt.Errorf("warden: load waiters: %w", err)
		}
		if state.Version != 1 {
			return fmt.Errorf("warden: unsupported waiter state version %d", state.Version)
		}
	}
	for k, waiters := range state.Entries {
		s.tokens[k] = &entry{waiters: maps.Clone(waiters)}
	}
	s.waiterStore = store
	s.savedWaiters = state.Entries
	if s.savedWaiters == nil {
		s.savedWaiters = map[string]map[string]string{}
	}
	return nil
}

// saveWaiters is called with e.mu held after a change. Snapshot copies keep
// other entries' updates out of marshaling; waiterMu orders all saves.
func (s *Source) saveWaiters(k string, e *entry) {
	if s.waiterStore == nil {
		return
	}
	s.waiterMu.Lock()
	defer s.waiterMu.Unlock()
	if len(e.waiters) == 0 {
		delete(s.savedWaiters, k)
	} else {
		s.savedWaiters[k] = maps.Clone(e.waiters)
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
