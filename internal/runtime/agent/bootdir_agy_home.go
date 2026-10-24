package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// bootdir_agy_home.go gives an agy launch its own HOME so Nanite can write
// the MCP allow rules without touching the operator's ~/.gemini
// (CW-20261024-0109).
//
// agy honours $HOME for its config and reads settings from
// $HOME/.gemini/antigravity-cli/settings.json only (plus an admin file). So a
// launch gets:
//
//	<bootDir>/agy-home/            HOME for the agy process
//	  <entry> -> <real home>/<entry>   a symlink for every entry of the real
//	                                   home except .gemini, .agents, .DS_Store,
//	                                   .CFUserTextEncoding, .Trash, so ~/.ssh,
//	                                   ~/.gitconfig, ~/.nanite, ~/dev ... resolve
//	                                   as usual
//	  .gemini -> <private .gemini>     agy's private config dir
//
// The private .gemini is PERSISTENT PER AGENT, not per boot:
// ~/.nanite/agy-gemini/<agent slug>/. agy's first-run onboarding and consent
// answers, its conversation history and its caches then survive across boots,
// and the Nanite settings regenerate on every launch (a revoked grant is gone
// at the next launch). The cost is that two concurrent launches of one agent
// share it; they write identical rules for the same grants, and every write is
// an atomic rename. An agent with no usable slug gets a per-boot dir inside the
// boot dir instead (<bootDir>/agy-gemini), which dies with the launch.
//
// Auth is shared by symlink: oauth_creds.json, google_accounts.json,
// jetski-standalone-oauth-token, installation_id and
// antigravity-cli/antigravity-oauth-token point at the operator's real files
// (only those that exist), so no login is repeated and no credential is copied.
// If agy replaces a link with a refreshed file, that copy is private to the
// agent's gemini dir. Nothing under the real ~/.gemini or ~/.agents is ever
// written by Nanite, and the real .agents is not linked: global plugins and
// skills of the operator do not load here (the planted plugin is the boot
// dir's .agents).

const (
	agyHomeDirName   = "agy-home"
	agyGeminiDirName = "agy-gemini"
	agySettingsRel   = "antigravity-cli/settings.json"
	// agyOwnedRulesRel is Nanite's record of the allow rules it wrote, so the
	// next launch removes exactly those before adding the current ones. It is a
	// sidecar, not a settings key: agy is not known to tolerate unknown keys.
	agyOwnedRulesRel = "antigravity-cli/nanite-owned-rules.json"
)

// agyHomeSkip are the real-home entries a farm does not link.
var agyHomeSkip = map[string]bool{
	".gemini": true, ".agents": true, ".DS_Store": true, ".CFUserTextEncoding": true, ".Trash": true,
}

// agyAuthFiles are the real ~/.gemini files the private dir links to.
var agyAuthFiles = []string{
	"oauth_creds.json", "google_accounts.json", "jetski-standalone-oauth-token",
	"installation_id", "antigravity-cli/antigravity-oauth-token",
}

// agyGeminiRoot is the parent of the per-agent private .gemini dirs (a var so
// the live proof can run against a throwaway data dir).
var agyGeminiRoot = func() string {
	if home := naniteHomeDir(); home != "" {
		return filepath.Join(home, agyGeminiDirName)
	}
	return ""
}

var agySlugSafe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// agyPrivateGeminiDir is the private .gemini for this launch (see the file
// comment).
func agyPrivateGeminiDir(bootDir string, params SetupParams) string {
	if root := agyGeminiRoot(); root != "" && params.AgentProfile != nil {
		for _, key := range []string{params.AgentProfile.Slug, params.AgentProfile.ID} {
			if agySlugSafe.MatchString(key) {
				return filepath.Join(root, key)
			}
		}
	}
	return filepath.Join(bootDir, agyGeminiDirName)
}

