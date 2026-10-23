package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/internal/chat"
	runtimeagent "github.com/hollis-labs/nanite/internal/runtime/agent"
	"github.com/hollis-labs/nanite/internal/store"
)

// definedAPIView stores an API-backed defined view for the fixture's session.
func definedAPIView(t *testing.T, f *characterizationFixture) {
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
	settings := store.NativeHostSettings{Version: "1", Runtime: "api", Provider: "characterization", Model: "characterization-model"}
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
