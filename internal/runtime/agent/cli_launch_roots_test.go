package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/permission"
	"github.com/hollis-labs/nanite/internal/store"
)

// CW-20261001-0232: what a launched CLI may write comes from the work root,
// the configured dev_tools_allowed_paths and operator config, and never from
// what a turn's text names.

// mentionHome is a $HOME holding every directory the named mentions point at,
// so that a missing directory is not what keeps them out of the roots.
func mentionHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, env := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"} {
		t.Setenv(env, "")
	}
	for _, d := range []string{".ssh", ".gnupg", filepath.Join(".config", "systemd", "user"), filepath.Join(".local", "bin"), "dev/proj"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

const namingTurn = "please update ~/.ssh/authorized_keys, write ~/.config/systemd/user/x.service and edit /etc/hosts"

func containsPath(roots []string, want string) bool {
	for _, r := range roots {
		if r == want {
			return true
		}
	}
	return false
}

// A turn that names ~/.ssh, ~/.config/systemd/user and /etc must not put them,
// or anything above them, in what the launch hands to Codex or Claude.
func TestCLILaunchRoots_NamedPathsNeverReachTheLaunch(t *testing.T) {
	home := mentionHome(t)
	workRoot := filepath.Join(home, "dev", "proj")
	const sessionID = "sess-named"

	grants := permission.NewPathGrants()
	grants.RegisterFromUserMessage(sessionID, namingTurn+" and ~/dev/proj/main.go")
	// Subagent lineage: a worker spawned under the session sees the same grants.
	const childID = "sess-named-worker"
	grants.RegisterLineage(childID, sessionID)

	forbidden := []string{
		filepath.Join(home, ".ssh"), filepath.Join(home, ".config", "systemd", "user"), filepath.Join(home, ".config", "systemd"),
		filepath.Join(home, ".config"), "/etc", "/", home,
	}

	for _, tc := range []struct {
		name, provider, session string
	}{
		{"codex", "codex", sessionID},
		{"claude", "claude", sessionID},
		{"codex subagent via lineage", "codex", childID},
		{"claude subagent via lineage", "claude", childID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			layout, params := composeBootdirParams(&Dependencies{PathGrants: grants},
				Options{Provider: tc.provider, SessionID: tc.session, Workdir: workRoot},
				&store.AgentProfile{Name: "w", SystemPrompt: "p"}, tc.session)
			if got, want := params.CLIWritableRoots, []string{workRoot}; !equalStrings(got, want) {
				t.Fatalf("CLIWritableRoots = %v, want only the work root %v", got, want)
			}

			bootDir, err := layout.Setup(params)
			if err != nil {
				t.Fatalf("Setup: %v", err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(bootDir) })

			var planted string
			switch tc.provider {
			case "codex":
				planted = readTestFile(t, filepath.Join(bootDir, "config.toml"))
			default:
				planted = readTestFile(t, filepath.Join(bootDir, ".claude", "settings.json"))
				var settings struct {
					Permissions struct {
						AdditionalDirectories []string `json:"additionalDirectories"`
					} `json:"permissions"`
				}
				if err := json.Unmarshal([]byte(planted), &settings); err != nil {
					t.Fatalf("parse settings.json: %v\n%s", err, planted)
				}
				if got, want := settings.Permissions.AdditionalDirectories, []string{workRoot}; !equalStrings(got, want) {
					t.Fatalf("additionalDirectories = %v, want %v", got, want)
				}
				// The only --add-dir is the work root.
				if got, want := workRootArgs("claude", workRoot), []string{"--add-dir", workRoot}; !equalStrings(got, want) {
					t.Fatalf("claude work-root args = %v, want %v", got, want)
				}
			}
			for _, bad := range forbidden {
				// Exactly, as a listed root. (The work root is under $HOME, so a
				// "<home>/ prefix would match it.)
				if strings.Contains(planted, `"`+bad+`"`) {
					t.Fatalf("planted config lists %s as a root\n%s", bad, planted)
				}
			}
			for _, bad := range forbidden[:3] {
				if strings.Contains(planted, `"`+bad+`/`) {
					t.Fatalf("planted config lists something under %s\n%s", bad, planted)
				}
			}
		})
	}
}

