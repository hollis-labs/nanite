package agent

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// bootdir_codex_auth.go points a codex boot dir's auth.json at the host's
// codex login (CW-20261001-0027).
//
// # Why auth.json is needed at all
//
// codexLayout.AmendEnv sets CODEX_HOME=<bootDir> so codex reads the planted
// config.toml. Codex then reads its credentials ONLY from
// <bootDir>/auth.json, never from ~/.codex/auth.json. With no usable file
// there every model call fails 401 (CW-20261001-0021).
//
// # Why a symlink, not a copy
//
// CW-20261001-0021 (#349) planted a byte copy of the host's auth.json. A
// ChatGPT login refreshes its tokens during a session, and the refresh
// rotates the refresh token: codex persists the new one
// (refresh_and_persist_chatgpt_token -> persist_tokens -> storage.save,
// codex-rs/login/src/auth/manager.rs at rust-v0.159.2). Into a copy, that
// write lands in a boot dir that is deleted at session end, while the
// host's file keeps a refresh token the provider may have invalidated.
//
// A symlink sends the write to the host file instead. That holds because
// codex rewrites auth.json in place rather than replacing it: its
// FileAuthStorage::save opens the path with truncate+write+create and
// mode 0600 and never renames over it (codex-rs/login/src/auth/storage.rs
// at rust-v0.159.2), and opening a symlink opens its target. Checked
// against codex-cli 0.159.2: `codex login --with-api-key` with CODEX_HOME
// at a dir whose auth.json was a symlink kept the link and rewrote the
// target, same inode, mode still 0600. Tether reached the same answer
// for its own fix (CW-20261001-0031). A codex release that switched to
// temp-file-and-rename would replace the link with a regular file and
// bring the drift back; that is the assumption to recheck on a codex
// upgrade.
//
// It also leaves no credential copies in boot dirs.
//
// # Not logged in
//
// The link is planted whether or not the host file exists. A dangling
// link reads exactly like a missing file, so codex reports "Not logged
// in" at dispatch and the boot itself never fails. Unlike a placeholder or
// a missing file, a dangling link resolves on its own once the user logs
// in on the host, so a session started before the login can still use it.
//
// # Ownership: outside the materialize engine
//
// auth.json is deliberately NOT part of the planted artifact tree. The
// engine writes each entry through a temp file and a rename, so owning
// this path would replace the link with a regular file on every re-plant.
// This is the one direct write into a codex boot dir. It does not set the
// trap bootdir_plant.go warns about, because no plant ever asks the engine
// for this path.
//
// # Sandbox caveat
//
// Codex runs outside Nanite's own sandbox today: deps.SandboxBaseProfile
// is never set, and a profile with no ID disables go-agent-wrapper's
// pre-spawn wrapping. Codex's own sandbox confines the commands it runs,
// not its credential writes. If Nanite ever sandboxes the codex process
// itself, the link's target directory must stay readable, and writable
// for refreshes.
//
// INTERIM: the shared PreparedExecution under CW-20260930-0113 is meant
// to own credential placement (go-providers' EffectCodexAuthJSON with a
// caller CredentialResolver); this goes away when Nanite adopts it.

// codexAuthFile is the credentials file codex reads from $CODEX_HOME.
const codexAuthFile = "auth.json"

// linkCodexHostAuth makes <bootDir>/auth.json a symlink to the host's
// codex login, replacing whatever is there (a previous link, or a copy a
// pre-CW-20261001-0027 binary planted). It does nothing when the host
// login's location cannot be resolved (no CODEX_HOME and no home dir).
func linkCodexHostAuth(bootDir string) error {
	hostAuth := codexHostAuthPath()
	if hostAuth == "" {
		return nil
	}
	planted := filepath.Join(bootDir, codexAuthFile)
	if filepath.Clean(planted) == filepath.Clean(hostAuth) {
		return nil
	}
	if err := os.Remove(planted); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("agent: codex auth.json: remove %s: %w", planted, err)
	}
	if err := os.Symlink(hostAuth, planted); err != nil {
		return fmt.Errorf("agent: codex auth.json: link %s to the host login: %w", planted, err)
	}
	return nil
}

// codexHostAuthPath is the absolute path of the host's codex login:
// $CODEX_HOME/auth.json in Nanite's own environment, else
// ~/.codex/auth.json — codex's own discovery rule, and go-providers'.
// Absolute, because a relative link target would resolve against the boot
// dir rather than against Nanite's working directory.
func codexHostAuthPath() string {
	if h := os.Getenv("CODEX_HOME"); h != "" {
		abs, err := filepath.Abs(filepath.Join(h, codexAuthFile))
		if err != nil {
			return ""
		}
		return abs
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".codex", codexAuthFile)
}
