package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func codexSessionsParams(sess string, mode Mode) SetupParams {
	return SetupParams{
		SessionID:    sess,
		RunID:        "r0",
		AgentProfile: &store.AgentProfile{Name: "codex-sess", Slug: "codex-sess"},
		Mode:         mode,
		BootContent:  "# Boot\n",
	}
}

// A long-lived Codex session's rollout survives the boot dir: the restart
// (Stop removes the boot dir, a cold boot plants a new one) sees the thread.
func TestCodexLayout_SessionsSurviveRestart(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const sess, thread = "s-restart", "01a16f12-4436-7222-9d07-2f55bded0bd6"

	if codexThreadResumable(sess, thread) {
		t.Fatal("thread resumable before it exists")
	}

	boot1, err := codexLayout{}.Setup(codexSessionsParams(sess, ModeLongLived))
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	// What codex does: write sessions/YYYY/MM/DD/rollout-<ts>-<thread>.jsonl under CODEX_HOME.
	day := filepath.Join(boot1, "sessions", "2026", "10", "24")
	if err := os.MkdirAll(day, 0o700); err != nil {
		t.Fatalf("mkdir through link: %v", err)
	}
	if err := os.WriteFile(filepath.Join(day, "rollout-2026-10-24T13-00-30-"+thread+".jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !codexThreadResumable(sess, thread) {
		t.Fatal("thread not resumable after codex wrote its rollout")
	}

	// Nanite restart: Session.Stop removes the boot dir.
	if err := os.RemoveAll(boot1); err != nil {
		t.Fatal(err)
	}
	if !codexThreadResumable(sess, thread) {
		t.Fatal("rollout lost with the boot dir")
	}

	boot2, err := codexLayout{}.Setup(codexSessionsParams(sess, ModeLongLived))
	if err != nil {
		t.Fatalf("cold Setup: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(boot2) })
	if boot1 == boot2 {
		t.Fatal("expected a fresh boot dir")
	}
	got := filepath.Join(boot2, "sessions", "2026", "10", "24", "rollout-2026-10-24T13-00-30-"+thread+".jsonl")
	if _, err := os.Stat(got); err != nil {
		t.Fatalf("new boot dir does not see the prior rollout: %v", err)
	}

	// Other sessions and other thread ids are not resumable from this dir.
	if codexThreadResumable("s-other", thread) || codexThreadResumable(sess, "other-thread") || codexThreadResumable(sess, "../x") {
		t.Fatal("resumable check matched the wrong session or thread")
	}
}

// Disposable modes keep their transcript in the boot dir.
func TestCodexLayout_SessionsNotPersistedForOneShot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	boot, err := codexLayout{}.Setup(codexSessionsParams("s-oneshot", ModeOneShot))
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(boot) })
	if _, err := os.Lstat(filepath.Join(boot, "sessions")); !os.IsNotExist(err) {
		t.Errorf("one-shot boot dir has a sessions entry (err %v)", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".nanite", "codex-sessions")); !os.IsNotExist(err) {
		t.Errorf("one-shot created persistent codex state (err %v)", err)
	}
}
