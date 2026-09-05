package agent

import "testing"

func TestSessionManager_RecoveryReplacementCannotBeDeletedByPredecessor(t *testing.T) {
	manager := NewSessionManager()
	old := &Session{}
	replacement := &Session{}
	manager.Store("session", old)
	if previous, ok := manager.Swap("session", replacement); !ok || previous != old {
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
