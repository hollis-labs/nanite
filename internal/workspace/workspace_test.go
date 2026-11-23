package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestHomeAndEnsureHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("NANITE_HOME", filepath.Join(dir, "nh"))
	home, err := Home()
	if err != nil || home != filepath.Join(dir, "nh") {
		t.Fatalf("%q %v", home, err)
	}
	if IsAgentDir(home) {
		t.Fatal("empty home is an agent dir")
	}
	wrote, err := EnsureHome(home, "agent body", "host body")
	if err != nil || len(wrote) != 2 || !IsAgentDir(home) {
		t.Fatalf("%v %v", wrote, err)
	}
	// Files the person has are kept.
	write(t, filepath.Join(home, "agent.md"), "mine")
	if wrote, _ := EnsureHome(home, "agent body", "host body"); len(wrote) != 0 {
		t.Fatalf("rewrote %v", wrote)
	}
	if b, _ := os.ReadFile(filepath.Join(home, "agent.md")); string(b) != "mine" { // #nosec G304 -- the test's own temp dir
		t.Fatal("agent.md replaced")
	}
	t.Setenv("NANITE_HOME", "")
	t.Setenv("HOME", dir)
	if home, _ := Home(); home != filepath.Join(dir, ".nanite") {
		t.Fatalf("default home %q", home)
	}
}

func TestSessions(t *testing.T) {
	home := t.TempDir()
	ws := "/srv/work/my app"
	if k := projectKey(ws); k != "-srv-work-my-app" {
		t.Fatalf("key %q", k)
	}
	for _, bad := range []string{"", ".", "..", "a/b", `a\b`} {
		if _, err := SessionDir(home, ws, bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if s, err := LatestSession(home, ws); err != nil || s != "" {
		t.Fatalf("no sessions: %q %v", s, err)
	}
	old, _ := SessionDir(home, ws, "s-old")
	recent, _ := SessionDir(home, ws, "s-new")
	write(t, filepath.Join(old, "nanite.db"), "x")
	write(t, filepath.Join(recent, "nanite.db"), "x")
	past := time.Now().Add(-time.Hour)
	_ = os.Chtimes(filepath.Join(old, "nanite.db"), past, past)
	if s, _ := LatestSession(home, ws); s != "s-new" {
		t.Fatalf("latest %q", s)
	}
	// Another workspace's sessions are not this one's.
	if s, _ := LatestSession(home, "/srv/other"); s != "" {
		t.Fatalf("other workspace %q", s)
	}
}

func TestAncestors(t *testing.T) {
	got := strings.Join(Ancestors("/u/me/code/app", "/u/me"), ",")
	if got != "/u/me/code,/u/me/code/app" {
		t.Fatalf("under home: %s", got)
	}
	if got := strings.Join(Ancestors("/srv/a", "/u/me"), ","); got != "/,/srv,/srv/a" {
		t.Fatalf("outside home: %s", got)
	}
	if got := strings.Join(Ancestors("/u/me", "/u/me"), ","); got != "/u/me" {
		t.Fatalf("home itself: %s", got)
	}
}

func TestInstructionsAndProject(t *testing.T) {
	root := t.TempDir()
	userHome := filepath.Join(root, "me")
	home := filepath.Join(userHome, ".nanite")
	ws := filepath.Join(userHome, "code", "app", "web")
	write(t, filepath.Join(home, "AGENTS.md"), "global")
	write(t, filepath.Join(userHome, "AGENTS.md"), "above the walk") // $HOME itself is not walked
	write(t, filepath.Join(userHome, "code", "CLAUDE.md"), "code claude")
	write(t, filepath.Join(userHome, "code", "app", "AGENTS.md"), "app agents")
	write(t, filepath.Join(userHome, "code", "app", "CLAUDE.md"), "app claude (AGENTS.md wins)")
	write(t, filepath.Join(ws, "AGENTS.md"), "  \n") // blank: skipped
	layers, err := Instructions(home, ws, userHome)
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, l := range layers {
		texts = append(texts, l.Text)
	}
	if strings.Join(texts, "|") != "global|code claude|app agents" {
		t.Fatalf("layers %q", texts)
	}
	write(t, filepath.Join(ws, "AGENTS.md"), strings.Repeat("x", maxFile+1))
	if _, err := Instructions(home, ws, userHome); err == nil {
		t.Fatal("an oversized file was read")
	}

	write(t, filepath.Join(home, "settings.json"), "{}")
	write(t, filepath.Join(userHome, "code", ".nanite", "settings.json"), "{}")
	write(t, filepath.Join(userHome, "code", "app", ".mcp.json"), "{}")
	write(t, filepath.Join(ws, ".nanite", "settings.json"), "{}")
	write(t, filepath.Join(ws, ".nanite", "skills", "s", "SKILL.md"), "x")
	p := FindProject(home, ws, userHome)
	rel := func(paths []string) string {
		var out []string
		for _, s := range paths {
			r, _ := filepath.Rel(root, s)
			out = append(out, r)
		}
		return strings.Join(out, ",")
	}
	if rel(p.Settings) != "me/.nanite/settings.json,me/code/.nanite/settings.json,me/code/app/web/.nanite/settings.json" ||
		rel(p.MCP) != "me/code/app/.mcp.json" || rel(p.SkillDirs) != "me/code/app/web/.nanite/skills" {
		t.Fatalf("project %+v", p)
	}
}
