package subagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "variants"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "variants", name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadAuthoredVariants(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "lead.md", "---\ndescription: Runs a council.\ntools:\n  allow: [read_file, \"mcp__*\"]\n  deny: [mcp__danger]\nmodel: claude-opus-5-5\nbudgets:\n  steps: 12\n  tokens: 50000\n  wall: 5m\n  cost: 0.5\nlast_n: 4\nresult_cap: 3000\ncan_spawn: true\n---\nYou lead.\n")
	write(t, dir, "notes.txt", "ignored")
	variants, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	lead := variants["lead"]
	if lead.Builtin || !lead.CanSpawn || lead.Model != "claude-opus-5-5" || lead.Budgets.Steps != 12 || lead.Budgets.Tokens != 50000 ||
		lead.Budgets.Wall != 5*time.Minute || lead.Budgets.Cost != 0.5 || lead.LastN != 4 || lead.ResultCap != 3000 || lead.Prompt != "You lead." {
		t.Fatalf("lead %+v", lead)
	}
	for _, c := range []struct {
		tool     string
		readOnly bool
		want     bool
	}{{"read_file", true, true}, {"mcp__search", false, true}, {"mcp__danger", false, false}, {"bash", false, false}} {
		if got := lead.Allows(c.tool, c.readOnly); got != c.want {
			t.Errorf("lead allows %s: %v", c.tool, got)
		}
	}
	for _, name := range []string{"code", "research", "worker", "planner", "lead"} {
		if _, ok := variants[name]; !ok {
			t.Errorf("missing %s", name)
		}
	}
	if variants["planner"].Allows("write_file", false) || !variants["planner"].Allows("read_file", true) {
		t.Error("planner is not read-only")
	}
}

func TestLoadRejectsBadVariantsButKeepsGoodOnes(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "good.md", "Plain body, no frontmatter.\n")
	for name, body := range map[string]string{
		"typo.md":     "---\ncan_spawm: true\n---\nBody\n",
		"empty.md":    "---\nmodel: x\n---\n\n",
		"Bad_Name.md": "Body\n",
		"worker.md":   "Body\n",
		"wall.md":     "---\nbudgets:\n  wall: soon\n---\nBody\n",
		"neg.md":      "---\nbudgets:\n  steps: -1\n---\nBody\n",
		"open.md":     "---\nmodel: x\nBody\n",
		"glob.md":     "---\ntools:\n  allow: [\"[\"]\n---\nBody\n",
	} {
		write(t, dir, name, body)
	}
	variants, err := Load(dir)
	if err == nil {
		t.Fatal("bad variants loaded without error")
	}
	for _, file := range []string{"typo", "empty", "Bad_Name", "worker", "wall", "neg", "open", "glob"} {
		if !strings.Contains(err.Error(), "variants/"+file+".md") {
			t.Errorf("no error for %s: %v", file, err)
		}
	}
	if _, ok := variants["good"]; !ok || variants["worker"].Builtin != true {
		t.Fatalf("good variant lost or built-in replaced: %+v", variants)
	}
}

func TestRequestValidateAndMessage(t *testing.T) {
	variants := Builtins()
	base := Request{Goal: "find it", Variant: "research", ReturnSpec: json.RawMessage(`"a list"`)}
	if err := base.Validate(variants); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Request){
		"no variant": func(r *Request) { r.Variant = "" },
		"unknown":    func(r *Request) { r.Variant = "astrologer" },
		"no goal":    func(r *Request) { r.Goal = " " },
		"no spec":    func(r *Request) { r.ReturnSpec = nil },
		"null spec":  func(r *Request) { r.ReturnSpec = json.RawMessage("null") },
		"empty spec": func(r *Request) { r.ReturnSpec = json.RawMessage(`""`) },
	} {
		r := base
		mutate(&r)
		if err := r.Validate(variants); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	schema := base
	schema.ReturnSpec = json.RawMessage(`{"type":"object"}`)
	schema.Inputs = json.RawMessage(`{"path":"a.go"}`)
	if msg := schema.Message(); !strings.HasPrefix(msg, "Goal: find it\n") || !strings.Contains(msg, `{"path":"a.go"}`) || !strings.Contains(msg, `JSON Schema:`+"\n"+`{"type":"object"}`) {
		t.Fatalf("message %q", msg)
	}
	if msg := base.Message(); !strings.HasSuffix(msg, "Return: a list") {
		t.Fatalf("message %q", msg)
	}
}
