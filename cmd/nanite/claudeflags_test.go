package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hollis-labs/nanite/internal/headless/headlesstest"
)

func TestMain(m *testing.M) {
	if headlesstest.ServeIfAsked() {
		return
	}
	os.Exit(m.Run())
}

// step is one scripted model reply: text, or one tool call.
type step struct{ text, tool, args string }

// scriptedAgent is an agent directory whose model (a local OpenAI-compatible stub) gives the
// steps in order, then "fin". It records each request body's model.
func scriptedAgent(t *testing.T, models *[]string, steps ...step) string {
	t.Helper()
	dir := t.TempDir()
	if err := initAgent(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "host.yaml"), []byte("kind: stable\nlifetime: on-demand\nseat_id: local-agent\nruntime:\n  provider: openai\n  model: test-model\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var n atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if models != nil {
			*models = append(*models, body.Model)
		}
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
			_, _ = fmt.Fprintf(w, `data: {"id":"f","object":"chat.completion.chunk","created":1,"model":"test-model","choices":[{"index":0,"delta":%s,"finish_reason":%s}]}`+"\n\n", d, fin)
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
	return dir
}

// mcpFile writes a --mcp-config with the approve fixture under "exo_runner", as exo names it.
func mcpFile(t *testing.T, behavior, log string) string {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"exo_runner": headlesstest.Server(t, behavior, log)}})
	p := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func results(t *testing.T, out string) []map[string]any {
	t.Helper()
	var rs []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("%q: %v", line, err)
		}
		if m["type"] == "result" {
			rs = append(rs, m)
		}
	}
	return rs
}

func TestPermissionPromptToolDecidesEdits(t *testing.T) {
	for _, behavior := range []string{"deny", "allow"} {
		t.Run(behavior, func(t *testing.T) {
			work := t.TempDir()
			target := filepath.Join(work, "note.txt")
			args, _ := json.Marshal(map[string]string{"path": target, "content": "hi"})
			dir := scriptedAgent(t, nil, step{tool: "write_file", args: string(args)}, step{text: "done"})
			log := filepath.Join(t.TempDir(), "calls.log")
			// exo's claude argv: stream-json both ways, an MCP config holding the prompt server.
			out, err := runRoot(t, dir, `{"type":"user","message":{"role":"user","content":"write it"}}`+"\n",
				"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
				"--mcp-config", mcpFile(t, behavior, log), "--permission-prompt-tool", "mcp__exo_runner__approve",
				"--add-dir", work, "--setting-sources", "project,local")
			if err != nil {
				t.Fatal(err)
			}
			rs := results(t, out)
			if len(rs) != 1 || rs[0]["result"] != "done" {
				t.Fatalf("results %v\n%s", rs, out)
			}
			denials, _ := rs[0]["permission_denials"].([]any)
			_, statErr := os.Stat(target)
			calls, _ := os.ReadFile(log) // #nosec G304 -- the test's own temp file
			if !strings.Contains(string(calls), `"tool_name":"write_file"`) {
				t.Fatalf("the prompt tool was not asked: %q", calls)
			}
			switch behavior {
			case "deny":
				if statErr == nil || len(denials) != 1 || denials[0].(map[string]any)["tool_name"] != "write_file" {
					t.Fatalf("denied: file %v, denials %v", statErr, denials)
				}
			case "allow":
				if statErr != nil || len(denials) != 0 {
					t.Fatalf("allowed: file %v, denials %v", statErr, denials)
				}
			}
		})
	}
}

func TestClaudeFlagsModelSettingsAndRules(t *testing.T) {
	var models []string
	work := t.TempDir()
	target := filepath.Join(work, "ok.txt")
	args, _ := json.Marshal(map[string]string{"path": target, "content": "x"})
	dir := scriptedAgent(t, &models, step{tool: "write_file", args: string(args)}, step{text: "ok"})
	// --settings allows write_file (Claude's "Write"), so no gate is needed; --model wins over host.yaml.
	settings := `{"permissions":{"allow":["Write"]},"hooks":{"Stop":[]}}`
	out, err := runRoot(t, dir, "", "-p", "go", "--output-format", "json", "--model", "flag-model", "--settings", settings,
		"--add-dir", work, "--append-system-prompt", "Be terse.")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); err != nil || !strings.Contains(out, `"result":"ok"`) {
		t.Fatalf("allowed by settings: %v %s", err, out)
	}
	if len(models) == 0 || models[0] != "flag-model" {
		t.Fatalf("models %v", models)
	}
	// bypassPermissions writes without asking; an unknown mode is refused.
	args2, _ := json.Marshal(map[string]string{"path": filepath.Join(work, "yolo.txt"), "content": "y"})
	dir2 := scriptedAgent(t, nil, step{tool: "write_file", args: string(args2)}, step{text: "ok"})
	if _, err := runRoot(t, dir2, "", "-p", "go", "--dangerously-skip-permissions", "--add-dir", work); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(work, "yolo.txt")); err != nil {
		t.Fatal("bypassPermissions did not write")
	}
	if _, err := runRoot(t, dir2, "", "-p", "go", "--permission-mode", "sometimes"); err == nil {
		t.Fatal("unknown mode accepted")
	}
	// The prompt tool needs -p.
	if _, err := runRoot(t, dir2, "", "--permission-prompt-tool", "mcp__exo_runner__approve", "--mcp-config", mcpFile(t, "allow", "")); err == nil {
		t.Fatal("prompt tool in the terminal accepted")
	}
}
