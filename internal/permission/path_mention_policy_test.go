package permission

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// CW-20261001-0232: a free-text path mention can come from any loopback
// client, so some grants are refused whoever wrote the text.

// useHome points $HOME at home and clears the XDG overrides.
func useHome(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	for _, env := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"} {
		t.Setenv(env, "")
	}
}

func TestMentions_SensitivePathsAreNeverGranted(t *testing.T) {
	const home = "/home/nanite-test-user"
	useHome(t, home)
	for _, mention := range []string{
		"~/.ssh/id_ed25519",
		"~/.gnupg/private-keys-v1.d/key",
		"~/.config/systemd/user/evil.service",
		"~/.claude/settings.json",
		"~/.claude.json",
		"~/.claude-work/x",
		"~/.codex/auth.json",
		"~/.local/bin/tool",
		"~/.local/share/nanite/workspaces/default/main.db",
		"~/.local/state/nanite/coordination/x",
		"~/.local/share/torque/torque.db",
		"~/.local/state/tether/x",
		"~/.config/tether/x",
		"~/.aws/credentials",
		"~/.kube/config",
		"~/.netrc",
		"~/.bashrc",
		"~/.zshrc",
		"~/.profile",
	} {
		t.Run(mention, func(t *testing.T) {
			g := NewPathGrants()
			granted, refused := g.RegisterMentions("s", "please edit "+mention)
			if len(granted) != 0 {
				t.Fatalf("mention %q granted %v; a sensitive path must never be granted", mention, granted)
			}
			if len(refused) == 0 {
				t.Fatalf("mention %q was neither granted nor reported refused", mention)
			}
			abs := filepath.Join(home, strings.TrimPrefix(mention, "~/"))
			if g.IsPathAllowed("s", abs) {
				t.Fatalf("IsPathAllowed(%q) = true after the mention", abs)
			}
		})
	}
}

// Q2 grants a mentioned path's parent directory too. A parent that is an
// ancestor of a sensitive path would grant it, so it is refused, and
// that includes "/" for a top-level mention like /tmp.
func TestMentions_AncestorsOfSensitivePathsAreRefused(t *testing.T) {
	const home = "/home/nanite-test-user"
	useHome(t, home)

	for _, tc := range []struct {
		mention string
		want    []string // what is still granted
	}{
		{"~/notes.txt", []string{home + "/notes.txt"}},                           // parent $HOME refused
		{"~/.config/app.toml", []string{home + "/.config/app.toml"}},             // parent ~/.config covers systemd
		{"~/.local/share/thing.txt", []string{home + "/.local/share/thing.txt"}}, // parent ~/.local/share covers app state
		{"~/.local", nil},
		{"~/.local/share", nil},
		{"~/.config", nil},
		{"~/", nil},
		{"/", nil},
		{"/home", nil},
		{"/home/nanite-test-user", nil},
		{"/etc/hosts", []string{"/etc/hosts", "/etc"}}, // /etc covers nothing sensitive
		{"~/dev/proj/main.go", []string{home + "/dev/proj/main.go", home + "/dev/proj"}},
	} {
		t.Run(tc.mention, func(t *testing.T) {
			g := NewPathGrants()
			// A trailing word keeps "~/" and "/" from ending the sentence.
			granted := g.RegisterFromUserMessage("s", "look at "+tc.mention+" please")
			if len(granted) != len(tc.want) {
				t.Fatalf("mention %q granted %v, want %v", tc.mention, granted, tc.want)
			}
			for _, w := range tc.want {
				found := false
				for _, got := range granted {
					found = found || got == w
				}
				if !found {
					t.Fatalf("mention %q granted %v, want %v", tc.mention, granted, tc.want)
				}
			}
		})
	}

	// The case this fixes: /tmp used to grant "/", and with it every path.
	g := NewPathGrants()
	g.RegisterFromUserMessage("s", "look in /tmp please")
	for _, outside := range []string{home + "/.ssh/id_rsa", "/etc/shadow", "/var/log/syslog"} {
		if g.IsPathAllowed("s", outside) {
			t.Fatalf("a mention of /tmp granted %s, outside /tmp", outside)
		}
	}
	if !g.IsPathAllowed("s", "/tmp/scratch.txt") {
		t.Fatal("a mention of /tmp no longer grants /tmp itself")
	}
	for _, p := range g.ListGrants("s") {
		if p == "/" {
			t.Fatal(`a mention of /tmp granted "/"`)
		}
	}
}

