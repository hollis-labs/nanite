package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hollis-labs/nanite/internal/store"
)

func TestShouldBuildRecoveryPack(t *testing.T) {
	cases := []struct {
		cold  bool
		prior int
		want  bool
	}{
		{true, 4, true},   // cold boot with history → recover
		{true, 0, false},  // cold boot, no history (new session) → skip
		{false, 4, false}, // live runtime → no recovery
	}
	for _, c := range cases {
		if got := shouldBuildRecoveryPack(c.cold, c.prior); got != c.want {
			t.Errorf("shouldBuildRecoveryPack(%v,%d)=%v want %v", c.cold, c.prior, got, c.want)
		}
	}
}

func TestMessagePlainText(t *testing.T) {
	cases := []struct{ in, want string }{
		{`{"v":1,"text":"hello there"}`, "hello there"},
		{`{"v":1,"text":""}`, ""},
		{"plain user text", "plain user text"},
		{`{not json`, `{not json`},
		{`{"text":"no version"}`, `{"text":"no version"}`}, // v missing → treat as plain
	}
	for _, c := range cases {
		if got := messagePlainText(c.in); got != c.want {
			t.Errorf("messagePlainText(%q)=%q want %q", c.in, got, c.want)
		}
	}
}

func TestExcludeCurrentTurn(t *testing.T) {
	msgs := []store.Message{
		{Role: "user", Content: "q1"},
		{Role: "assistant", Content: `{"v":1,"text":"a1"}`},
		{Role: "user", Content: "continue please"},
	}
	got := excludeCurrentTurn(msgs, "continue please")
	if len(got) != 2 {
		t.Fatalf("expected trailing current user turn dropped, got %d msgs", len(got))
	}
	// Non-matching trailing turn is kept.
	if got2 := excludeCurrentTurn(msgs, "something else"); len(got2) != 3 {
		t.Fatalf("non-matching userContent should keep all, got %d", len(got2))
	}
}

func TestBuildRecoveryPack(t *testing.T) {
	pack := buildRecoveryPack(recoveryPackInput{
		Session:  &store.Session{Title: "Release prep", Provider: "claude", Model: "sonnet"},
		Reason:   "host service restart",
		History:  []store.Message{{Role: "user", Content: "do the thing"}, {Role: "assistant", Content: `{"v":1,"text":"options: 1,2,3"}`}},
		PackPath: "/boot/recovery.md",
	})
	for _, sub := range []string{
		"<recovered-session-context>", "RECOVERED CONTEXT", "Release prep",
		"do the thing", "options: 1,2,3", "/boot/recovery.md", "</recovered-session-context>",
	} {
		if !strings.Contains(pack, sub) {
			t.Errorf("pack missing %q:\n%s", sub, pack)
		}
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
