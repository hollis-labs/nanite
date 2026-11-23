package headless

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/hollis-labs/libs/agent/agentdir"
	rules "github.com/hollis-labs/libs/security/policy/permission"
	"github.com/hollis-labs/libs/security/secrets"

	"github.com/hollis-labs/nanite/internal/mcpclient"
	"github.com/hollis-labs/nanite/internal/permission"
	"github.com/hollis-labs/nanite/internal/tools"
)

// Claude Code's flags and settings, read for Nanite.

// PermissionMode maps a claude --permission-mode (or Nanite's own name) to Nanite's mode.
func PermissionMode(m string) (string, error) {
	switch m {
	case "", "default":
		return m, nil
	case "acceptEdits", "accept-edits":
		return "accept-edits", nil
	case "plan":
		return "plan", nil
	case "bypassPermissions", "yolo":
		return "yolo", nil
	case "dontAsk":
		// Claude denies what isn't pre-approved; Nanite's default mode asks, and with no gate
		// an ask is denied, which is the same outcome headless.
		return "default", nil
	}
	return "", fmt.Errorf("unknown permission mode %q (default, acceptEdits, plan, bypassPermissions)", m)
}

// claudeTools are Claude Code's tool names and Nanite's equivalents.
var claudeTools = map[string]string{
	"Bash": "bash", "Read": "read_file", "Write": "write_file", "Edit": "edit_file", "MultiEdit": "edit_file",
	"Glob": "find_files", "Grep": "search_files", "LS": "list_dir", "TodoWrite": "todo_write", "Task": "subtask",
}

// Rule turns one claude permission rule ("Bash", "Bash(git log:*)", "Read(src/**)", "mcp__srv",
// "mcp__srv__tool") into a Nanite rule with the given behavior.
func Rule(entry, behavior string) (rules.Rule, error) {
	entry = strings.TrimSpace(entry)
	tool, spec := entry, ""
	if i := strings.IndexByte(entry, '('); i > 0 && strings.HasSuffix(entry, ")") {
		tool, spec = entry[:i], entry[i+1:len(entry)-1]
	}
	if tool == "" {
		return rules.Rule{}, fmt.Errorf("empty permission rule")
	}
	if n, ok := claudeTools[tool]; ok {
		tool = n
	}
	// "mcp__srv" covers every tool of that server.
	if strings.HasPrefix(tool, "mcp__") && strings.Count(tool, "__") == 1 {
		tool += "__*"
	}
	spec = strings.TrimSpace(spec)
	if strings.HasSuffix(spec, ":*") { // claude's prefix match: "git log:*"
		spec = strings.TrimSuffix(spec, ":*") + "*"
	}
	r := rules.Rule{Tool: tool, Pattern: spec, Behavior: rules.Decision(behavior), Source: "settings"}
	return r, nil
}

// Settings is what Nanite reads from a claude --settings file: permissions, env and model.
// Other keys (hooks, sandbox, statusLine, ...) are ignored.
type Settings struct {
	Permissions struct {
		Allow                 []string `json:"allow"`
		Ask                   []string `json:"ask"`
		Deny                  []string `json:"deny"`
		DefaultMode           string   `json:"defaultMode"`
		AdditionalDirectories []string `json:"additionalDirectories"`
	} `json:"permissions"`
	Env   map[string]string `json:"env"`
	Model string            `json:"model"`
	// Ignored lists the top-level keys Nanite does not use, so the caller can say so.
	Ignored []string `json:"-"`
}

// Rules are the settings' permission rules.
func (s Settings) Rules() ([]rules.Rule, error) {
	var out []rules.Rule
	for _, g := range []struct {
		entries  []string
		behavior string
	}{{s.Permissions.Deny, "deny"}, {s.Permissions.Ask, "ask"}, {s.Permissions.Allow, "allow"}} {
		for _, e := range g.entries {
			r, err := Rule(e, g.behavior)
			if err != nil {
				return nil, err
			}
			out = append(out, r)
		}
	}
	return out, nil
}

// readJSONArg is a flag's JSON: inline when it starts with "{", else the file it names.
func readJSONArg(v string) ([]byte, error) {
	if s := strings.TrimSpace(v); strings.HasPrefix(s, "{") {
		return []byte(s), nil
	}
	return os.ReadFile(v) // #nosec G304 -- the caller names the file on the command line.
}

// ReadSettings reads a --settings value (a file or inline JSON).
func ReadSettings(v string) (Settings, error) {
	raw, err := readJSONArg(v)
	if err != nil {
		return Settings{}, fmt.Errorf("--settings: %w", err)
	}
	var s Settings
	if err := json.Unmarshal(raw, &s); err != nil {
		return Settings{}, fmt.Errorf("--settings: %w", err)
	}
	var all map[string]json.RawMessage
	_ = json.Unmarshal(raw, &all)
	for k := range all {
		switch k {
		case "permissions", "env", "model":
		default:
			s.Ignored = append(s.Ignored, k)
		}
	}
	return s, nil
}

