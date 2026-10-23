package agent

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/llm/core/contracts/runtimes"
	"github.com/hollis-labs/nanite/internal/store"
)

type fakeHostMCP struct {
	rows    map[string]*store.MCPServerConfig
	granted []string
	grantID string
	grantRv string
	err     error
}

func (f *fakeHostMCP) MCPServer(_ context.Context, name string) (*store.MCPServerConfig, error) {
	return f.rows[name], f.err
}

func (f *fakeHostMCP) GrantedTools(_ context.Context, id, rev string) ([]string, error) {
	f.grantID, f.grantRv = id, rev
	return slices.Clone(f.granted), nil
}

func tetherRow() *store.MCPServerConfig {
	return &store.MCPServerConfig{Name: "tether", TransportType: store.TransportStdio, Command: "tether", Enabled: true,
		Args: `["mcp","--token-file","/t"]`, Env: `["TETHER_MCP_SCOPES=message.write"]`}
}

func hostProfile(servers ...string) *store.AgentProfile {
	return &store.AgentProfile{HostSettingsID: "host-1", Revision: "rev-7",
		NativeHost: &store.NativeHostSettings{Runtime: "cli", Provider: "claude", MCPServers: servers}}
}

func TestResolveHostMCP_AllowsOnlyGrantedTools(t *testing.T) {
	src := &fakeHostMCP{rows: map[string]*store.MCPServerConfig{"tether": tetherRow()}, granted: []string{"tether_message_send", "agent_list"}}
	got, err := resolveHostMCP(context.Background(), src, hostProfile("tether", "tether"), "claude", "nanite")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Servers) != 1 || got.Servers[0].Command != "tether" || got.Servers[0].Env["TETHER_MCP_SCOPES"] != "message.write" {
		t.Fatalf("servers = %+v", got.Servers)
	}
	want := []string{"mcp__tether__agent_list", "mcp__tether__tether_message_send"}
	if !slices.Equal(got.AllowRules, want) {
		t.Fatalf("rules = %v, want %v", got.AllowRules, want)
	}
	if src.grantID != "host-1" || src.grantRv != "rev-7" {
		t.Fatalf("grants read for %q@%q", src.grantID, src.grantRv)
	}

	// No grant: the server is planted, nothing is allowed -- fail closed.
	src.granted = nil
	got, err = resolveHostMCP(context.Background(), src, hostProfile("tether"), "claude", "nanite")
	if err != nil || len(got.Servers) != 1 || len(got.AllowRules) != 0 {
		t.Fatalf("no grant: %+v, %v", got, err)
	}
}

func TestResolveHostMCP_Refusals(t *testing.T) {
	rows := map[string]*store.MCPServerConfig{"tether": tetherRow(), "off": {Name: "off", TransportType: store.TransportStdio, Command: "x"}}
	src := &fakeHostMCP{rows: rows}
	ctx := context.Background()
	for name, tc := range map[string]struct {
		src      HostMCPSource
		profile  *store.AgentProfile
		provider string
	}{
		"missing server":   {src, hostProfile("nope"), "claude"},
		"disabled server":  {src, hostProfile("off"), "claude"},
		"shadows nanite":   {&fakeHostMCP{rows: map[string]*store.MCPServerConfig{"nanite": {Name: "nanite", TransportType: store.TransportStdio, Command: "x", Enabled: true}}}, hostProfile("nanite"), "claude"},
		"codex":            {src, hostProfile("tether"), "codex"},
		"opencode":         {src, hostProfile("tether"), "opencode"},
		"no registry":      {nil, hostProfile("tether"), "claude"},
		"registry failure": {&fakeHostMCP{err: errors.New("db down")}, hostProfile("tether"), "claude"},
	} {
		if got, err := resolveHostMCP(ctx, tc.src, tc.profile, tc.provider, "nanite"); err == nil || got != nil {
			t.Errorf("%s: got %+v, err %v; want refusal", name, got, err)
		}
	}
	if got, err := resolveHostMCP(ctx, nil, hostProfile(), "codex", "nanite"); got != nil || err != nil {
		t.Errorf("no mcp_servers must be a no-op for any provider: %+v %v", got, err)
	}
}

func TestHostMCPSupported(t *testing.T) {
	for p, want := range map[string]bool{"claude": true, "codex": false, "opencode": false, "antigravity": false, "copilot": false} {
		if got := HostMCPSupported(p); got != want {
			t.Errorf("HostMCPSupported(%q) = %v", p, got)
		}
	}
}

func TestRenderMCPJSONWith_PlantsHostServersBesideNanite(t *testing.T) {
	cfg := MCPConfig{BinaryPath: "/bin/nanite", DBPath: "/db"}
	body, err := renderMCPJSONWith(cfg, "sess", false, []HostMCPServer{{Name: "tether", Command: "tether", Args: []string{"mcp"}, Env: map[string]string{"A": "b"}}})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.MCPServers["nanite"].Command != "/bin/nanite" || doc.MCPServers["tether"].Command != "tether" || doc.MCPServers["tether"].Env["A"] != "b" {
		t.Fatalf("doc = %+v", doc)
	}
	plain, _ := renderMCPJSON(cfg, "sess", false)
	same, _ := renderMCPJSONWith(cfg, "sess", false, nil)
	if plain != same {
		t.Fatal("no host servers must render exactly what renderMCPJSON does")
	}
}

func TestClaudeMCPAllowArgs_HostRulesShareOneFlag(t *testing.T) {
	got := claudeMCPAllowArgs(runtimes.Claude, "/boot", testMCPConfig, "mcp__tether__a", "mcp__tether__b")
	want := []string{"--allowedTools=mcp__nanite__*,mcp__tether__a,mcp__tether__b"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q", got)
	}
	if claudeMCPAllowArgs(runtimes.Codex, "/boot", testMCPConfig, "mcp__tether__a") != nil {
		t.Fatal("host rules must not reach another runtime")
	}
}

func TestComposeBootdirParams_ResolvesHostMCPAndFailsClosed(t *testing.T) {
	src := &fakeHostMCP{rows: map[string]*store.MCPServerConfig{"tether": tetherRow()}, granted: []string{"tether_message_send"}}
	deps := &Dependencies{HostMCP: src, MCPConfig: MCPConfig{BinaryPath: "/bin/nanite", DBPath: "/db"}}
	profile := hostProfile("tether")
	profile.DefaultProvider = "claude"
	_, params := composeBootdirParams(deps, Options{Provider: "claude", Workdir: t.TempDir()}, profile, "s")
	if params.HostMCPErr != nil || params.HostMCP == nil || !slices.Equal(params.HostMCP.AllowRules, []string{"mcp__tether__tether_message_send"}) {
		t.Fatalf("params: %+v %v", params.HostMCP, params.HostMCPErr)
	}
	body, err := mcpConfigBytes(params)
	if err != nil || !strings.Contains(string(body), `"tether"`) {
		t.Fatalf("planted: %s %v", body, err)
	}

	// A launch that could not resolve its servers must not plant a partial file.
	broken := &Dependencies{MCPConfig: deps.MCPConfig}
	_, params = composeBootdirParams(broken, Options{Provider: "claude", Workdir: t.TempDir()}, profile, "s")
	if params.HostMCPErr == nil {
		t.Fatal("expected a resolution error with no registry wired")
	}
	if _, err := mcpConfigBytes(params); err == nil {
		t.Fatal("boot must fail rather than plant without the host servers")
	}
}
