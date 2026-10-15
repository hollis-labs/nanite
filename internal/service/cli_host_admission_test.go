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
		{"mcp servers", cliHost(func(h *store.NativeHostSettings) { h.MCPServers = []string{"files"} }), "default", nil, CauseCLIHostUnsupportedField},
		{"acp protocol", cliHost(func(h *store.NativeHostSettings) { h.Protocol = "acp" }), "default", nil, CauseCLIHostUnsupportedField},
		{"no directories", cliHost(func(h *store.NativeHostSettings) { h.Directories = nil }), "default", nil, CauseHostDirectoriesMissing},
		{"relative directory", cliHost(func(h *store.NativeHostSettings) { h.Directories = []string{"work"} }), "default", nil, CauseCLIHostUnsupportedField},
		{"model on unverified provider", cliHost(func(h *store.NativeHostSettings) { h.Provider = "codex" }), "default", nil, CauseCLIHostUnsupportedField},
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
	if err := svc.checkHostRevision(ctx, agent); err != nil {
		t.Fatalf("unchanged host: %v", err)
	}
	changed := host
	changed.Settings.Model = "opus"
	if _, err := st.UpdateAgentHostSettings(ctx, changed, host.Revision); err != nil {
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