// ReadMCPConfig reads --mcp-config values (files or inline JSON, Claude's {"mcpServers":{...}}),
// later ones replacing servers of the same name.
func ReadMCPConfig(values []string) (agentdir.MCPConfig, error) {
	out := agentdir.MCPConfig{Servers: map[string]agentdir.MCPServer{}}
	for _, v := range values {
		raw, err := readJSONArg(v)
		if err != nil {
			return out, fmt.Errorf("--mcp-config: %w", err)
		}
		cfg, err := agentdir.ParseMCP(raw)
		if err != nil {
			return out, fmt.Errorf("--mcp-config %s: %w", v, err)
		}
		for name, s := range cfg.Servers {
			out.Servers[name] = s
		}
	}
	return out, nil
}

// PromptGate answers approvals by calling an MCP tool, as claude's --permission-prompt-tool
// mcp__<server>__<tool> does. The tool gets {"tool_name","input","tool_use_id"} and answers
// with text holding {"behavior":"allow"|"deny","message"?,"updatedInput"?}.
type PromptGate struct {
	name   string // mcp__server__tool
	server string
	cfg    agentdir.MCPServer
	client *mcpclient.Client

	mu    sync.Mutex
	lease *mcpclient.Lease
	call  func(context.Context, tools.Call) (tools.Result, error)
}

// NewPromptGate is the gate for tool name (mcp__server__tool), whose server is in servers.
func NewPromptGate(name string, servers agentdir.MCPConfig, dir string, resolver *secrets.Resolver, logger *slog.Logger) (*PromptGate, error) {
	rest, ok := strings.CutPrefix(name, "mcp__")
	server, _, ok2 := strings.Cut(rest, "__")
	if !ok || !ok2 || server == "" {
		return nil, fmt.Errorf("--permission-prompt-tool %q: give mcp__<server>__<tool>", name)
	}
	cfg, ok := servers.Servers[server]
	if !ok {
		return nil, fmt.Errorf("--permission-prompt-tool %q: no MCP server %q (add it with --mcp-config)", name, server)
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &PromptGate{name: name, server: server, cfg: cfg, client: mcpclient.New(dir, resolver, nil, logger, nil)}, nil
}

// tool connects to the server once and finds the tool.
func (g *PromptGate) tool(ctx context.Context) (func(context.Context, tools.Call) (tools.Result, error), error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.call != nil {
		return g.call, nil
	}
	lease, warnings := g.client.Reconcile(ctx, agentdir.MCPConfig{Servers: map[string]agentdir.MCPServer{g.server: g.cfg}})
	list, more := lease.Tools(ctx)
	for _, t := range list {
		if t.Name == g.name {
			g.lease, g.call = lease, t.Handler
			return g.call, nil
		}
	}
	lease.Close()
	return nil, fmt.Errorf("permission prompt tool %s not found: %s", g.name, strings.Join(append(warnings, more...), "; "))
}

// Decide implements permission.Gate.
func (g *PromptGate) Decide(ctx context.Context, req permission.PermissionRequest) (permission.PermissionDecision, error) {
	call, err := g.tool(ctx)
	if err != nil {
		// No tool to ask is a refusal, as with no gate; the turn goes on.
		return permission.PermissionDecision{Reason: err.Error()}, nil //nolint:nilerr // a failed ask denies the call
	}
	input := req.Input
	if len(bytes.TrimSpace(input)) == 0 || !json.Valid(input) {
		input = json.RawMessage(`{}`)
	}
	args, _ := json.Marshal(map[string]any{"tool_name": req.Tool, "input": input, "tool_use_id": req.ID})
	res, err := call(ctx, tools.Call{Input: args})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return permission.PermissionDecision{}, err
		}
		return permission.PermissionDecision{Reason: "permission prompt tool failed: " + err.Error()}, nil //nolint:nilerr // a failed ask denies the call
	}
	var answer struct {
		Behavior string `json:"behavior"`
		Message  string `json:"message"`
	}
	if res.IsError || json.Unmarshal([]byte(strings.TrimSpace(res.Text)), &answer) != nil {
		return permission.PermissionDecision{Reason: "permission prompt tool gave no decision: " + res.Text}, nil //nolint:nilerr // an unreadable answer denies the call
	}
	if answer.Behavior == "allow" {
		return permission.PermissionDecision{Allow: true, Human: true, Reason: "allowed by " + g.name}, nil
	}
	return permission.PermissionDecision{Human: true, Reason: firstNonEmpty(answer.Message, "denied by "+g.name)}, nil
}

// Close releases the connection.
func (g *PromptGate) Close() error {
	g.mu.Lock()
	if g.lease != nil {
		g.lease.Close()
	}
	g.mu.Unlock()
	return g.client.Close()
}
