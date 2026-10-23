package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/hollis-labs/nanite/internal/store"
)

// host_mcp.go projects a CLI host's mcp_servers (names from the global MCP
// registry) into the Claude boot dir (CW-20261023-0023).
//
// A CLI runtime spawns and calls an MCP server itself; Nanite's permission
// engine never sees the call. So the grant has to be enforced where the CLI
// enforces it. For Claude that is the headless permission gate: an MCP tool
// with no allow rule is refused (no TTY to ask), and the allow rule is carried
// on argv (claude_mcp_allow.go). The projection is therefore fail-closed:
//
//   - The servers are planted in .mcp.json, so strict-MCP mode loads them.
//   - An allow rule `mcp__<server>__<tool>` is added only for a tool the
//     host's native authority grants (LocalAuthority.ResolveToolNames, the same
//     read API agents use). No grant, no rule, every call refused.
//   - A grant makes a tool available; it does not approve a use, and for a CLI
//     it is the only gate there is. Grants are per tool name, not per server,
//     so a granted name is allowed on every listed server that exposes it.
//
// Other runtimes are refused rather than planted without a gate: Codex and
// OpenCode run MCP tools with no per-tool allow Nanite can set, and the
// Antigravity headless gate is unverified. HostMCPSupported is the one
// predicate; admission and launch both use it.

// HostMCPSource resolves a CLI host's MCP servers and tool grants at launch.
type HostMCPSource interface {
	// MCPServer returns the registry row for name, or nil when absent.
	MCPServer(ctx context.Context, name string) (*store.MCPServerConfig, error)
	// GrantedTools returns the tool names the host settings revision's native
	// authority currently grants.
	GrantedTools(ctx context.Context, hostSettingsID, revision string) ([]string, error)
}

// HostMCPServer is one registry server rendered for a CLI's MCP file.
type HostMCPServer struct {
	Name    string
	Command string
	Args    []string
	Env     map[string]string
}

// HostMCPLaunch is a launch's resolved host MCP projection.
type HostMCPLaunch struct {
	Servers []HostMCPServer
	// AllowRules are the Claude permission rules the grants produce.
	AllowRules []string
}

// HostMCPSupported reports whether a CLI provider can carry host mcp_servers
// with the grant enforced (see the file comment).
func HostMCPSupported(providerName string) bool {
	return normalizeProviderName(providerName) == "claude"
}

// ValidateHostMCPServer returns why a registry row cannot be projected into a
// CLI launch, or nil. ownID is the planted Nanite server's name, which a host
// server must not shadow.
func ValidateHostMCPServer(name string, row *store.MCPServerConfig, ownID string) error {
	switch {
	case row == nil:
		return fmt.Errorf("mcp server %q is not registered", name)
	case !row.Enabled:
		return fmt.Errorf("mcp server %q is disabled", name)
	case row.TransportType != store.TransportStdio || row.Command == "":
		return fmt.Errorf("mcp server %q is not a stdio server; only stdio servers can be projected into a CLI agent", name)
	case name == ownID:
		return fmt.Errorf("mcp server %q would shadow the server Nanite plants for the agent", name)
	case strings.ContainsAny(name, " \t\n,*"):
		return fmt.Errorf("mcp server name %q cannot be used in a permission rule", name)
	}
	return nil
}

func hostMCPServer(row *store.MCPServerConfig) (HostMCPServer, error) {
	srv := HostMCPServer{Name: row.Name, Command: row.Command}
	if row.Args != "" {
		if err := json.Unmarshal([]byte(row.Args), &srv.Args); err != nil {
			return srv, fmt.Errorf("mcp server %q: args are not a JSON array: %w", row.Name, err)
		}
	}
	var env []string
	if row.Env != "" {
		if err := json.Unmarshal([]byte(row.Env), &env); err != nil {
			return srv, fmt.Errorf("mcp server %q: env is not a JSON array: %w", row.Name, err)
		}
	}
	if len(env) > 0 {
		srv.Env = make(map[string]string, len(env))
		for _, kv := range env {
			k, v, _ := strings.Cut(kv, "=")
			srv.Env[k] = v
		}
	}
	return srv, nil
}

// hostMCPResolveTimeout bounds the registry and grant reads of a launch.
const hostMCPResolveTimeout = 5 * time.Second

// errHostMCPNoGate is why a launch that runs without Claude's permission gate
// cannot carry host servers: the allow rules are the only thing that keeps an
// ungranted tool from running, and --dangerously-skip-permissions removes the
// gate they feed.
var errHostMCPNoGate = errors.New("the session runs without the permission gate (yolo mode launches Claude with --dangerously-skip-permissions), so host mcp_servers would run every tool whether granted or not; leave yolo mode or remove mcp_servers from the host settings")

// applyHostMCP resolves a CLI host's mcp_servers into params. ctx carries the
// turn's execution capability, which is where the submitting credential and
// the turn's narrowing come from. A failure is recorded on params and fails the
// boot at plant time; it never leaves the servers planted without their gate.
func applyHostMCP(ctx context.Context, deps *Dependencies, opts Options, profile *store.AgentProfile, params *SetupParams) {
	if profile == nil || profile.NativeHost == nil || len(profile.NativeHost.MCPServers) == 0 {
		return
	}
	if deps.yoloMode() {
		params.HostMCPErr = errHostMCPNoGate
		return
	}
	var src HostMCPSource
	if deps != nil {
		src = deps.HostMCP
	}
	ctx, cancel := context.WithTimeout(ctx, hostMCPResolveTimeout)
	defer cancel()
	params.HostMCP, params.HostMCPErr = resolveHostMCP(ctx, src, profile, effectiveProvider(opts, profile), mcpServerID(params.MCPConfig))
}

// resolveHostMCP resolves profile's host mcp_servers. A nil result means the
// host lists none. Any server that cannot be projected, or a runtime that
// cannot enforce the grant, is an error: the launch is refused, never
// planted open.
func resolveHostMCP(ctx context.Context, src HostMCPSource, profile *store.AgentProfile, providerName, ownID string) (*HostMCPLaunch, error) {
	if profile == nil || profile.NativeHost == nil || len(profile.NativeHost.MCPServers) == 0 {
		return nil, nil
	}
	if !HostMCPSupported(providerName) {
		return nil, fmt.Errorf("host mcp_servers are not supported for provider %q: no per-tool gate Nanite can set", providerName)
	}
	if src == nil {
		return nil, fmt.Errorf("host mcp_servers are listed but this composition root has no MCP registry wired")
	}
	out := &HostMCPLaunch{}
	seen := map[string]bool{}
	for _, name := range profile.NativeHost.MCPServers {
		if seen[name] {
			continue
		}
		seen[name] = true
		row, err := src.MCPServer(ctx, name)
		if err != nil {
			return nil, err
		}
		if err = ValidateHostMCPServer(name, row, ownID); err != nil {
			return nil, err
		}
		srv, err := hostMCPServer(row)
		if err != nil {
			return nil, err
		}
		out.Servers = append(out.Servers, srv)
	}
	granted, err := src.GrantedTools(ctx, profile.HostSettingsID, profile.Revision)
	if err != nil {
		return nil, err
	}
	sort.Strings(granted)
	for _, srv := range out.Servers {
		for _, tool := range granted {
			out.AllowRules = append(out.AllowRules, "mcp__"+srv.Name+"__"+tool)
		}
	}
	return out, nil
}
