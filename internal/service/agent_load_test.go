package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/agentdir"
	"github.com/hollis-labs/nanite/internal/store"
)

func loadFixture(t *testing.T, body, hostExtra string) (*AgentLoadService, string) {
	t.Helper()
	root, work := t.TempDir(), canonicalTempDir(t)
	svc := &AgentLoadService{Store: newTestStore(t)}
	writeLoadDir(t, root, work, body, hostExtra)
	return svc, root
}

func writeLoadDir(t *testing.T, root, work, body, hostExtra string) {
	t.Helper()
	writeLoadDirRev(t, root, work, "1", body, hostExtra)
}

func writeLoadDirRev(t *testing.T, root, work, rev, body, hostExtra string) {
	t.Helper()
	def := "---\nschema_version: \"2\"\ndefinition_id: def:load-agent\nrevision: \"" + rev + "\"\nname: load-agent\ndescription: Load agent.\nbehavior:\n  purpose: Answer.\nrequirements: {}\nharness_profile:\n  context: {}\n  permissions:\n    profile: default\ncontinuity:\n  mode: ephemeral\n---\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(root, "agent.md"), []byte(def), 0o600); err != nil {
		t.Fatal(err)
	}
	host := "runtime: cli\nprovider: claude\nmodel: haiku\ndirectories: [" + work + "]\n" + hostExtra
	if err := os.WriteFile(filepath.Join(root, "host.yaml"), []byte(host), 0o600); err != nil {
		t.Fatal(err)
	}
}

func loadDir(t *testing.T, svc *AgentLoadService, root string, opts AgentLoadOptions) (AgentLoadResponse, error) {
	t.Helper()
	d, err := agentdir.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	return svc.Load(context.Background(), d, opts, "test")
}

func TestAgentLoadIsIdempotentAndPublishesNoAuthority(t *testing.T) {
	svc, root := loadFixture(t, "Version one.", "")
	first, err := loadDir(t, svc, root, AgentLoadOptions{})
	if err != nil || !first.Changed || first.AuthorityID != "" || len(first.Tools) != 0 {
		t.Fatalf("first = %+v, %v", first, err)
	}
	again, err := loadDir(t, svc, root, AgentLoadOptions{})
	if err != nil || again.Changed || again.HostSettingsID != first.HostSettingsID || again.Diff[0] == "new agent" {
		t.Fatalf("again = %+v, %v", again, err)
	}
	hosts, _ := svc.Store.ListAgentHostSettings(context.Background())
	if len(hosts) != 1 {
		t.Fatalf("%d host rows after a repeat load, want 1", len(hosts))
	}
}

func TestAgentLoadEditAddsARowAndLeavesTheOldOneUntouched(t *testing.T) {
	svc, root := loadFixture(t, "Version one.", "")
	first, err := loadDir(t, svc, root, AgentLoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	work := canonicalTempDir(t)
	writeLoadDirRev(t, root, work, "2", "Version two.", "")
	second, err := loadDir(t, svc, root, AgentLoadOptions{})
	if err != nil || !second.Changed || second.HostSettingsID == first.HostSettingsID || second.Previous != first.Slug {
		t.Fatalf("second = %+v, %v", second, err)
	}
	if !strings.Contains(strings.Join(second.Diff, "\n"), "directories") {
		t.Fatalf("diff = %v", second.Diff)
	}
	old, err := svc.Store.GetAgentHostSettings(context.Background(), first.HostSettingsID)
	if err != nil || old.Revision != first.HostSettings.Revision || !old.Enabled {
		t.Fatalf("the first row changed: %+v %v", old, err)
	}
}

func TestAgentLoadPinnedNeedsForceForAnotherRevision(t *testing.T) {
	svc, root := loadFixture(t, "Version one.", "mode: pinned\n")
	if _, err := loadDir(t, svc, root, AgentLoadOptions{}); err != nil {
		t.Fatal(err)
	}
	writeLoadDirRev(t, root, canonicalTempDir(t), "2", "Version two.", "mode: pinned\n")
	if _, err := loadDir(t, svc, root, AgentLoadOptions{}); !errors.Is(err, store.ErrAgentLoadPinned) {
		t.Fatalf("err = %v, want ErrAgentLoadPinned", err)
	}
	if res, err := loadDir(t, svc, root, AgentLoadOptions{Force: true}); err != nil || !res.Changed {
		t.Fatalf("forced load = %+v, %v", res, err)
	}
}

func TestAgentLoadDryRunWritesNothing(t *testing.T) {
	svc, root := loadFixture(t, "Version one.", "")
	res, err := loadDir(t, svc, root, AgentLoadOptions{DryRun: true})
	if err != nil || !res.DryRun || !res.Changed {
		t.Fatalf("%+v %v", res, err)
	}
	if hosts, _ := svc.Store.ListAgentHostSettings(context.Background()); len(hosts) != 0 {
		t.Fatalf("a dry run wrote %d host rows", len(hosts))
	}
}

func TestAgentLoadGrantSameAsCopiesOnlyTheNamedTools(t *testing.T) {
	ctx := context.Background()
	svc, root := loadFixture(t, "Version one.", "")
	first, err := loadDir(t, svc, root, AgentLoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	tools, err := svc.Store.ResolveSetupTools(ctx, []string{store.AllAvailableTools})
	if err != nil || len(tools) == 0 {
		t.Skipf("no available tools to grant: %v", err)
	}
	local := svc.Store.LocalAuthority()
	auth, err := local.CreateAuthority(ctx, "test", first.HostSettingsID, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if err = local.GrantTool(ctx, "test", auth.ID, tools[0], "fixture"); err != nil {
		t.Fatal(err)
	}
	writeLoadDirRev(t, root, canonicalTempDir(t), "2", "Version two.", "")
	second, err := loadDir(t, svc, root, AgentLoadOptions{GrantFrom: first.Slug, Reason: "same tools"})
	if err != nil || len(second.Tools) != 1 || second.Tools[0] != tools[0] || second.AuthorityID == "" {
		t.Fatalf("second = %+v, %v", second, err)
	}
}

func TestAgentLoadEditWithoutBumpingRevisionSaysHow(t *testing.T) {
	svc, root := loadFixture(t, "Version one.", "")
	if _, err := loadDir(t, svc, root, AgentLoadOptions{}); err != nil {
		t.Fatal(err)
	}
	writeLoadDir(t, root, canonicalTempDir(t), "Version two, same revision.", "")
	if _, err := loadDir(t, svc, root, AgentLoadOptions{}); err == nil || !strings.Contains(err.Error(), "bump `revision`") {
		t.Fatalf("err = %v", err)
	}
}
