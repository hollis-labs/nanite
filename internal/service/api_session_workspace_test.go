package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/permission"
	runtimeagent "github.com/hollis-labs/nanite/internal/runtime/agent"
	"github.com/hollis-labs/nanite/internal/store"
)

// definedAPIView stores an API-backed defined view for the fixture's session.
func definedAPIView(t *testing.T, f *characterizationFixture, dirs ...string) {
	t.Helper()
	ctx := t.Context()
	if err := installEmbeddedChatDefinition(ctx, f.st); err != nil {
		t.Fatal(err)
	}
	verified, err := EmbeddedDefinition()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := MapChatDefinition(verified)
	if err != nil {
		t.Fatal(err)
	}
	settings := store.NativeHostSettings{Version: "1", Runtime: "api", Provider: "characterization", Model: "characterization-model", Directories: dirs}
	host, err := f.st.CreateAgentHostSettings(ctx, store.AgentHostSettings{Slug: "api-ws", Title: "api-ws", Source: "test", DefinitionRef: verified.Ref.MeshRef(), Enabled: true, Settings: settings})
	if err != nil {
		t.Fatal(err)
	}
	cfg.Model = ModelSelection{"characterization", "characterization-model"}
	cfg.HostSettings = &settings
	cfg.HostSettingsRef = &HostSettingsRef{ID: host.ID, Revision: host.Revision}
	pinJSON, _ := json.Marshal(verified.Ref)
	cfgJSON, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.st.DB.ExecContext(ctx, `INSERT INTO cognitive_views(session_view_id,definition_ref_json,chat_config_json) VALUES(?,?,?)`, f.session, string(pinJSON), string(cfgJSON)); err != nil {
		t.Fatal(err)
	}
}

// An API-backed defined session gets the same durable workspace layout a CLI
// session gets, and the turn carries it.
func TestPrepareTurn_APISessionGetsTheSessionWorkspace(t *testing.T) {
	root := canonicalTempDir(t)
	f := newHandleMessageFixture(t, nil)
	f.svc.agentDeps = &runtimeagent.Dependencies{WorkspacesRoot: root}
	definedAPIView(t, f)

	ch := make(chan chat.StreamEvent, 16)
	prepared := f.svc.prepareTurn(cognitiveCtx(), f.session, "hi", ch)
	if prepared.setup == nil {
		t.Fatalf("prepareTurn refused an API view: %v", drainEvents(ch))
	}
	want := filepath.Join(root, f.session)
	if prepared.setup.workspace == nil || prepared.setup.workspace.Root != want {
		t.Fatalf("workspace = %+v, want root %q", prepared.setup.workspace, want)
	}
	for _, sub := range []string{"prompts", "state", "logs"} {
		if fi, err := os.Stat(filepath.Join(want, sub)); err != nil || !fi.IsDir() {
			t.Errorf("%s missing: %v", sub, err)
		}
	}
	// A second turn finds the same workspace rather than failing.
	if again := f.svc.prepareTurn(cognitiveCtx(), f.session, "again", make(chan chat.StreamEvent, 16)); again.setup == nil || again.setup.workspace.Root != want {
		t.Fatalf("second turn: %+v", again.setup)
	}
}

// No workspaces root configured (embedding callers, focused tests) is not an
// error, and a CLI view's workspace stays Boot's to create.
func TestPrepareTurn_NoWorkspaceWithoutARootOrForCLI(t *testing.T) {
	f := newHandleMessageFixture(t, nil)
	f.svc.agentDeps = &runtimeagent.Dependencies{}
	definedAPIView(t, f)
	prepared := f.svc.prepareTurn(cognitiveCtx(), f.session, "hi", make(chan chat.StreamEvent, 16))
	if prepared.setup == nil || prepared.setup.workspace != nil {
		t.Fatalf("no root: %+v", prepared.setup)
	}

	root := canonicalTempDir(t)
	host := canonicalTempDir(t)
	cli := newHandleMessageFixture(t, nil)
	cli.svc.agentDeps = &runtimeagent.Dependencies{WorkspacesRoot: root}
	definedCLIView(t, cli, host, "")
	prepared = cli.svc.prepareTurn(cognitiveCtx(), cli.session, "hi", make(chan chat.StreamEvent, 16))
	if prepared.setup == nil || prepared.setup.workspace != nil {
		t.Fatalf("cli view: %+v", prepared.setup)
	}
	if _, err := os.Stat(filepath.Join(root, cli.session)); !os.IsNotExist(err) {
		t.Fatalf("prepareTurn created a CLI session's workspace: %v", err)
	}
}

// The session's tools are bounded by the host's directories and the
// workspace, and the first directory is the default working directory.
func TestPrepareTurn_APISessionToolRoots(t *testing.T) {
	root := canonicalTempDir(t)
	hostA, hostB := canonicalTempDir(t), canonicalTempDir(t)
	f := newHandleMessageFixture(t, nil)
	f.svc.agentDeps = &runtimeagent.Dependencies{WorkspacesRoot: root}
	f.svc.pathGrants = permission.NewPathGrants()
	definedAPIView(t, f, hostA, hostB)

	prepared := f.svc.prepareTurn(cognitiveCtx(), f.session, "hi", make(chan chat.StreamEvent, 16))
	if prepared.setup == nil {
		t.Fatal("prepareTurn refused an API view with host directories")
	}
	if got := f.svc.pathGrants.BestSessionDir(f.session); got != hostA {
		t.Fatalf("work root = %q, want the first host directory %q", got, hostA)
	}
	for _, dir := range []string{hostA, hostB, prepared.setup.workspace.Root} {
		if !f.svc.pathGrants.IsPathAllowed(f.session, filepath.Join(dir, "f")) {
			t.Errorf("%s not granted to the session", dir)
		}
	}
	if f.svc.pathGrants.IsPathAllowed(f.session, filepath.Join(canonicalTempDir(t), "f")) {
		t.Error("a directory the host never listed was granted")
	}
}

// With no host directories the workspace is the work root, as for a CLI
// session.
func TestPrepareTurn_APISessionWithoutDirectoriesWorksInItsWorkspace(t *testing.T) {
	root := canonicalTempDir(t)
	f := newHandleMessageFixture(t, nil)
	f.svc.agentDeps = &runtimeagent.Dependencies{WorkspacesRoot: root}
	f.svc.pathGrants = permission.NewPathGrants()
	definedAPIView(t, f)
	prepared := f.svc.prepareTurn(cognitiveCtx(), f.session, "hi", make(chan chat.StreamEvent, 16))
	if prepared.setup == nil {
		t.Fatal("prepareTurn refused")
	}
	if got := f.svc.pathGrants.BestSessionDir(f.session); got != prepared.setup.workspace.Root {
		t.Fatalf("work root = %q, want the workspace %q", got, prepared.setup.workspace.Root)
	}
}
