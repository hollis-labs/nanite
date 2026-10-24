package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/mesh/core/agentdef"
	"github.com/hollis-labs/nanite/internal/agentdir"
	"github.com/hollis-labs/nanite/internal/agentpolicy"
)

// fieldsDir writes an agent directory that declares every field the
// definition-fields table covers, with real digests.
func fieldsDir(t *testing.T, extraAgentYAML string) string {
	t.Helper()
	root, work := t.TempDir(), canonicalTempDir(t)
	files := map[string]string{
		"context/notes.md":    "Project notes.\n",
		"context/policy.json": "{}\n",
		"steer/tone.md":       "Be terse.\n",
		"res/data.txt":        "data\n",
		"memory.md":           "Remember nothing.\n",
		"recovery.md":         "Do not recover.\n",
	}
	for name, body := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	dg := func(name string) string { return agentdef.ArtifactDigest([]byte(files[name])) }
	if err := os.MkdirAll(filepath.Join(root, "skills", "review", "scripts"), 0o750); err != nil {
		t.Fatal(err)
	}
	skillFiles := map[string][]byte{"SKILL.md": []byte("# review\n"), "scripts/run.sh": []byte("echo ok\n")}
	for rel, body := range skillFiles {
		if err := os.WriteFile(filepath.Join(root, "skills", "review", filepath.FromSlash(rel)), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	blob, err := agentpolicy.EncodeSkillTree(skillFiles)
	if err != nil {
		t.Fatal(err)
	}
	def := `---
schema_version: "2"
definition_id: def:fields-agent
revision: "1"
name: fields-agent
description: Declares every field.
behavior:
  purpose: Answer.
capabilities:
  - id: review.code
    description: Reviews code.
requirements:
  uses: [cap-optional]
  tools: [dev_read, dev_write]
  skills:
    - name: review
      content: {uri: "file:skills/review", digest: "` + agentdef.ArtifactDigest(blob) + `"}
  resources:
    - {uri: "file:res/data.txt", digest: "` + dg("res/data.txt") + `"}
harness_profile:
  steering:
    - {uri: "file:steer/tone.md", digest: "` + dg("steer/tone.md") + `"}
  context:
    sources:
      - {uri: "file:context/notes.md", digest: "` + dg("context/notes.md") + `"}
    policy: {uri: "file:context/policy.json", digest: "` + dg("context/policy.json") + `"}
  permissions:
    profile: default
continuity:
  mode: ephemeral
  memory_policy: {uri: "file:memory.md", digest: "` + dg("memory.md") + `"}
  recovery_strategy: {uri: "file:recovery.md", digest: "` + dg("recovery.md") + `"}
` + extraAgentYAML + `---
Body text.
`
	if err := os.WriteFile(filepath.Join(root, "agent.md"), []byte(def), 0o600); err != nil {
		t.Fatal(err)
	}
	host := "runtime: cli\nprovider: claude\nmodel: haiku\ndirectories: [" + work + "]\n"
	if err := os.WriteFile(filepath.Join(root, "host.yaml"), []byte(host), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestAgentLoadAcceptsEveryDeclaredFieldAndReportsEach(t *testing.T) {
	ctx := context.Background()
	svc := &AgentLoadService{Store: newTestStore(t)}
	root := fieldsDir(t, "")
	resp, err := loadDir(t, svc, root, AgentLoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]agentpolicy.Disposition{}
	for _, f := range resp.Fields {
		got[f.Field] = f.Disposition
	}
	want := map[string]agentpolicy.Disposition{
		"capabilities":                    agentpolicy.Recorded,
		"requirements.uses":               agentpolicy.Unmet,
		"requirements.tools":              agentpolicy.Recorded,
		"requirements.skills":             agentpolicy.Unmet,
		"requirements.resources":          agentpolicy.Applied,
		"harness_profile.steering":        agentpolicy.Applied,
		"harness_profile.context.sources": agentpolicy.Applied,
		"harness_profile.context.policy":  agentpolicy.Recorded,
		"continuity.memory_policy":        agentpolicy.Recorded,
		"continuity.recovery_strategy":    agentpolicy.Recorded,
	}
	for field, disp := range want {
		if got[field] != disp {
			t.Errorf("%s = %q, want %q", field, got[field], disp)
		}
	}
	for _, tool := range resp.Requests.Tools {
		if tool.State != "pending" {
			t.Errorf("tool request %s is %s on a revision with no authority", tool.Name, tool.State)
		}
	}
	if len(resp.Requests.Tools) != 2 || len(resp.Requests.Skills) != 1 || resp.Requests.Skills[0].State != "pending" {
		t.Errorf("requests = %+v", resp.Requests)
	}
	view, err := svc.Store.GetAgentView(ctx, resp.Slug)
	if err != nil || len(view.Requests.Tools) != 2 {
		t.Fatalf("agent view requests = %+v, %v", view.Requests, err)
	}
	if !strings.Contains(strings.Join(resp.Notes, "\n"), "requirements.skills") {
		t.Errorf("notes do not say the skill is not delivered: %v", resp.Notes)
	}

	// The same definition launches: steering follows the body, the context file
	// is listed, and the pinned files are carried for planting.
	dir, err := agentdir.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := agentdef.Digest(dir.Parsed)
	byURI := map[string][]byte{}
	for _, r := range dir.Resources {
		byURI[r.URI] = r.Content
	}
	cfg, err := MapChatDefinition(VerifiedDefinition{
		Ref:        DefinitionRef{DefinitionID: dir.Parsed.DefinitionID, Revision: dir.Parsed.Revision, SemanticDigest: digest},
		Definition: dir.Parsed,
		ReadResource: func(_ context.Context, ref agentdef.Ref) ([]byte, error) {
			return byURI[ref.URI], nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, steer, listed := strings.Index(cfg.Instructions, "Body text."), strings.Index(cfg.Instructions, "Be terse."), strings.Index(cfg.Instructions, "- agent/context/notes.md")
	if body < 0 || steer < body || listed < steer {
		t.Errorf("instruction order wrong (body %d, steering %d, listed file %d):\n%s", body, steer, listed, cfg.Instructions)
	}
	if string(cfg.Files["agent/context/notes.md"]) != "Project notes.\n" || string(cfg.Files["agent/res/data.txt"]) != "data\n" || len(cfg.Files) != 2 {
		t.Errorf("files = %v", cfg.Files)
	}
}

func TestAgentLoadRefusesHooksAndUnmetRequiresNamingThem(t *testing.T) {
	svc := &AgentLoadService{Store: newTestStore(t)}
	for name, tc := range map[string]struct{ patch, want string }{
		"hooks":    {"hooks", `"guard"`},
		"requires": {"requires", `"cap-needed"`},
	} {
		t.Run(name, func(t *testing.T) {
			root := fieldsDir(t, "")
			raw, _ := os.ReadFile(filepath.Join(root, "agent.md"))
			s := string(raw)
			if tc.patch == "hooks" {
				s = strings.Replace(s, "  purpose: Answer.\n", "  purpose: Answer.\n  hooks: [guard]\n", 1)
			} else {
				s = strings.Replace(s, "requirements:\n", "requirements:\n  requires: [cap-needed]\n", 1)
			}
			if err := os.WriteFile(filepath.Join(root, "agent.md"), []byte(s), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := loadDir(t, svc, root, AgentLoadOptions{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to name %s", err, tc.want)
			}
		})
	}
}

func TestAgentLoadRefusesASkillWhoseDigestIsStale(t *testing.T) {
	root := fieldsDir(t, "")
	if err := os.WriteFile(filepath.Join(root, "skills", "review", "SKILL.md"), []byte("# changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := agentdir.Load(root); err == nil || !strings.Contains(err.Error(), "packs to sha256:") {
		t.Fatalf("err = %v, want a digest mismatch that names the packed digest", err)
	}
}

func TestAgentViewShowsAToolRequestAsGrantedOnlyOnceGranted(t *testing.T) {
	ctx := context.Background()
	svc := &AgentLoadService{Store: newTestStore(t)}
	resp, err := loadDir(t, svc, fieldsDir(t, ""), AgentLoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Store.DB.ExecContext(ctx, `INSERT OR IGNORE INTO known_tools(id,name,source,status,created_at,updated_at) VALUES('fixture-dev-read','dev_read','builtin','available','t','t')`); err != nil {
		t.Fatal(err)
	}
	local := svc.Store.LocalAuthority()
	auth, err := local.CreateAuthority(ctx, "test", resp.HostSettingsID, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if err = local.GrantTool(ctx, "test", auth.ID, "dev_read", "fixture"); err != nil {
		t.Fatal(err)
	}
	view, err := svc.Store.GetAgentView(ctx, resp.Slug)
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]string{}
	for _, r := range view.Requests.Tools {
		states[r.Name] = r.State
	}
	if states["dev_read"] != "granted" || states["dev_write"] != "pending" {
		t.Fatalf("states = %v", states)
	}
}
