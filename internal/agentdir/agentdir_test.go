package agentdir

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testDefinition = "---\nschema_version: \"2\"\ndefinition_id: def:dir-agent\nrevision: \"1\"\nname: dir-agent\ndescription: Directory agent.\nbehavior:\n  purpose: Answer.\nrequirements: {}\nharness_profile:\n  context: {}\n  permissions:\n    profile: default\ncontinuity:\n  mode: ephemeral\n---\nBody.\n"

func write(t *testing.T, root, name, body string) {
	t.Helper()
	p := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadReadsDefinitionHostAndSkills(t *testing.T) {
	root := t.TempDir()
	work := t.TempDir()
	write(t, root, "agent.md", testDefinition)
	write(t, root, "host.yaml", "runtime: cli\nprovider: claude\nmodel: haiku\nmode: pinned\ndirectories:\n  - "+work+"\n")
	write(t, root, "skills/review/SKILL.md", "# review\n")
	d, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if d.Parsed.DefinitionID != "def:dir-agent" || d.Host.Runtime != "cli" || d.Host.Mode != ModePinned || d.Host.Version != "1" {
		t.Fatalf("%+v %+v", d.Parsed, d.Host)
	}
	if len(d.Skills) != 1 || d.Skills[0] != "review" {
		t.Fatalf("skills = %v", d.Skills)
	}
}

func TestLoadRefusals(t *testing.T) {
	work := t.TempDir()
	for name, tc := range map[string]struct{ host, want string }{
		"unknown field":      {"runtime: api\nbogus: 1\n", "bogus"},
		"bad runtime":        {"runtime: wasm\n", "runtime"},
		"bad mode":           {"runtime: api\nmode: sometimes\n", "mode"},
		"relative directory": {"runtime: api\ndirectories: [rel]\n", "absolute"},
		"cli without dirs":   {"runtime: cli\nprovider: claude\n", "directories"},
		"second document":    {"runtime: api\n---\nruntime: cli\nprovider: claude\n", "more than one"},
		"malformed trailing": {"runtime: api\n---\n: : [\n", "host.yaml"},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			write(t, root, "agent.md", testDefinition)
			write(t, root, "host.yaml", tc.host)
			if _, err := Load(root); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
	t.Run("missing host.yaml", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "agent.md", testDefinition)
		if _, err := Load(root); err == nil {
			t.Fatal("loaded without host.yaml")
		}
	})
	t.Run("host.yaml symlinked out", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "agent.md", testDefinition)
		write(t, work, "elsewhere.yaml", "runtime: api\n")
		if err := os.Symlink(filepath.Join(work, "elsewhere.yaml"), filepath.Join(root, "host.yaml")); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(root); err == nil {
			t.Fatal("followed a symlink out of the directory")
		}
	})
}

func TestDiscover(t *testing.T) {
	root := t.TempDir()
	if _, err := Discover(root); err == nil {
		t.Fatal("found an agent in an empty directory")
	}
	write(t, root, ".nanite/agent.md", testDefinition)
	got, err := Discover(root)
	if err != nil || got != filepath.Join(root, ".nanite") {
		t.Fatalf("%q %v", got, err)
	}
	write(t, root, "agent.md", testDefinition)
	if got, _ = Discover(root); got != root {
		t.Fatalf("direct agent.md should win, got %q", got)
	}
}
