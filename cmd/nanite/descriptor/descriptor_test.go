package descriptor

import (
	"path/filepath"
	"testing"

	"github.com/hollis-labs/substrate/kinds"
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

func TestProjectHasNoDeployment(t *testing.T) {
	p, err := Load(filepath.Join(root(t), "project.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Deploy != nil {
		t.Fatal("the local CLI must not declare a deployment target")
	}
}
