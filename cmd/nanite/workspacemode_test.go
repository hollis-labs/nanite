package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/hollis-labs/nanite/internal/headless/headlesstest"
)

// wsGateway is a local OpenAI-compatible model that gives the steps in order (then "fin") and
// records every request body.
type wsGateway struct {
	mu     sync.Mutex
	bodies []map[string]any
}

func (g *wsGateway) requests() []map[string]any {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]map[string]any(nil), g.bodies...)
}

func newWSGateway(t *testing.T, steps ...step) *wsGateway {
	t.Helper()
	g := &wsGateway{}
	var n atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		g.mu.Lock()
		g.bodies = append(g.bodies, body)
		g.mu.Unlock()
		k := int(n.Add(1)) - 1
		s := step{text: "fin"}
		if k < len(steps) {
			s = steps[k]
		}
		w.Header().Set("Content-Type", "text/event-stream")
		send := func(delta any, finish string) {
			fin := "null"
			if finish != "" {
				fin = `"` + finish + `"`
			}
			d, _ := json.Marshal(delta)
			_, _ = fmt.Fprintf(w, `data: {"id":"f","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":%s,"finish_reason":%s}]}`+"\n\n", d, fin)
		}
		if s.tool != "" {
			send(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": fmt.Sprintf("call_%d", k), "type": "function",
				"function": map[string]any{"name": s.tool, "arguments": s.args}}}}, "")
			send(map[string]any{}, "tool_calls")
		} else {
			send(map[string]any{"content": s.text}, "")
			send(map[string]any{}, "stop")
		}
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	key := filepath.Join(t.TempDir(), "mock-key")
	if err := os.WriteFile(key, []byte("unit-test-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolved, _ := filepath.EvalSymlinks(key)
	t.Setenv("NANITE_GATEWAY_URL", server.URL+"/v1")
	t.Setenv("NANITE_GATEWAY_KEY_REF", "file://"+resolved)
	return g
}

// wsFixture is a user home with a Nanite home (host.yaml for the local model, no agent.md yet)
// and a project with a workspace two levels down.
type wsFixture struct {
	userHome, home, project, ws string
}

func newWSFixture(t *testing.T) wsFixture {
	t.Helper()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	f := wsFixture{userHome: filepath.Join(root, "me")}
	f.home = filepath.Join(f.userHome, ".nanite")
	f.project = filepath.Join(f.userHome, "code", "app")
	f.ws = filepath.Join(f.project, "web")
	for _, d := range []string{f.home, f.ws} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", f.userHome)
	t.Setenv("NANITE_HOME", f.home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "xdg"))
	writeFile(t, filepath.Join(f.home, "host.yaml"), "kind: stable\nlifetime: on-demand\nseat_id: nanite\nruntime:\n  provider: openai\n  model: home-model\n")
	return f
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// runWS runs nanite in workspace mode in dir, as runArgs does past argv parsing.
func runWS(t *testing.T, dir, stdin string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var errb bytes.Buffer
	ws, err := detectWorkspace(false, dir, args, &errb)
	if err != nil {
		return "", errb.String(), err
	}
	if ws == nil {
		t.Fatalf("%s ran as an agent directory", dir)
	}
	ctx := context.Background()
	started, err := start(ctx, ws.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer started.Stop(ctx)
	root := newRootFor(started.App, dir, ws)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(args)
	err = root.ExecuteContext(ctx)
	return out.String(), errb.String(), err
}

func systemText(t *testing.T, body map[string]any) string {
	t.Helper()
	b, _ := json.Marshal(body["messages"])
	return string(b)
}

func TestWorkspaceModeLikeClaude(t *testing.T) {
	f := newWSFixture(t)
	g := newWSGateway(t, step{text: "one"}, step{text: "two"}, step{text: "three"}, step{text: "four"})
	writeFile(t, filepath.Join(f.home, "AGENTS.md"), "RULE-GLOBAL")
	writeFile(t, filepath.Join(f.userHome, "AGENTS.md"), "RULE-ABOVE-THE-WALK")
	writeFile(t, filepath.Join(f.project, "AGENTS.md"), "RULE-PROJECT")
	writeFile(t, filepath.Join(f.ws, "CLAUDE.md"), "RULE-WS-CLAUDE")
	writeFile(t, filepath.Join(f.project, ".nanite", "settings.json"), `{"model":"project-model","hooks":{}}`)

	out, stderr, err := runWS(t, f.ws, "", "-p", "hello", "--output-format", "json", "--append-system-prompt", "RULE-FLAG")
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	// The home got its base agent.md (host.yaml was there); the person is told once.
	if b, _ := os.ReadFile(filepath.Join(f.home, "agent.md")); !strings.Contains(string(b), "You are Nanite") ||
		!strings.Contains(stderr, "set up "+f.home+" (agent.md)") || !strings.Contains(stderr, "not used: hooks") {
		t.Fatalf("home setup: %s", stderr)
	}
	var r struct {
		Result    string `json:"result"`
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil || r.Result != "one" {
		t.Fatalf("%v %s", err, out)
	}
	// State is per session under the home, named by the session id.
	if _, err := os.Stat(filepath.Join(f.home, "projects", projectKeyFor(f.ws), r.SessionID, "nanite.db")); err != nil {
		t.Fatalf("session state: %v", err)
	}
	reqs := g.requests()
	sys := systemText(t, reqs[0])
	order := []string{"You are Nanite", "RULE-GLOBAL", "RULE-PROJECT", "RULE-WS-CLAUDE", "RULE-FLAG"}
	last := -1
	for _, want := range order {
		i := strings.Index(sys, want)
		if i < 0 || i < last {
			t.Fatalf("instruction %q missing or out of order in %s", want, sys)
		}
		last = i
	}
	if strings.Contains(sys, "RULE-ABOVE-THE-WALK") || reqs[0]["model"] != "project-model" {
		t.Fatalf("walk past $HOME or model %v", reqs[0]["model"])
	}

	// --continue picks that session up; --resume names it; --model wins over the project's.
	out, _, err = runWS(t, f.ws, "", "-p", "again", "--output-format", "json", "--continue", "--model", "flag-model")
	if err != nil || !strings.Contains(out, `"session_id":"`+r.SessionID+`"`) || !strings.Contains(out, `"result":"two"`) {
		t.Fatalf("continue: %v %s", err, out)
	}
	if got := g.requests()[1]["model"]; got != "flag-model" {
		t.Fatalf("model %v", got)
	}
	if out, _, err = runWS(t, f.ws, "", "-p", "third", "--output-format", "json", "--resume", r.SessionID); err != nil || !strings.Contains(out, r.SessionID) {
		t.Fatalf("resume: %v %s", err, out)
	}
	if _, _, err := runWS(t, f.ws, "", "-p", "x", "--resume", "nope"); err == nil || !strings.Contains(err.Error(), "no session nope") {
		t.Fatalf("unknown session: %v", err)
	}
	// Without --continue a run is a new session.
	out, _, _ = runWS(t, f.ws, "", "-p", "fresh", "--output-format", "json")
	if strings.Contains(out, r.SessionID) {
		t.Fatalf("a plain run reused the session: %s", out)
	}
}

func TestWorkspaceModeKeepsTheHomeOutOfReach(t *testing.T) {
	f := newWSFixture(t)
	writeFile(t, filepath.Join(f.home, "permissions.yaml"), "permissions:\n  mode: default\n")
	args, _ := json.Marshal(map[string]string{"path": filepath.Join(f.home, "host.yaml")})
	newWSGateway(t, step{tool: "read_file", args: string(args)}, step{text: "done"})
	out, stderr, err := runWS(t, f.ws, "", "-p", "read it", "--output-format", "stream-json")
	if err != nil {
		t.Fatalf("%v %s", err, stderr)
	}
	if !strings.Contains(out, `"is_error":true`) || strings.Contains(out, "seat_id") {
		t.Fatalf("the home was readable:\n%s", out)
	}
}

func TestWorkspaceProjectMCPServesThePromptTool(t *testing.T) {
	f := newWSFixture(t)
	target := filepath.Join(f.ws, "note.txt")
	args, _ := json.Marshal(map[string]string{"path": target, "content": "hi"})
	newWSGateway(t, step{tool: "write_file", args: string(args)}, step{text: "done"})
	b, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"approver": approveServerFor(t, "deny")}})
	writeFile(t, filepath.Join(f.project, ".mcp.json"), string(b))
	out, stderr, err := runWS(t, f.ws, `{"type":"user","message":{"role":"user","content":"write"}}`+"\n",
		"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--permission-mode", "default",
		"--permission-prompt-tool", "mcp__approver__approve")
	if err != nil {
		t.Fatalf("%v %s", err, stderr)
	}
	if _, err := os.Stat(target); err == nil || !strings.Contains(out, `"tool_name":"write_file"`) {
		t.Fatalf("the project's approver did not refuse the write:\n%s", out)
	}
}