// A symlink gets no further than its target: one into a sensitive
// directory is refused, and so is a symlinked sensitive directory.
func TestMentions_SymlinksAreResolved(t *testing.T) {
	home := t.TempDir()
	useHome(t, home)
	for _, d := range []string{".ssh", "elsewhere", "dev"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// ~/innocent -> ~/.ssh
	if err := os.Symlink(filepath.Join(home, ".ssh"), filepath.Join(home, "innocent")); err != nil {
		t.Fatal(err)
	}
	g := NewPathGrants()
	if granted := g.RegisterFromUserMessage("s", "read ~/innocent/id_rsa"); len(granted) != 0 {
		t.Fatalf("a symlink into ~/.ssh was granted: %v", granted)
	}

	// ~/.config/systemd is itself a symlink to ~/elsewhere.
	if err := os.MkdirAll(filepath.Join(home, ".config"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, "elsewhere"), filepath.Join(home, ".config", "systemd")); err != nil {
		t.Fatal(err)
	}
	if granted := g.RegisterFromUserMessage("s2", "write ~/elsewhere/unit.service"); len(granted) != 0 {
		t.Fatalf("the target of a symlinked sensitive directory was granted: %v", granted)
	}
	if granted := g.RegisterFromUserMessage("s3", "write ~/.config/systemd/user/unit.service"); len(granted) != 0 {
		t.Fatalf("a path under a symlinked sensitive directory was granted: %v", granted)
	}
}

func TestMentions_ConfinedPolicyRefusesOutsideHomeAndBases(t *testing.T) {
	home := t.TempDir()
	base := t.TempDir()
	outside := t.TempDir()
	useHome(t, home)
	if err := os.MkdirAll(filepath.Join(home, "dev"), 0o700); err != nil {
		t.Fatal(err)
	}
	// A link inside $HOME that leads outside it.
	if err := os.Symlink(outside, filepath.Join(home, "dev", "out")); err != nil {
		t.Fatal(err)
	}

	g := NewPathGrants()
	g.SetMentionPolicy(MentionPolicy{Confine: true, Home: home, Bases: []string{base}})

	for _, tc := range []struct {
		name    string
		mention string
		granted bool
	}{
		{"inside home", "~/dev/x.go", true},
		{"inside a base", filepath.Join(base, "proj", "y.go"), true},
		{"system path", "/etc/passwd", false},
		{"outside every base", filepath.Join(outside, "z.txt"), false},
		{"a link from home to outside", "~/dev/out/z.txt", false},
		{"an ancestor of a base", filepath.Dir(base), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			granted, refused := g.RegisterMentions("s-"+tc.name, "open "+tc.mention+" now")
			if tc.granted && len(granted) == 0 {
				t.Fatalf("%q was refused (%v), want granted", tc.mention, refused)
			}
			if !tc.granted {
				for _, p := range granted {
					if p == tc.mention || strings.HasPrefix(p, outside) || p == "/etc/passwd" {
						t.Fatalf("%q was granted (%v), want refused", tc.mention, granted)
					}
				}
			}
		})
	}
}

func TestMentions_ConfiguredDeniedDirsAreRefused(t *testing.T) {
	const home = "/home/nanite-test-user"
	useHome(t, home)
	g := NewPathGrants()
	g.SetMentionPolicy(MentionPolicy{Home: home, Denied: []string{"/srv/nanite-instance/data"}})
	if granted := g.RegisterFromUserMessage("s", "edit /srv/nanite-instance/data/main.db"); len(granted) != 0 {
		t.Fatalf("a configured denied directory was granted: %v", granted)
	}
	if granted := g.RegisterFromUserMessage("s2", "edit /srv/other/file.txt"); len(granted) == 0 {
		t.Fatal("an unrelated path was refused")
	}
}

// A parent session's refused mentions are not in its bucket, so a spawned
// worker cannot inherit them through lineage.
func TestMentions_LineageCarriesOnlyWhatWasGranted(t *testing.T) {
	const home = "/home/nanite-test-user"
	useHome(t, home)
	g := NewPathGrants()
	g.RegisterFromUserMessage("parent", "see ~/.ssh/config and ~/dev/proj/main.go")
	g.RegisterLineage("child", "parent")

	if g.IsPathAllowed("child", home+"/.ssh/config") {
		t.Fatal("a worker inherited a grant for ~/.ssh/config")
	}
	for _, p := range g.ListLineageGrants("child") {
		if strings.Contains(p, ".ssh") || p == home {
			t.Fatalf("lineage grants include %q", p)
		}
	}
	if !g.IsPathAllowed("child", home+"/dev/proj/other.go") {
		t.Fatal("the permitted mention was not inherited")
	}
}
