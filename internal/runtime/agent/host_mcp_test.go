package agent

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/harness/core/adapters"
	"github.com/hollis-labs/libs/llm/core/contracts/runtimes"
	permissionlib "github.com/hollis-labs/libs/security/policy/permission"
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
	for p, want := range map[string]bool{"claude": true, "codex": false, "opencode": false, "antigravity": true, "agy": true, "copilot": false} {
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

func hostMCPDeps(src HostMCPSource, mode permissionlib.Mode) *Dependencies {
	return &Dependencies{HostMCP: src, MCPConfig: MCPConfig{BinaryPath: "/bin/nanite", DBPath: "/db"}, Permissions: permissionlib.NewEngine(mode, nil)}
}

func TestApplyHostMCP_ResolvesAndFailsClosed(t *testing.T) {
	src := &fakeHostMCP{rows: map[string]*store.MCPServerConfig{"tether": tetherRow()}, granted: []string{"tether_message_send"}}
	profile := hostProfile("tether")
	opts := Options{Provider: "claude", Workdir: t.TempDir()}

	params := SetupParams{MCPConfig: MCPConfig{BinaryPath: "/bin/nanite", DBPath: "/db"}, SessionID: "s"}
	applyHostMCP(context.Background(), hostMCPDeps(src, permissionlib.ModeDefault), opts, profile, &params)
	if params.HostMCPErr != nil || params.HostMCP == nil || !slices.Equal(params.HostMCP.AllowRules, []string{"mcp__tether__tether_message_send"}) {
		t.Fatalf("params: %+v %v", params.HostMCP, params.HostMCPErr)
	}
	body, err := mcpConfigBytes(params)
	if err != nil || !strings.Contains(string(body), `"tether"`) {
		t.Fatalf("planted: %s %v", body, err)
	}

	// No registry wired: the boot must fail, not plant a partial file.
	params = SetupParams{MCPConfig: MCPConfig{BinaryPath: "/bin/nanite", DBPath: "/db"}}
	applyHostMCP(context.Background(), &Dependencies{}, opts, profile, &params)
	if params.HostMCPErr == nil {
		t.Fatal("expected a resolution error with no registry wired")
	}
	if _, err := mcpConfigBytes(params); err == nil {
		t.Fatal("boot must fail rather than plant without the host servers")
	}

	// A host that lists nothing is untouched, whatever the mode.
	params = SetupParams{}
	applyHostMCP(context.Background(), hostMCPDeps(src, permissionlib.ModeYolo), opts, hostProfile(), &params)
	if params.HostMCP != nil || params.HostMCPErr != nil {
		t.Fatalf("no mcp_servers: %+v %v", params.HostMCP, params.HostMCPErr)
	}
}

// In yolo mode Claude is launched with --dangerously-skip-permissions, which
// would run every tool whether granted or not: the boot is refused.
func TestApplyHostMCP_RefusedInYoloMode(t *testing.T) {
	src := &fakeHostMCP{rows: map[string]*store.MCPServerConfig{"tether": tetherRow()}, granted: []string{"tether_message_send"}}
	params := SetupParams{MCPConfig: MCPConfig{BinaryPath: "/bin/nanite", DBPath: "/db"}}
	applyHostMCP(context.Background(), hostMCPDeps(src, permissionlib.ModeYolo), Options{Provider: "claude"}, hostProfile("tether"), &params)
	if !errors.Is(params.HostMCPErr, errHostMCPNoGate) || params.HostMCP != nil {
		t.Fatalf("yolo: %+v %v", params.HostMCP, params.HostMCPErr)
	}
	if _, err := mcpConfigBytes(params); err == nil {
		t.Fatal("yolo launch planted the host servers")
	}
}

func claudeArgv(t *testing.T, deps *Dependencies, x launchExtra) []string {
	t.Helper()
	a, err := selectAdapter(deps, RuntimeSelection{Runtime: runtimes.Claude, Mode: runtimes.ModeStreamingStdio}, t.TempDir(), t.TempDir(), x)
	if err != nil {
		t.Fatal(err)
	}
	return a.(adapters.RuntimeAdapter).CLIAdapter().BuildArgs("hi", "", "")
}

// The grants reach Claude's argv, and only a gated launch drops user settings.
func TestSelectAdapter_CarriesHostMCPAllowRulesAndIsolatesSettings(t *testing.T) {
	deps := hostMCPDeps(nil, permissionlib.ModeDefault)
	argv := claudeArgv(t, deps, launchExtra{HostMCPGated: true, HostMCPAllow: []string{"mcp__tether__tether_message_send"}})
	if !slices.Contains(argv, "--allowedTools=mcp__nanite__*,mcp__tether__tether_message_send") {
		t.Fatalf("allow rule missing from %q", argv)
	}
	if i := slices.Index(argv, "--setting-sources"); i < 0 || argv[i+1] != "project,local" {
		t.Fatalf("user settings not excluded: %q", argv)
	}
	plain := claudeArgv(t, deps, launchExtra{})
	if slices.Contains(plain, "--setting-sources") {
		t.Fatalf("a launch without host servers must keep the user's settings: %q", plain)
	}
	// Gated but nothing granted: still isolated, no host rule.
	none := claudeArgv(t, deps, launchExtra{HostMCPGated: true})
	if !slices.Contains(none, "--setting-sources") || !slices.Contains(none, "--allowedTools=mcp__nanite__*") {
		t.Fatalf("gated launch with no grants: %q", none)
	}
}

func TestSelectAdapter_RefusesYoloForAHostMCPLaunch(t *testing.T) {
	deps := hostMCPDeps(nil, permissionlib.ModeYolo)
	_, err := selectAdapter(deps, RuntimeSelection{Runtime: runtimes.Claude, Mode: runtimes.ModeStreamingStdio}, t.TempDir(), t.TempDir(), launchExtra{HostMCPGated: true})
	if !errors.Is(err, errHostMCPNoGate) {
		t.Fatalf("err = %v", err)
	}
	if _, err = selectAdapter(deps, RuntimeSelection{Runtime: runtimes.Claude, Mode: runtimes.ModeStreamingStdio}, t.TempDir(), t.TempDir(), launchExtra{}); err != nil {
		t.Fatalf("yolo without host servers must still launch: %v", err)
	}
}

func TestCLILaunchExtra_CarriesTheHostGate(t *testing.T) {
	profile := &store.AgentProfile{DefaultModel: "haiku"}
	params := SetupParams{CLIWritableRoots: []string{"/w"}, HostMCP: &HostMCPLaunch{AllowRules: []string{"mcp__t__a"}}}
	got := cliLaunchExtra(profile, params, false)
	if got.Model != "haiku" || !got.HostMCPGated || !slices.Equal(got.HostMCPAllow, []string{"mcp__t__a"}) || !slices.Equal(got.AddDirs, []string{"/w"}) {
		t.Fatalf("extra = %+v", got)
	}
	if got := cliLaunchExtra(profile, SetupParams{}, false); got.HostMCPGated || got.HostMCPAllow != nil {
		t.Fatalf("no host servers must not gate: %+v", got)
	}
	if got := cliLaunchExtra(profile, params, true); got.HostMCPGated || got.Model != "" {
		t.Fatalf("ACP launches take none: %+v", got)
	}
}

// A server name is spliced into a permission rule, so it may not carry rule
// syntax.
func TestValidateHostMCPServer_RejectsNamesThatBreakARule(t *testing.T) {
	ok := func(name string) *store.MCPServerConfig {
		return &store.MCPServerConfig{Name: name, TransportType: store.TransportStdio, Command: "x", Enabled: true}
	}
	if err := ValidateHostMCPServer("tether", ok("tether"), "nanite"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a b", "a,b", "a*", "a\tb", "a\nb"} {
		if err := ValidateHostMCPServer(name, ok(name), "nanite"); err == nil {
			t.Errorf("name %q accepted", name)
		}
	}
}