func projectKeyFor(ws string) string {
	var b strings.Builder
	for _, r := range ws {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

func approveServerFor(t *testing.T, behavior string) any {
	t.Helper()
	return headlesstest.Server(t, behavior, "")
}

func TestAgentDirectoriesAreNotWorkspaces(t *testing.T) {
	f := newWSFixture(t)
	agentDir := t.TempDir()
	if err := initAgent(agentDir); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		given bool
		dir   string
	}{{true, f.ws}, {false, agentDir}, {true, agentDir}} {
		if ws, err := detectWorkspace(c.given, c.dir, nil, io.Discard); err != nil || ws != nil {
			t.Errorf("%+v: workspace mode %v %v", c, ws, err)
		}
	}
	if _, err := os.Stat(filepath.Join(f.home, "agent.md")); err == nil {
		t.Fatal("an agent-directory run set up the home")
	}
	if flagValue([]string{"-p", "x", "--session-id=abc"}, "--session-id") != "abc" ||
		flagValue([]string{"--", "--resume", "x"}, "--resume") != "" || !hasFlag([]string{"--continue"}, "--continue") {
		t.Fatal("flag scan")
	}
	_, given, _, _ := extractDirGiven([]string{"--agent-dir", agentDir, "-p", "x"})
	if !given {
		t.Fatal("--agent-dir not seen")
	}
}
