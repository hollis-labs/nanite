package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/libs/mesh/core/agentdef"
	"github.com/hollis-labs/nanite/internal/agentpolicy"
	runtimeagent "github.com/hollis-labs/nanite/internal/runtime/agent"
	"github.com/hollis-labs/nanite/internal/store"
)

func skillDeliveryDef(t *testing.T, revision string, files map[string][]byte) ([]byte, []store.DefinitionResource, string) {
	t.Helper()
	blob, err := agentpolicy.EncodeSkillTree(files)
	if err != nil {
		t.Fatal(err)
	}
	digest := agentdef.ArtifactDigest(blob)
	def := fmt.Sprintf("---\nschema_version: \"2\"\ndefinition_id: def:skill-delivery\nrevision: \"%s\"\nname: skill-delivery\ndescription: Requests a skill.\nbehavior:\n  purpose: Complete the requested work.\nrequirements:\n  skills:\n    - name: review\n      content: {uri: \"file:skills/review\", digest: \"%s\"}\nharness_profile:\n  context: {}\n  permissions:\n    profile: default\ncontinuity:\n  mode: ephemeral\n---\nSkill agent.\n", revision, digest)
	return []byte(def), []store.DefinitionResource{{URI: "file:skills/review", Content: blob}}, digest
}

func skillFieldDisposition(t *testing.T, cfg ChatDefinitionConfig) agentpolicy.Disposition {
	t.Helper()
	for _, f := range cfg.FieldReport {
		if f.Field == "requirements.skills" {
			return f.Disposition
		}
	}
	t.Fatal("no requirements.skills in the field report")
	return ""
}

// A session created from host settings gets the skills its authority has been
// granted, at the pinned digest, planted under agent/skills/<name>/ and reported
// applied; nothing else is delivered (CW-20261024-0089).
func TestSessionCreateDeliversOnlyGrantedSkills(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	files := map[string][]byte{"SKILL.md": []byte("# review\n"), "scripts/run.sh": []byte("echo ok\n")}
	def, res, digest := skillDeliveryDef(t, "1", files)
	pin, err := st.InstallAgentDefinition(ctx, def, res)
	if err != nil {
		t.Fatal(err)
	}
	newHost := func(slug string) store.AgentHostSettings {
		h, herr := st.CreateAgentHostSettings(ctx, store.AgentHostSettings{Slug: slug, Title: slug, Source: "test", DefinitionRef: pin, Enabled: true,
			Settings: store.NativeHostSettings{Version: "1", Runtime: "api", Provider: "mock-provider", Model: "mock-model", Directories: []string{canonicalTempDir(t)}}})
		if herr != nil {
			t.Fatal(herr)
		}
		return h
	}
	host := newHost("skill-delivery")
	views := &CognitiveViews{Store: st, Resolver: &StoredDefinitionResolver{Store: st}, Models: ModelAuthorizerFunc(func(context.Context, DefinitionRef, *ModelSelection) (ModelSelection, error) {
		return ModelSelection{"mock-provider", "mock-model"}, nil
	})}
	create := func(h store.AgentHostSettings) ChatDefinitionConfig {
		t.Helper()
		view, cerr := views.Create(ctx, CreateDefinedView{DefinitionRef: DefinitionRefFromMesh(pin), HostSettings: &HostSettingsRef{ID: h.ID, Revision: h.Revision}})
		if cerr != nil {
			t.Fatal(cerr)
		}
		_, cfg, gerr := views.Get(ctx, view.ID)
		if gerr != nil {
			t.Fatal(gerr)
		}
		return cfg
	}

	// Pending: no grant, nothing planted, reported unmet.
	cfg := create(host)
	if len(cfg.Files) != 0 || skillFieldDisposition(t, cfg) != agentpolicy.Unmet {
		t.Fatalf("ungranted: files=%d disposition=%s", len(cfg.Files), skillFieldDisposition(t, cfg))
	}

	// Granted at the pinned digest: planted and applied.
	auth, err := st.LocalAuthority().CreateAuthority(ctx, "cred-admin", host.ID, "review")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.LocalAuthority().GrantSkill(ctx, "cred-admin", auth.ID, "review", digest, "approved"); err != nil {
		t.Fatal(err)
	}
	cfg = create(host)
	if string(cfg.Files["agent/skills/review/SKILL.md"]) != "# review\n" || string(cfg.Files["agent/skills/review/scripts/run.sh"]) != "echo ok\n" || skillFieldDisposition(t, cfg) != agentpolicy.Applied {
		t.Fatalf("granted: files=%v disposition=%s", cfg.Files, skillFieldDisposition(t, cfg))
	}
	// An API session plants those files into its workspace.
	ws, err := runtimeagent.EnsureSessionWorkspace(t.TempDir(), "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if err = runtimeagent.PlantDefinitionFiles(ws, cfg.Files); err != nil {
		t.Fatal(err)
	}
	if b, rerr := os.ReadFile(filepath.Join(ws.Root, "agent", "skills", "review", "scripts", "run.sh")); rerr != nil || string(b) != "echo ok\n" {
		t.Fatalf("workspace file: %q %v", b, rerr)
	}

	// Revoked: the next session has none.
	if err = st.LocalAuthority().RevokeSkill(ctx, "cred-admin", auth.ID, "review", "withdrawn"); err != nil {
		t.Fatal(err)
	}
	if cfg = create(host); len(cfg.Files) != 0 || skillFieldDisposition(t, cfg) != agentpolicy.Unmet {
		t.Fatalf("revoked: files=%d", len(cfg.Files))
	}

	// A new host revision (reviewed state changed) has no grant until re-granted.
	if _, err = st.LocalAuthority().GrantSkill(ctx, "cred-admin", auth.ID, "review", digest, "again"); err != nil {
		t.Fatal(err)
	}
	moved := host
	moved.Title = "moved"
	moved, err = st.UpdateAgentHostSettings(ctx, moved, host.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if cfg = create(moved); len(cfg.Files) != 0 {
		t.Fatalf("a new host revision inherited the grant: %d files", len(cfg.Files))
	}
}