// plantAgyHome builds the launch's HOME farm and private .gemini and writes the
// settings. It never writes under the real home except Nanite's own ~/.nanite.
func plantAgyHome(bootDir string, params SetupParams) error {
	realHome, err := os.UserHomeDir()
	if err != nil || realHome == "" {
		return fmt.Errorf("agent: agy home: cannot resolve the home directory: %v", err)
	}
	farm := filepath.Join(bootDir, agyHomeDirName)
	if err := os.MkdirAll(farm, 0o700); err != nil {
		return fmt.Errorf("agent: agy home: %w", err)
	}
	entries, err := os.ReadDir(realHome)
	if err != nil {
		return fmt.Errorf("agent: agy home: read %s: %w", realHome, err)
	}
	for _, e := range entries {
		if agyHomeSkip[e.Name()] {
			continue
		}
		if err := os.Symlink(filepath.Join(realHome, e.Name()), filepath.Join(farm, e.Name())); err != nil && !errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("agent: agy home: link %s: %w", e.Name(), err)
		}
	}

	gem := agyPrivateGeminiDir(bootDir, params)
	for _, d := range []string{"antigravity-cli", "config"} {
		if err := os.MkdirAll(filepath.Join(gem, d), 0o700); err != nil {
			return fmt.Errorf("agent: agy home: %w", err)
		}
	}
	if err := os.Symlink(gem, filepath.Join(farm, ".gemini")); err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("agent: agy home: link .gemini: %w", err)
	}
	realGemini := filepath.Join(realHome, ".gemini")
	for _, rel := range agyAuthFiles {
		src := filepath.Join(realGemini, rel)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		dst := filepath.Join(gem, rel)
		if _, err := os.Lstat(dst); err == nil {
			continue
		}
		if err := os.Symlink(src, dst); err != nil {
			return fmt.Errorf("agent: agy home: link %s: %w", rel, err)
		}
	}
	return writeAgySettings(gem, agyAllowRules(params))
}

// writeAgySettings merges rules into <gem>/antigravity-cli/settings.json: every
// key and every allow entry Nanite did not write is kept, the entries Nanite
// wrote last time (the sidecar) are dropped, and the current rules are added.
// The file is 0600 and replaced atomically.
func writeAgySettings(gem string, rules []string) error {
	sp := filepath.Join(gem, agySettingsRel)
	settings := map[string]any{}
	if b, err := os.ReadFile(sp); err == nil { //nolint:gosec // G304: the agent's private gemini dir
		if json.Unmarshal(b, &settings) != nil || settings == nil {
			// Unreadable settings cannot be merged: keep them aside, start fresh.
			_ = os.Rename(sp, sp+".bak-"+time.Now().UTC().Format("20060102T150405"))
			settings = map[string]any{}
		}
	}
	var owned []string
	if b, err := os.ReadFile(filepath.Join(gem, agyOwnedRulesRel)); err == nil { //nolint:gosec // G304: as above
		_ = json.Unmarshal(b, &owned)
	}
	perms, _ := settings["permissions"].(map[string]any)
	if perms == nil {
		perms = map[string]any{}
	}
	var allow []any
	if cur, ok := perms["allow"].([]any); ok {
		for _, e := range cur {
			if s, isStr := e.(string); isStr && (slices.Contains(owned, s) || slices.Contains(rules, s)) {
				continue
			}
			allow = append(allow, e)
		}
	}
	for _, r := range rules {
		allow = append(allow, r)
	}
	if allow == nil {
		allow = []any{}
	}
	perms["allow"] = allow
	settings["permissions"] = perms
	body, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("agent: agy settings: %w", err)
	}
	if err := writeFileAtomic(sp, append(body, '\n')); err != nil {
		return err
	}
	if rules == nil {
		rules = []string{}
	}
	ownedBody, err := json.Marshal(rules)
	if err != nil {
		return fmt.Errorf("agent: agy settings: %w", err)
	}
	return writeFileAtomic(filepath.Join(gem, agyOwnedRulesRel), ownedBody)
}

// writeFileAtomic writes data to path as 0600 through a temp file and rename.
func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("agent: agy settings: %w", err)
	}
	name := tmp.Name()
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr, os.Chmod(name, 0o600)); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("agent: agy settings: %w", err)
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("agent: agy settings: %w", err)
	}
	return nil
}

// agyHomePath is the HOME an agy launch runs with, derived from its boot dir.
func agyHomePath(bootDir string) string {
	if strings.TrimSpace(bootDir) == "" {
		return ""
	}
	return filepath.Join(bootDir, agyHomeDirName)
}
