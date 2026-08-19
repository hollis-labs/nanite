package pack

import (
	"strings"
	"testing"

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
		if got := ShouldBuildRecoveryPack(c.cold, c.prior); got != c.want {
			t.Errorf("ShouldBuildRecoveryPack(%v,%d)=%v want %v", c.cold, c.prior, got, c.want)
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
		if got := MessagePlainText(c.in); got != c.want {
			t.Errorf("MessagePlainText(%q)=%q want %q", c.in, got, c.want)
		}
	}
}

func TestExcludeCurrentTurn(t *testing.T) {
	msgs := []store.Message{
		{Role: "user", Content: "q1"},
		{Role: "assistant", Content: `{"v":1,"text":"a1"}`},
		{Role: "user", Content: "continue please"},
	}
	got := ExcludeCurrentTurn(msgs, "continue please")
	if len(got) != 2 {
		t.Fatalf("expected trailing current user turn dropped, got %d msgs", len(got))
	}
	// Non-matching trailing turn is kept.
	if got2 := ExcludeCurrentTurn(msgs, "something else"); len(got2) != 3 {
		t.Fatalf("non-matching userContent should keep all, got %d", len(got2))
	}
}

func TestBuildRecoveryPack(t *testing.T) {
	pack := BuildRecoveryPack(RecoveryPackInput{
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
