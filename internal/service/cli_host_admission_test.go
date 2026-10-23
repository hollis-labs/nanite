package service

import (
	"context"
	"errors"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func cliHost(mod func(*store.NativeHostSettings)) store.NativeHostSettings {
	h := store.NativeHostSettings{Version: "1", Runtime: "cli", Provider: "claude", Model: "haiku", Directories: []string{"/tmp"}}
	if mod != nil {
		mod(&h)
	}
	return h
}

func TestAdmitCLIHostAcceptsClaudeWithDirectory(t *testing.T) {
	got, err := admitCLIHost(cliHost(nil), "default", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != (ModelSelection{Provider: "claude", Model: "haiku"}) {
		t.Fatalf("selection = %+v", got)
	}
	// An empty native_loop and no model are the defaults, not unsupported knobs.
	if _, err := admitCLIHost(cliHost(func(h *store.NativeHostSettings) { h.Model = "" }), "", nil); err != nil {
		t.Fatalf("no model: %v", err)
	}
}

func TestAdmitCLIHostRefusalsCarryACause(t *testing.T) {
	limit := 3
	for _, tc := range []struct {
		name    string
		host    store.NativeHostSettings
		profile string
		req     *ModelSelection
		cause   string
	}{
		{"no provider", cliHost(func(h *store.NativeHostSettings) { h.Provider = "" }), "default", nil, CauseProviderNotLaunchable},
		{"unknown provider", cliHost(func(h *store.NativeHostSettings) { h.Provider = "no-such-cli" }), "default", nil, CauseProviderNotLaunchable},
		{"read-only definition", cliHost(nil), "read-only", nil, CauseReadOnlyUnenforceable},
		{"harness profile", cliHost(func(h *store.NativeHostSettings) { h.HarnessProfile = "strict" }), "default", nil, CauseCLIHostUnsupportedField},
		{"recall limit", cliHost(func(h *store.NativeHostSettings) { h.RecallLimit = &limit }), "default", nil, CauseCLIHostUnsupportedField},
		{"mcp servers on a runtime whose grants cannot be enforced", cliHost(func(h *store.NativeHostSettings) {
			h.Provider = "copilot"
			h.Model = ""
			h.MCPServers = []string{"files"}
		}), "default", nil, CauseCLIHostUnsupportedField},
		{"acp protocol", cliHost(func(h *store.NativeHostSettings) { h.Protocol = "acp" }), "default", nil, CauseCLIHostUnsupportedField},
		{"no directories", cliHost(func(h *store.NativeHostSettings) { h.Directories = nil }), "default", nil, CauseHostDirectoriesMissing},
		{"relative directory", cliHost(func(h *store.NativeHostSettings) { h.Directories = []string{"work"} }), "default", nil, CauseCLIHostUnsupportedField},
		{"model on unverified provider", cliHost(func(h *store.NativeHostSettings) { h.Provider = "copilot" }), "default", nil, CauseCLIHostUnsupportedField},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := admitCLIHost(tc.host, tc.profile, tc.req)
			if !errors.Is(err, ErrUnsupportedDefinition) {
				t.Fatalf("err = %v, want ErrUnsupportedDefinition", err)
			}
			if got := CauseCode(err); got != tc.cause {
				t.Fatalf("cause = %q, want %q (err: %v)", got, tc.cause, err)
			}
		})
	}
}

func TestAdmitCLIHostRefusesAConflictingRequestSelection(t *testing.T) {
	_, err := admitCLIHost(cliHost(nil), "default", &ModelSelection{Provider: "claude", Model: "opus"})
	if !errors.Is(err, ErrUnsupportedModel) {
		t.Fatalf("err = %v, want ErrUnsupportedModel", err)
	}
}

