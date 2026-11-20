package subagent

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/authored"
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
	// MCP readOnlyHint is the server's word; the built-in planner does not take it.
	if variants["planner"].Allows("mcp__notes__search", true) {
		t.Error("planner allows an MCP tool")
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
		"lookout.md":  "---\nread_only: true\ncan_spawn: true\n---\nBody\n",
		"nomodel.md":  "---\nprovider: small\n---\nBody\n",
	} {
		write(t, dir, name, body)
	}
	variants, err := Load(dir)
	if err == nil {
		t.Fatal("bad variants loaded without error")
	}
	for _, file := range []string{"typo", "empty", "Bad_Name", "worker", "wall", "neg", "open", "glob", "lookout", "nomodel"} {
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
		"bad schema": func(r *Request) { r.ReturnSpec = json.RawMessage(`{"type":42}`) },
		"not prose":  func(r *Request) { r.ReturnSpec = json.RawMessage(`[1,2]`) },
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
	if msg := base.Message(); !strings.Contains(msg, "Return: a list\n") || !strings.HasSuffix(msg, "Finish by calling return_result with the result. Only what you pass to it reaches your parent.") {
		t.Fatalf("message %q", msg)
	}
	if resolved, err := schema.Schema(); err != nil || resolved == nil {
		t.Fatalf("schema %v, %v", resolved, err)
	}
	if resolved, err := base.Schema(); err != nil || resolved != nil {
		t.Fatalf("prose spec gave a schema: %v, %v", resolved, err)
	}
}

func TestResultCapBytes(t *testing.T) {
	if got := ResultCapBytes(Variant{}, 0, 0); got != DefaultResultCap*BytesPerToken {
		t.Errorf("default %d", got)
	}
	if got := ResultCapBytes(Variant{}, 1000, 0); got != 4000 {
		t.Errorf("agent cap %d", got)
	}
	if got := ResultCapBytes(Variant{Variant: authored.Variant{ResultCap: 50}}, 1000, 0); got != 200 {
		t.Errorf("variant cap %d", got)
	}
	if got := ResultCapBytes(Variant{}, 0, 23*1024); got != 23*1024/BytesPerToken*BytesPerToken {
		t.Errorf("the default is not clamped to the limit: %d", got)
	}
	// A setting near the int limit is clamped before it is multiplied.
	if got := ResultCapBytes(Variant{Variant: authored.Variant{ResultCap: math.MaxInt}}, 0, 23*1024); got <= 0 || got > 23*1024 {
		t.Errorf("a huge cap overflowed: %d", got)
	}
}

func TestCheckLimit(t *testing.T) {
	for n, ok := range map[int]bool{0: true, 1: true, 8: true, 9: false, -1: false} {
		if err := CheckLimit(n); (err == nil) != ok {
			t.Errorf("CheckLimit(%d) = %v", n, err)
		}
	}
}

func TestReturnResultAlwaysOfferedToAChild(t *testing.T) {
	narrow := Variant{Variant: authored.Variant{Name: "narrow", ReadOnly: true, Allow: []string{"read_file"}, Deny: []string{"return_*"}}}
	if !AllowsTool(false, []Variant{narrow}, false, ReturnToolName, false) {
		t.Error("a variant took return_result away from its child")
	}
}

func TestAllowsToolNeedsTheWholeChain(t *testing.T) {
	lead := Variant{Variant: authored.Variant{Name: "lead", CanSpawn: true, Deny: []string{"write_file", "subtask"}}}
	code := Builtins()["code"]
	if AllowsTool(false, []Variant{lead, code}, false, "write_file", false) {
		t.Error("a child was allowed a tool its lead denies")
	}
	if !AllowsTool(false, []Variant{lead, code}, false, "read_file", true) {
		t.Error("a child lost a tool both allow")
	}
	if AllowsTool(false, []Variant{lead}, true, "subtask", false) {
		t.Error("can_spawn overrode the variant's own deny list")
	}
	if !AllowsTool(false, []Variant{{Variant: authored.Variant{Name: "x", CanSpawn: true}}}, true, "subtask", false) || AllowsTool(false, []Variant{code}, false, "subtask", false) {
		t.Error("subtask does not follow canSpawn")
	}
	if !AllowsTool(true, nil, true, "anything", false) || AllowsTool(false, []Variant{code}, false, "ask_user", true) {
		t.Error("root or ask_user handling changed")
	}
}

func TestResultReadersFollowDenyButNotAllow(t *testing.T) {
	allowOnly := Variant{Variant: authored.Variant{Name: "narrow", Allow: []string{"read_file"}}}
	denying := Variant{Variant: authored.Variant{Name: "sealed", Allow: []string{"read_file"}, Deny: []string{"fetch_tool_result", "search_*"}}}
	for _, tool := range []string{"fetch_tool_result", "search_tool_result"} {
		if !AllowsTool(false, []Variant{allowOnly}, false, tool, true) {
			t.Errorf("an allow list that does not name %s removed it", tool)
		}
		if AllowsTool(false, []Variant{denying}, false, tool, true) {
			t.Errorf("an explicit deny did not remove %s", tool)
		}
		if AllowsTool(false, []Variant{denying, allowOnly}, false, tool, true) {
			t.Errorf("a lead's deny did not remove %s from its child", tool)
		}
	}
}

// TestBuiltinNamesMatchAuthored: the names agent/authored refuses for an
// authored variant are exactly the built-in variants.
func TestBuiltinNamesMatchAuthored(t *testing.T) {
	if got, want := Names(Builtins()), slices.Sorted(slices.Values(authored.BuiltinVariants())); !slices.Equal(got, want) {
		t.Fatalf("built-in variants %v, authored.BuiltinVariants %v", got, want)
	}
}