// With the kill switch on the old fold returns, but only for grants that
// survived the mention policy: the named protected paths are still refused at
// registration, so they do not come back through the switch.
func TestCLILaunchRoots_KillSwitchFoldsOnlyVettedGrants(t *testing.T) {
	home := mentionHome(t)
	workRoot := filepath.Join(home, "work")
	if err := os.MkdirAll(workRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	const sessionID, childID = "sess-switch", "sess-switch-worker"

	grants := permission.NewPathGrants()
	// How the container configures it.
	grants.SetMentionPolicy(permission.MentionPolicy{Confine: true})
	grants.RegisterFromUserMessage(sessionID, namingTurn+" and ~/dev/proj/main.go")
	grants.RegisterLineage(childID, sessionID)
	deps := &Dependencies{PathGrants: grants}

	// Off: no grant is a root.
	t.Setenv(PathMentionLaunchRootsEnv, "")
	if got := effectiveCLIWritableRoots(deps, sessionID, workRoot); !equalStrings(got, []string{workRoot}) {
		t.Fatalf("switch off: roots = %v, want only the work root", got)
	}
	t.Setenv(PathMentionLaunchRootsEnv, "0")
	if got := effectiveCLIWritableRoots(deps, sessionID, workRoot); !equalStrings(got, []string{workRoot}) {
		t.Fatalf("switch 0: roots = %v, want only the work root", got)
	}

	t.Setenv(PathMentionLaunchRootsEnv, "1")
	for _, id := range []string{sessionID, childID} {
		got := effectiveCLIWritableRoots(deps, id, workRoot)
		if !containsPath(got, filepath.Join(home, "dev", "proj")) {
			t.Fatalf("switch on, %s: roots = %v, want the vetted ~/dev/proj grant folded in", id, got)
		}
		for _, bad := range []string{filepath.Join(home, ".ssh"), filepath.Join(home, ".config", "systemd", "user"), "/etc", "/", home} {
			if containsPath(got, bad) {
				t.Fatalf("switch on, %s: roots = %v include %s, which the mention policy refuses", id, got, bad)
			}
		}
	}
}

// A writable root that does not exist makes every Codex command fail
// ("bwrap: Can't bind mount ... No such file or directory"), so none reaches
// the launch: not a configured one, not the work root, not a file, and not a
// grant the kill switch folds in.
func TestCLILaunchRoots_DropsRootsThatAreNotExistingDirectories(t *testing.T) {
	base := t.TempDir()
	good := filepath.Join(base, "good")
	if err := os.MkdirAll(good, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(base, "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(base, "newdir")
	deps := &Dependencies{CLIWritableRoots: []string{good, missing, file, filepath.Join(missing, "deeper")}}

	if got := effectiveCLIWritableRoots(deps, "s", good); !equalStrings(got, []string{good}) {
		t.Fatalf("roots = %v, want only the existing directory %v", got, good)
	}
	if got := effectiveCLIWritableRoots(deps, "s", missing); !equalStrings(got, []string{good}) {
		t.Fatalf("missing work root: roots = %v, want only %v", got, good)
	}
	if got := effectiveCLIWritableRoots(nil, "s", missing); len(got) != 0 {
		t.Fatalf("nil deps, missing work root: roots = %v, want none", got)
	}

	// Through the kill switch: a mention of a file in a directory that does not
	// exist yet grants that directory and its file, and neither is a root, nor
	// is the parent a missing grant would otherwise widen to ($HOME here).
	t.Setenv(PathMentionLaunchRootsEnv, "1")
	t.Setenv("HOME", base)
	grants := permission.NewPathGrants()
	grants.RegisterFromUserMessage("s", filepath.Join(missing, "x.txt"))
	deps = &Dependencies{PathGrants: grants}
	if got := effectiveCLIWritableRoots(deps, "s", good); !equalStrings(got, []string{good}) {
		t.Fatalf("kill switch on, grant of a missing directory: roots = %v, want only %v", got, good)
	}
	// The same turn once the directory exists: now it is a root.
	if err := os.MkdirAll(missing, 0o700); err != nil {
		t.Fatal(err)
	}
	if got := effectiveCLIWritableRoots(deps, "s", good); !containsPath(got, missing) {
		t.Fatalf("kill switch on, directory created: roots = %v, want %v", got, missing)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path) //nolint:gosec // a file this test planted into its own temp dir
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(body)
}
