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

// A revision label is part of the state: a revision-only change yields a new
// host row pinned to the new revision, and the old row keeps its pin.
func TestAgentLoadRevisionOnlyChangeGetsItsOwnHost(t *testing.T) {
	svc, root := loadFixture(t, "Same body.", "")
	first, err := loadDir(t, svc, root, AgentLoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	d, err := agentdir.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	work := d.Host.Directories[0]
	writeLoadDirRev(t, root, work, "2", "Same body.", "")
	second, err := loadDir(t, svc, root, AgentLoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !second.Changed || second.HostSettingsID == first.HostSettingsID || second.Slug == first.Slug {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
	if second.DefinitionRef.Revision != "2" || second.HostSettings.Revision == "" || first.DefinitionRef.Revision != "1" {
		t.Fatalf("pins: first=%+v second=%+v", first.DefinitionRef, second.DefinitionRef)
	}
	old, err := svc.Store.GetAgentHostSettings(context.Background(), first.HostSettingsID)
	if err != nil || old.DefinitionRef.Revision != "1" {
		t.Fatalf("old row = %+v, %v", old, err)
	}
}

// Two definitions that share an explicit agent slug stay separate rows.
func TestAgentLoadDistinctDefinitionIDsSharingASlug(t *testing.T) {
	svc, root := loadFixture(t, "Same body.", "slug: shared\n")
	first, err := loadDir(t, svc, root, AgentLoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "agent.md")
	b, err := os.ReadFile(path) //nolint:gosec // test fixture path
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte(strings.Replace(string(b), "def:load-agent", "def:other-agent", 1)), 0o600); err != nil { //nolint:gosec // test fixture path
		t.Fatal(err)
	}
	second, err := loadDir(t, svc, root, AgentLoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if second.HostSettingsID == first.HostSettingsID || second.DefinitionRef.DefinitionID == first.DefinitionRef.DefinitionID {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
}

// A stored row edited after loading is not the authored state: reload refuses
// instead of handing out the mutated row, and leaves the row alone.
func TestAgentLoadRefusesAMutatedRowButIgnoresEnabledAndTitle(t *testing.T) {
	ctx := context.Background()
	svc, root := loadFixture(t, "Body.", "")
	first, err := loadDir(t, svc, root, AgentLoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	row, err := svc.Store.GetAgentHostSettings(ctx, first.HostSettingsID)
	if err != nil {
		t.Fatal(err)
	}
	mutated := row
	mutated.Settings.Model = "different-model"
	if _, err = svc.Store.UpdateAgentHostSettings(ctx, mutated, row.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = loadDir(t, svc, root, AgentLoadOptions{}); err == nil || !strings.Contains(err.Error(), "model") {
		t.Fatalf("err = %v", err)
	}
	got, err := svc.Store.GetAgentHostSettings(ctx, first.HostSettingsID)
	if err != nil || got.Settings.Model != "different-model" {
		t.Fatalf("row was altered by the refused load: %+v, %v", got, err)
	}

	// Title and enabled are operator-owned, not authored state: a retitled,
	// disabled row is still the loaded state and is reused untouched.
	svc2, root2 := loadFixture(t, "Body.", "")
	f2, err := loadDir(t, svc2, root2, AgentLoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc2.Store.DisableAgentHostSettings(ctx, f2.HostSettingsID, f2.HostSettings.Revision); err != nil {
		t.Fatal(err)
	}
	again, err := loadDir(t, svc2, root2, AgentLoadOptions{})
	if err != nil || again.Changed || again.HostSettingsID != f2.HostSettingsID {
		t.Fatalf("again = %+v, %v", again, err)
	}
}
