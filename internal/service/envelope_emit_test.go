package service

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestApprovalEmitter_PersistsAndStreams(t *testing.T) {
	// In-memory store with migrations applied.
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := store.New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	// Seed workspace + session rows required by envelope_instances FK.
	if _, err := s.DB.Exec(
		`INSERT INTO workspaces (id, name, created_at) VALUES (?, ?, CURRENT_TIMESTAMP)`,
		"ws-emit-test", "emit-test",
	); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	sessionID := "sess-emit-1"
	if _, err := s.DB.Exec(
		`INSERT INTO sessions (id, workspace_id, title, short_code, created_at) VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		sessionID, "ws-emit-test", "emit test", "sc-emit-1",
	); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	sm := NewStreamManager()

	// CW-20260418-0100: CreateStream returns the producer channel; the live
	// consumer side is obtained via Subscribe. The pump sits between the two
	// and assigns EventID.
	produce := sm.CreateStream("msg-1", sessionID)
	defer close(produce)
	ch, _, ok := sm.Subscribe("msg-1", 0)
	if !ok {
		t.Fatal("Subscribe returned ok=false for freshly-created stream")
	}

	emitter := NewApprovalEmitter(s, sm)

	payload := []byte(`{"run_id":"r-1","role":"file-backend","prompt":"x","mode":"interactive"}`)
	id, err := emitter.Emit(context.Background(), sessionID, "subagent-spawn-approval", payload)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if id == "" {
		t.Fatal("want envelope id, got empty")
	}

	// Persistence: row exists with correct type + json.
	inst, err := s.GetEnvelopeInstance(id)
	if err != nil {
		t.Fatalf("get instance: %v", err)
	}
	if inst.EnvelopeType != "subagent-spawn-approval" {
		t.Errorf("type = %q", inst.EnvelopeType)
	}
	if inst.EnvelopeJSON != string(payload) {
		t.Errorf("json mismatch: got %q", inst.EnvelopeJSON)
	}

	// Stream: one plugin_envelope event wrapping {id,type,data}.
	select {
	case evt := <-ch:
		if evt.Type != "plugin_envelope" {
			t.Errorf("stream type = %q", evt.Type)
		}
		var wrap struct {
			ID   string          `json:"id"`
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		// chat.StreamEvent.Envelope is a string holding JSON.
		if err := json.Unmarshal([]byte(evt.Envelope), &wrap); err != nil {
			t.Fatalf("unmarshal wrap: %v", err)
		}
		if wrap.ID != id {
			t.Errorf("wrap.id = %q, want %q", wrap.ID, id)
		}
		if wrap.Type != "subagent-spawn-approval" {
			t.Errorf("wrap.type = %q", wrap.Type)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("no stream event delivered within 1s")
	}
}
