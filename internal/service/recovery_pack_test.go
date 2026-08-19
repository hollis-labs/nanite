package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hollis-labs/nanite/internal/store"
)

func TestShouldRecoverColdBoot(t *testing.T) {
	s := &chatServiceImpl{}

	// Not a cold boot → never recover.
	if s.shouldRecoverColdBoot("sess", false) {
		t.Error("non-cold boot should not recover")
	}
	// Cold boot, no fresh flag → recover (daemon restart / Recover()).
	if !s.shouldRecoverColdBoot("sess", true) {
		t.Error("cold boot without fresh flag should recover")
	}
	// Intentional reboot armed the one-shot flag → suppress recovery once...
	s.freshBootSessions.Store("sess", struct{}{})
	if s.shouldRecoverColdBoot("sess", true) {
		t.Error("fresh-flagged cold boot should NOT recover")
	}
	// ...and the flag is consumed (one-shot): the next cold boot recovers again.
	if !s.shouldRecoverColdBoot("sess", true) {
		t.Error("fresh flag should be one-shot — next cold boot recovers")
	}
}

// rawInsertMessage writes a message with an explicit created_at so ordering is
// deterministic (CreateMessage stamps now() at second precision, which ties).
func rawInsertMessage(t *testing.T, st *store.Store, sessionID, role, content, createdAt string) {
	t.Helper()
	if _, err := st.DB.Exec(
		`INSERT INTO messages (id, session_id, role, content, metadata, created_at) VALUES (?,?,?,?,?,?)`,
		uuid.New().String(), sessionID, role, content, "{}", createdAt,
	); err != nil {
		t.Fatalf("insert message: %v", err)
	}
}

// TestComposeBootPayload_ColdBootInjectsRecovery is the Slice-1 acceptance test:
// a cold-booted CLI session with prior history gets a recovery pack planted
// ahead of the current user message; a live (non-cold) turn and a brand-new
// session (no prior history) do not.
func TestComposeBootPayload_ColdBootInjectsRecovery(t *testing.T) {
	st := newConfigTestStore(t)
	svc := &chatServiceImpl{store: st}

	sess := &store.Session{Title: "Release prep", Provider: "claude", Model: "claude-sonnet"}
	if err := st.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	rawInsertMessage(t, st, sess.ID, "user", "what are the options?", "2026-05-25T10:00:01Z")
	rawInsertMessage(t, st, sess.ID, "assistant", `{"v":1,"text":"Option A, Option B, Option C"}`, "2026-05-25T10:00:02Z")
	rawInsertMessage(t, st, sess.ID, "user", "continue please", "2026-05-25T10:00:03Z") // the current turn (persisted)

	bootDir := t.TempDir()

	// Cold boot → recovery pack planted ahead of the current message.
	payload := svc.composeBootPayload(sess.ID, sess, nil, bootDir, nil, "continue please", true)
	if !strings.Contains(payload, "<recovered-session-context>") {
		t.Fatalf("cold-boot payload missing recovery pack:\n%s", payload)
	}
	for _, sub := range []string{"what are the options?", "Option A, Option B, Option C"} {
		if !strings.Contains(payload, sub) {
			t.Errorf("recovery payload missing prior turn %q", sub)
		}
	}
	// Recovery context precedes the current user message.
	if idx := strings.Index(payload, "<recovered-session-context>"); idx < 0 || idx > strings.LastIndex(payload, "continue please") {
		t.Errorf("recovery block should precede the current user message")
	}
	// The current turn is not replayed inside the recovered block.
	block := payload[strings.Index(payload, "<recovered-session-context>"):strings.Index(payload, "</recovered-session-context>")]
	if strings.Contains(block, "continue please") {
		t.Errorf("current turn should be excluded from the recovered block")
	}
	// Pointer file written.
	if _, err := os.Stat(filepath.Join(bootDir, recoveryPackFileName)); err != nil {
		t.Errorf("recovery.md pointer not written: %v", err)
	}

	// event_log postmortem: a real, enriched row lands via the same store
	// the pack was built against — not a bare event-type marker.
	events, err := st.ListEvents("recovery", 50)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	var plantedEvent *store.EventLog
	for i := range events {
		if events[i].EventType == "recovery_pack_planted" && events[i].SessionID == sess.ID {
			plantedEvent = &events[i]
			break
		}
	}
	if plantedEvent == nil {
		t.Fatalf("event_log missing recovery_pack_planted row for session %s; got %d recovery events", sess.ID, len(events))
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(plantedEvent.Metadata), &meta); err != nil {
		t.Fatalf("event_log metadata not JSON: %v\nblob: %s", err, plantedEvent.Metadata)
	}
	// 2 prior turns replayed (the "continue please" current turn is excluded).
	if got, want := meta["messages_replayed"], float64(2); got != want {
		t.Errorf("metadata.messages_replayed = %v, want %v", got, want)
	}
	if meta["source_session_id"] != sess.ID {
		t.Errorf("metadata.source_session_id = %v, want %v", meta["source_session_id"], sess.ID)
	}
	if meta["reason"] == "" || meta["reason"] == nil {
		t.Errorf("metadata.reason is empty, want a real reason string")
	}
	if meta["history_window_capped"] != false {
		t.Errorf("metadata.history_window_capped = %v, want false (only 2 prior turns, well under the window)", meta["history_window_capped"])
	}

	// Live runtime (not cold) → no recovery pack.
	if live := svc.composeBootPayload(sess.ID, sess, nil, bootDir, nil, "continue please", false); strings.Contains(live, "<recovered-session-context>") {
		t.Errorf("non-cold payload must not inject recovery:\n%s", live)
	}

	// Brand-new session (only the current turn, no prior history) → no pack.
	fresh := &store.Session{Title: "New", Provider: "claude", Model: "claude-sonnet"}
	if err := st.CreateSession(fresh); err != nil {
		t.Fatalf("CreateSession fresh: %v", err)
	}
	rawInsertMessage(t, st, fresh.ID, "user", "first message", "2026-05-25T11:00:01Z")
	if got := svc.composeBootPayload(fresh.ID, fresh, nil, t.TempDir(), nil, "first message", true); strings.Contains(got, "<recovered-session-context>") {
		t.Errorf("new session (no prior history) must not inject recovery:\n%s", got)
	}
}
