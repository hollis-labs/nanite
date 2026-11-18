package descriptor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/substrate/kinds"
	"gopkg.in/yaml.v3"
)

const app = "nanite"

// root is the repository root, three levels above this package.
func root(t *testing.T) string {
	t.Helper()
	d, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func readYAML(t *testing.T, name string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root(t), name))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := yaml.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return m
}

// at walks nested maps/slices by key or index and returns the value ("" path elements are not allowed).
func at(t *testing.T, v any, path ...any) any {
	t.Helper()
	for _, p := range path {
		switch k := p.(type) {
		case string:
			m, ok := v.(map[string]any)
			if !ok {
				t.Fatalf("path %v: %v is not a map", path, v)
			}
			v = m[k]
		case int:
			s, ok := v.([]any)
			if !ok || k >= len(s) {
				t.Fatalf("path %v: no element %d", path, k)
			}
			v = s[k]
		}
	}
	return v
}

func TestProjectYamlValidates(t *testing.T) {
	p, err := Load(filepath.Join(root(t), "project.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != app {
		t.Errorf("project.yaml id = %q, want %q (the id is the repository name)", p.ID, app)
	}
	if p.Class != kinds.ClassTool {
		t.Errorf("class = %v, want 2: a class change is a promotion, not an edit", p.Class)
	}
}

func TestValidateRejects(t *testing.T) {
	for name, doc := range map[string]string{
		"unknown key": "schema: project/1\nid: a\nname: A\nclass: 2\nlifecycle: active\nvisibility: public\nowner: user:x\ninterfaces: [{type: cli}]\nlast_verified: today\n",
		"no owner":    "schema: project/1\nid: a\nname: A\nclass: 2\nlifecycle: active\nvisibility: public\ninterfaces: [{type: cli}]\n",
		"bad enum":    "schema: project/1\nid: a\nname: A\nclass: 2\nlifecycle: soon\nvisibility: public\nowner: user:x\ninterfaces: [{type: cli}]\n",
		"bad auth":    "schema: project/1\nid: a\nname: A\nclass: 2\nlifecycle: active\nvisibility: public\nowner: user:x\ninterfaces: [{type: cli, auth: open}]\n",
	} {
		if _, err := Parse([]byte(doc)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// TestFleetDescriptorAgrees: deploy.yaml is for the same app, and project.yaml points at it.
func TestFleetDescriptorAgrees(t *testing.T) {
	p, err := Load(filepath.Join(root(t), "project.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Deploy == nil {
		t.Fatal("class 2 tools require deployment metadata")
	}
	if p.Deploy.Fleet != "deploy.yaml" {
		t.Fatalf("project.yaml deploy.fleet = %q, want deploy.yaml", p.Deploy.Fleet)
	}
	d := readYAML(t, p.Deploy.Fleet)
	if d["schema"] != "fleet-deploy/v1" {
		t.Errorf("deploy.yaml schema = %v", d["schema"])
	}
	if d["app"] != p.ID {
		t.Errorf("deploy.yaml app = %v, project.yaml id = %q", d["app"], p.ID)
	}
	if got := at(t, d, "targets", 0, "binary"); got != p.ID {
		t.Errorf("deploy.yaml binary = %v, want %q", got, p.ID)
	}
}
