package store

import (
	"context"
	"testing"
)

// TestAgentRuntimeProviderSessionID covers the CW-20260525-0001 Slice 3 lookup:
// the captured provider session id is readable by chat session id, and a
// re-create (cold-boot reboot) upsert CLEARS it — which is why the resume path
// reads the id BEFORE re-booting.
func TestAgentRuntimeProviderSessionID(t *testing.T) {
	s := newTestStore(t)

	// Missing row → empty, no error.
	if pid, err := s.AgentRuntimeProviderSessionID(context.Background(), "missing"); err != nil || pid != "" {
		t.Fatalf("missing row: got (%q, %v), want (\"\", nil)", pid, err)
	}

	// Captured provider session id is readable by chat session id.
	if err := s.CreateAgentRuntimeRow(context.Background(), &AgentRuntimeRow{ID: "sess-1", Mode: "long_lived", State: "running", ProviderSessionID: "claude-xyz"}); err != nil {
		t.Fatalf("CreateAgentRuntimeRow: %v", err)
	}
	if pid, err := s.AgentRuntimeProviderSessionID(context.Background(), "sess-1"); err != nil || pid != "claude-xyz" {
		t.Fatalf("captured: got (%q, %v), want claude-xyz", pid, err)
	}

	// A re-create (the cold-boot reboot path) upserts and clears the column —
	// the resume lookup must run before this.
	if err := s.CreateAgentRuntimeRow(context.Background(), &AgentRuntimeRow{ID: "sess-1", Mode: "long_lived", State: "running"}); err != nil {
		t.Fatalf("re-create: %v", err)
	}
	if pid, err := s.AgentRuntimeProviderSessionID(context.Background(), "sess-1"); err != nil || pid != "" {
		t.Fatalf("after reboot upsert: got (%q, %v), want cleared", pid, err)
	}
}
