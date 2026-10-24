package agent

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// bootdir_codex_sessions.go keeps a long-lived Codex session's conversation
// across Nanite restarts (CW-20261024-0034).
//
// Codex stores a thread as a rollout file under $CODEX_HOME/sessions, and
// `codex exec resume <thread id>` fails with "no rollout found for thread id"
// when that directory lacks it (verified against codex-cli 0.163.0: a fresh
// CODEX_HOME fails, the same CODEX_HOME seeded with only sessions/ resumes).
// Nanite points CODEX_HOME at a per-boot dir that Session.Stop removes, so
// every cold boot lost the thread and Nanite had to refuse to preset the
// provider session id.
//
// The fix is to keep only the sessions/ tree outside the boot dir, at
// ~/.nanite/codex-sessions/<nanite session id>, and link <bootDir>/sessions
// to it. Everything else codex writes there (sqlite state, logs, caches) is
// derivable and stays per boot; the resume probe above needed nothing else.
// The thread id itself is already persisted per session by
// Store.SetProviderSessionID and read back by the chat boot path.
//
// Only ModeLongLived and ModeResume get the link: those are the modes a
// restart re-boots with the stored id. One-shot, subagent and background
// launches keep their transcript in the disposable boot dir as before.

const codexSessionsDir = "sessions"

// codexSessionsRoot is the directory holding every session's persisted codex
// state, or "" when the home directory cannot be resolved.
func codexSessionsRoot() string {
	home := naniteHomeDir()
	if home == "" {
		return ""
	}
	return filepath.Join(home, "codex-sessions")
}

// codexPersistentSessionsDir is the persistent sessions dir for one Nanite
// session, or "" when it has none.
func codexPersistentSessionsDir(sessionID string) string {
	root := codexSessionsRoot()
	if root == "" || sessionID == "" || sessionID != filepath.Base(sessionID) || strings.HasPrefix(sessionID, ".") {
		return ""
	}
	return filepath.Join(root, sessionID)
}

func codexPersistsSessions(params SetupParams) bool {
	return params.Mode == ModeLongLived || params.Mode == ModeResume
}

// linkCodexSessions makes <bootDir>/sessions a link to the session's
// persistent dir, creating that dir (0700) on first use.
func linkCodexSessions(bootDir string, params SetupParams) error {
	if !codexPersistsSessions(params) {
		return nil
	}
	target := codexPersistentSessionsDir(params.SessionID)
	if target == "" {
		return nil
	}
	if err := os.MkdirAll(target, 0o700); err != nil {
		return fmt.Errorf("agent: codex sessions: create %s: %w", target, err)
	}
	link := filepath.Join(bootDir, codexSessionsDir)
	if err := os.RemoveAll(link); err != nil {
		return fmt.Errorf("agent: codex sessions: clear %s: %w", link, err)
	}
	if err := os.Symlink(target, link); err != nil {
		return fmt.Errorf("agent: codex sessions: link %s: %w", link, err)
	}
	return nil
}

// codexThreadResumable reports whether the persisted sessions dir for
// sessionID holds the rollout of threadID, i.e. whether `codex exec resume`
// can find it. Boot presets the stored thread id only when this holds, so a
// pruned or never-persisted dir degrades to a fresh thread instead of failing
// the first turn.
func codexThreadResumable(sessionID, threadID string) bool {
	dir := codexPersistentSessionsDir(sessionID)
	if dir == "" || threadID == "" || strings.ContainsAny(threadID, `/\*?[`) {
		return false
	}
	found := errors.New("found")
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && strings.HasPrefix(d.Name(), "rollout-") && strings.HasSuffix(d.Name(), "-"+threadID+".jsonl") {
			return found
		}
		return nil
	})
	return errors.Is(err, found)
}
