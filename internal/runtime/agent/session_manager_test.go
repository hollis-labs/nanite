package agent

import (
	"errors"
	"testing"
)

func TestSessionManager_RecoveryReplacementCannotBeDeletedByPredecessor(t *testing.T) {
	manager := NewSessionManager()
	old := &Session{}
	replacement := &Session{}
	manager.Store("session", old)
	if previous, ok, err := manager.Swap("session", replacement); err != nil || !ok || previous != old {
		t.Fatalf("Swap previous = %p, %v; want old %p", previous, ok, old)
	}
	if manager.CompareAndDelete("session", old) {
		t.Fatal("exiting predecessor deleted replacement binding")
	}
	if got, ok := manager.Load("session"); !ok || got != replacement {
		t.Fatalf("Load = %p, %v; want replacement %p", got, ok, replacement)
	}
}

func TestSessionManager_UsesSingleSharedACPManager(t *testing.T) {
	manager := NewSessionManager()
	if manager.ACPManager() == nil || manager.ACPManager() != manager.ACPManager() {
		t.Fatal("ACPManager must return one stable manager shared by wrappers")
	}
}

func TestSessionManager_StaleGenerationCannotRetireOrRecoverOverSuccessor(t *testing.T) {
	manager := NewSessionManager()
	old := &Session{}
	successor := &Session{}
	if err := manager.Store("session", old); err != nil {
		t.Fatalf("Store old: %v", err)
	}
	if _, _, err := manager.Swap("session", successor); err != nil {
		t.Fatalf("Swap successor: %v", err)
	}
	if manager.Retire("session", old) {
		t.Fatal("stale generation retired successor")
	}
	if _, ok := manager.BeginRecovery("session", old); ok {
		t.Fatal("stale generation acquired recovery lease over successor")
	}
	if got, ok := manager.Load("session"); !ok || got != successor {
		t.Fatalf("Load = %p, %v; want successor %p", got, ok, successor)
	}
}

func TestSessionManager_RecoveryLeaseBlocksOrdinaryStoreButAllowsAdoption(t *testing.T) {
	manager := NewSessionManager()
	old := &Session{}
	replacement := &Session{}
	if err := manager.Store("session", old); err != nil {
		t.Fatalf("Store old: %v", err)
	}
	lease, ok := manager.BeginRecovery("session", old)
	if !ok {
		t.Fatal("BeginRecovery did not claim current generation")
	}
	if err := manager.Store("session", &Session{}); !errors.Is(err, ErrRecoveryInProgress) {
		t.Fatalf("ordinary Store error = %v, want ErrRecoveryInProgress", err)
	}
	if previous, loaded, err := manager.Adopt("session", replacement); err != nil || loaded || previous != nil {
		t.Fatalf("Adopt = %p, %v, %v; want nil, false, nil", previous, loaded, err)
	}
	manager.EndRecovery("session", lease)
	if got, ok := manager.Load("session"); !ok || got != replacement {
		t.Fatalf("Load = %p, %v; want replacement %p", got, ok, replacement)
	}
}
