package headless

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/agent/agentdir"

	"github.com/hollis-labs/nanite/internal/headless/headlesstest"
	"github.com/hollis-labs/nanite/internal/permission"
)

// TestMain doubles as the fixture permission-prompt MCP server (headlesstest).
func TestMain(m *testing.M) {
	if headlesstest.ServeIfAsked() {
		return
	}
	os.Exit(m.Run())
}

// approveServer is an MCP config with the fixture server under "gate".
func approveServer(t *testing.T, behavior, log string) agentdir.MCPConfig {
	t.Helper()
	return agentdir.MCPConfig{Servers: map[string]agentdir.MCPServer{"gate": headlesstest.Server(t, behavior, log)}}
}

func TestPromptGateAsksTheTool(t *testing.T) {
	log := filepath.Join(t.TempDir(), "calls.log")
	for behavior, allow := range map[string]bool{"allow": true, "deny": false} {
		g, err := NewPromptGate("mcp__gate__approve", approveServer(t, behavior, log), t.TempDir(), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		d, err := g.Decide(t.Context(), permission.PermissionRequest{ID: "perm-1", Tool: "write_file", Input: json.RawMessage(`{"path":"a.txt"}`)})
		_ = g.Close()
		if err != nil || d.Allow != allow || !d.Human {
			t.Fatalf("%s: %+v %v", behavior, d, err)
		}
		if !allow && d.Reason != "not today" {
			t.Fatalf("deny reason %q", d.Reason)
		}
	}
	b, _ := os.ReadFile(log) // #nosec G304 -- the test's own temp file
	if !strings.Contains(string(b), `"tool_name":"write_file"`) || !strings.Contains(string(b), `"input":{"path":"a.txt"}`) {
		t.Fatalf("calls: %s", b)
	}
	// A missing server or a malformed name is refused up front; a missing tool denies.
	if _, err := NewPromptGate("mcp__nope__approve", approveServer(t, "allow", ""), t.TempDir(), nil, nil); err == nil {
		t.Fatal("missing server accepted")
	}
	if _, err := NewPromptGate("approve", approveServer(t, "allow", ""), t.TempDir(), nil, nil); err == nil {
		t.Fatal("bad name accepted")
	}
	g, _ := NewPromptGate("mcp__gate__other", approveServer(t, "allow", ""), t.TempDir(), nil, nil)
	defer g.Close()
	if d, err := g.Decide(t.Context(), permission.PermissionRequest{Tool: "bash"}); err != nil || d.Allow || !strings.Contains(d.Reason, "not found") {
		t.Fatalf("missing tool: %+v %v", d, err)
	}
}

func TestClaudeModesRulesSettingsAndMCP(t *testing.T) {
	for in, want := range map[string]string{"": "", "default": "default", "acceptEdits": "accept-edits", "plan": "plan", "bypassPermissions": "yolo", "yolo": "yolo", "dontAsk": "default"} {
		if got, err := PermissionMode(in); err != nil || got != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	if _, err := PermissionMode("auto"); err == nil {
		t.Error("unknown mode accepted")
	}
	for in, want := range map[string]string{
		"mcp__exo":           "mcp__exo__* allow",
		"mcp__exo__whoami":   "mcp__exo__whoami allow",
		"Bash(git log:*)":    "bash|git log* allow",
		"Read":               "read_file allow",
		"Edit(src/**)":       "edit_file|src/** allow",
		"custom_tool(x y z)": "custom_tool|x y z allow",
	} {
		r, err := Rule(in, "allow")
		got := r.Tool
		if r.Pattern != "" {
			got += "|" + r.Pattern
		}
		if err != nil || got+" "+string(r.Behavior) != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}

	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.json")
	_ = os.WriteFile(settings, []byte(`{"hooks":{"Stop":[]},"permissions":{"allow":["mcp__exo"],"deny":["Bash(rm:*)"],"defaultMode":"acceptEdits","additionalDirectories":["/srv/data"]},"env":{"A":"1"},"model":"m-2","sandbox":{}}`), 0o600)
	s, err := ReadSettings(settings)
	if err != nil {
		t.Fatal(err)
	}
	rs, _ := s.Rules()
	if len(rs) != 2 || rs[0].Behavior != "deny" || rs[1].Tool != "mcp__exo__*" || s.Model != "m-2" || s.Env["A"] != "1" ||
		s.Permissions.DefaultMode != "acceptEdits" || strings.Join(s.Permissions.AdditionalDirectories, ",") != "/srv/data" {
		t.Fatalf("settings %+v rules %+v", s, rs)
	}
	if strings.Join(slices.Sorted(slices.Values(s.Ignored)), ",") != "hooks,sandbox" {
		t.Fatalf("ignored %v", s.Ignored)
	}
	if s, err := ReadSettings(`{"model":"inline"}`); err != nil || s.Model != "inline" {
		t.Fatalf("inline: %+v %v", s, err)
	}

	mcp1 := filepath.Join(dir, "mcp.json")
	_ = os.WriteFile(mcp1, []byte(`{"mcpServers":{"a":{"type":"stdio","command":"one"},"b":{"type":"http","url":"https://mcp.example.net/b"}}}`), 0o600)
	cfg, err := ReadMCPConfig([]string{mcp1, `{"mcpServers":{"a":{"command":"two"}}}`})
	if err != nil || len(cfg.Servers) != 2 || cfg.Servers["a"].Command != "two" || cfg.Servers["b"].URL != "https://mcp.example.net/b" {
		t.Fatalf("mcp %+v %v", cfg, err)
	}
	if _, err := ReadMCPConfig([]string{filepath.Join(dir, "missing.json")}); err == nil {
		t.Fatal("missing file accepted")
	}
}
