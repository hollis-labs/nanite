// Package headlesstest is a fixture for tests of --permission-prompt-tool: a stdio MCP server
// with one tool, "approve", that answers claude's permission prompt shape. A test binary serves
// it when re-run with NANITE_TEST_APPROVE_MCP set; call ServeIfAsked first thing in TestMain.
package headlesstest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/hollis-labs/libs/agent/agentdir"
)

const (
	envBehavior = "NANITE_TEST_APPROVE_MCP"
	envLog      = "NANITE_TEST_APPROVE_LOG"
)

// Server is the MCP registration that runs this test binary as the fixture: every call is
// answered with behavior ("allow" or "deny"), and its arguments are appended to log, if set.
func Server(t testing.TB, behavior, log string) agentdir.MCPServer {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return agentdir.MCPServer{Type: "stdio", Command: exe, Env: map[string]string{envBehavior: behavior, envLog: log}}
}

// ServeIfAsked serves the fixture on stdio and returns true when this process was started as
// it; otherwise it returns false at once.
func ServeIfAsked() bool {
	behavior := os.Getenv(envBehavior)
	if behavior == "" {
		return false
	}
	serve(behavior, os.Getenv(envLog))
	return true
}

func serve(behavior, logPath string) {
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	out := json.NewEncoder(os.Stdout)
	for sc.Scan() {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(sc.Bytes(), &req) != nil || len(req.ID) == 0 {
			continue
		}
		var result any
		switch req.Method {
		case "initialize":
			var p struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			_ = json.Unmarshal(req.Params, &p)
			result = map[string]any{"protocolVersion": p.ProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}},
				"serverInfo": map[string]any{"name": "approve-fixture", "version": "1"}}
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{"name": "approve", "description": "approve a tool call",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"tool_name": map[string]any{"type": "string"}, "input": map[string]any{"type": "object"}}}}}}
		case "tools/call":
			var p struct {
				Arguments json.RawMessage `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &p)
			if logPath != "" {
				if f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil { // #nosec G304 G703 -- the test names its own temp file
					_, _ = fmt.Fprintln(f, string(p.Arguments))
					_ = f.Close()
				}
			}
			answer := `{"behavior":"allow","updatedInput":{}}`
			if behavior != "allow" {
				answer = `{"behavior":"deny","message":"not today"}`
			}
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": answer}}}
		default:
			result = map[string]any{}
		}
		_ = out.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}
}