// A view admitted under one host revision must not run another: editing the host
// after create fails the turn, naming the cause.
func TestCheckHostRevisionFailsAfterTheHostChanges(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	pin, err := st.InstallAgentDefinition(ctx, []byte("---\nschema_version: \"2\"\ndefinition_id: def:rev-check\nrevision: \"1\"\nname: rev-check\ndescription: Revision check.\nbehavior:\n  purpose: Answer.\nrequirements: {}\nharness_profile:\n  context: {}\n  permissions:\n    profile: default\ncontinuity:\n  mode: ephemeral\n---\nBody.\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
	host, err := st.CreateAgentHostSettings(ctx, store.AgentHostSettings{Slug: "rev-check", Title: "Rev check", DefinitionRef: pin, Enabled: true, Source: "test", Settings: cliHost(nil)})
	if err != nil {
		t.Fatal(err)
	}
	svc := &chatServiceImpl{store: st}
	agent := &store.AgentProfile{HostSettingsID: host.ID, Revision: host.Revision}
	if err = svc.checkHostRevision(ctx, agent); err != nil {
		t.Fatalf("unchanged host: %v", err)
	}
	changed := host
	changed.Settings.Model = "opus"
	if _, err = st.UpdateAgentHostSettings(ctx, changed, host.Revision); err != nil {
		t.Fatalf("update host: %v", err)
	}
	err = svc.checkHostRevision(ctx, agent)
	if CauseCode(err) != CauseHostRevisionChanged {
		t.Fatalf("after edit: err = %v, want cause %q", err, CauseHostRevisionChanged)
	}
}

func TestHostWorkRootIsTheFirstHostDirectory(t *testing.T) {
	agent := &store.AgentProfile{HostSettingsID: "h", NativeHost: &store.NativeHostSettings{Directories: []string{"/srv/work", "/srv/other"}}}
	if got, err := hostWorkRoot(agent); err != nil || got != "/srv/work" {
		t.Fatalf("hostWorkRoot = %q, %v", got, err)
	}
	if got, _ := hostWorkRoot(&store.AgentProfile{}); got != "" {
		t.Errorf("non-host profile got work root %q", got)
	}
	if _, err := hostWorkRoot(&store.AgentProfile{HostSettingsID: "h", NativeHost: &store.NativeHostSettings{Directories: []string{"rel"}}}); err == nil {
		t.Error("relative host directory accepted")
	}
}

func TestAdmitCLIHostAcceptsMCPServersOnClaude(t *testing.T) {
	if _, err := admitCLIHost(cliHost(func(h *store.NativeHostSettings) { h.MCPServers = []string{"tether"} }), "default", nil); err != nil {
		t.Fatalf("claude with mcp_servers: %v", err)
	}
}

func TestCheckHostMCPServersRefusesWhatLaunchCannotProject(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	v := &CognitiveViews{Store: st}
	add := func(cfg store.MCPServerConfig) {
		t.Helper()
		if err := st.CreateMCPServer(ctx, &cfg); err != nil {
			t.Fatal(err)
		}
	}
	add(store.MCPServerConfig{Name: "tether", TransportType: store.TransportStdio, Command: "tether", Args: `["mcp"]`, Enabled: true})
	add(store.MCPServerConfig{Name: "off", TransportType: store.TransportStdio, Command: "x", Enabled: false})
	add(store.MCPServerConfig{Name: "remote", TransportType: store.TransportSSE, URL: "http://x", Enabled: true})
	if err := v.checkHostMCPServers(ctx, []string{"tether"}); err != nil {
		t.Fatalf("stdio server: %v", err)
	}
	for _, name := range []string{"missing", "off", "remote", "nanite"} {
		err := v.checkHostMCPServers(ctx, []string{name})
		if !errors.Is(err, ErrUnsupportedDefinition) || CauseCode(err) != CauseCLIHostUnsupportedField {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

// Session creation, not only launch, refuses a host whose mcp_servers cannot
// be projected: the cause surfaces as a 422, not as a failed boot.
func TestCreateRefusesACLIHostWhoseMCPServerCannotBeProjected(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	pin, err := st.InstallAgentDefinition(ctx, []byte("---\nschema_version: \"2\"\ndefinition_id: def:host-mcp\nrevision: \"1\"\nname: host-mcp\ndescription: Host MCP.\nbehavior:\n  purpose: Answer.\nrequirements: {}\nharness_profile:\n  context: {}\n  permissions:\n    profile: default\ncontinuity:\n  mode: ephemeral\n---\nBody.\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
	views := &CognitiveViews{Store: st, Resolver: &StoredDefinitionResolver{Store: st}}
	create := func(slug string, servers ...string) error {
		host, hostErr := st.CreateAgentHostSettings(ctx, store.AgentHostSettings{Slug: slug, Title: slug, Source: "test", DefinitionRef: pin, Enabled: true,
			Settings: cliHost(func(h *store.NativeHostSettings) { h.Model = ""; h.MCPServers = servers })})
		if hostErr != nil {
			t.Fatal(hostErr)
		}
		_, createErr := views.Create(ctx, CreateDefinedView{DefinitionRef: DefinitionRefFromMesh(pin), HostSettings: &HostSettingsRef{ID: host.ID, Revision: host.Revision}})
		return createErr
	}
	err = create("missing-server", "no-such-server")
	if !errors.Is(err, ErrUnsupportedDefinition) || CauseCode(err) != CauseCLIHostUnsupportedField {
		t.Fatalf("missing server: %v", err)
	}
	if err = st.CreateMCPServer(ctx, &store.MCPServerConfig{Name: "tether", TransportType: store.TransportStdio, Command: "tether", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err = create("with-server", "tether"); err != nil {
		t.Fatalf("registered server: %v", err)
	}
}

// An api host's directories bound its tools, so a relative one is refused at
// session creation with its own cause; an absolute one is accepted.
func TestCreateRefusesARelativeAPIHostDirectory(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	pin, err := st.InstallAgentDefinition(ctx, []byte("---\nschema_version: \"2\"\ndefinition_id: def:api-dirs\nrevision: \"1\"\nname: api-dirs\ndescription: API dirs.\nbehavior:\n  purpose: Answer.\nrequirements: {}\nharness_profile:\n  context: {}\n  permissions:\n    profile: default\ncontinuity:\n  mode: ephemeral\n---\nBody.\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
	views := &CognitiveViews{Store: st, Resolver: &StoredDefinitionResolver{Store: st}, Models: ModelAuthorizerFunc(func(context.Context, DefinitionRef, *ModelSelection) (ModelSelection, error) {
		return ModelSelection{"mock-provider", "mock-model"}, nil
	})}
	create := func(slug, dir string) error {
		host, hostErr := st.CreateAgentHostSettings(ctx, store.AgentHostSettings{Slug: slug, Title: slug, Source: "test", DefinitionRef: pin, Enabled: true,
			Settings: store.NativeHostSettings{Version: "1", Runtime: "api", Provider: "mock-provider", Model: "mock-model", Directories: []string{dir}}})
		if hostErr != nil {
			t.Fatal(hostErr)
		}
		_, createErr := views.Create(ctx, CreateDefinedView{DefinitionRef: DefinitionRefFromMesh(pin), HostSettings: &HostSettingsRef{ID: host.ID, Revision: host.Revision}})
		return createErr
	}
	if err = create("rel", "work"); !errors.Is(err, ErrUnsupportedDefinition) || CauseCode(err) != CauseHostDirectoryInvalid {
		t.Fatalf("relative directory: %v", err)
	}
	if err = create("abs", t.TempDir()); err != nil {
		t.Fatalf("absolute directory: %v", err)
	}
}
