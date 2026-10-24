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

func TestResolveAgentMode(t *testing.T) {
	dev := DeployLabels{Env: "dev", Admin: "self"}
	for name, tc := range map[string]struct {
		host, app string
		labels    DeployLabels
		want      string
	}{
		"host.yaml wins over everything":      {"pinned", "live", dev, "pinned"},
		"host live wins over a prod label":    {"live", "pinned", DeployLabels{Env: "prod"}, "live"},
		"app default beats the label":         {"", "pinned", dev, "pinned"},
		"app default live on an unlabeled":    {"", "live", DeployLabels{}, "live"},
		"env dev, admin self":                 {"", "", dev, "live"},
		"env dev, no admin label":             {"", "", DeployLabels{Env: "dev"}, "live"},
		"env dev but admin owner":             {"", "", DeployLabels{Env: "dev", Admin: "owner"}, "pinned"},
		"env prod":                            {"", "", DeployLabels{Env: "prod", Admin: "self"}, "pinned"},
		"no label at all":                     {"", "", DeployLabels{}, "pinned"},
		"an unknown app default is ignored":   {"", "sometimes", dev, "live"},
		"a hostname-like env is not a signal": {"", "", DeployLabels{Env: "development-box"}, "pinned"},
	} {
		t.Run(name, func(t *testing.T) {
			if got, why := ResolveAgentMode(tc.host, tc.app, tc.labels); got != tc.want || why == "" {
				t.Fatalf("mode = %q (%q), want %q", got, why, tc.want)
			}
		})
	}
}

func TestAgentLoadDefaultModeFollowsTheRule(t *testing.T) {
	svc, root := loadFixture(t, "Version one.", "mode: \"\"\n") // "mode:" present, so the fixture adds no mode; empty = unset
	if _, err := loadDir(t, svc, root, AgentLoadOptions{}); err != nil {
		t.Fatal(err)
	}
	writeLoadDirRev(t, root, canonicalTempDir(t), "2", "Version two.", "mode: \"\"\n")
	// Unlabeled: pinned, so a second revision needs --force, and the error says why.
	_, err := loadDir(t, svc, root, AgentLoadOptions{})
	if !errors.Is(err, store.ErrAgentLoadPinned) || !strings.Contains(err.Error(), "not labeled env=dev") {
		t.Fatalf("unlabeled second revision: %v", err)
	}
	// An operator at a shell is present, so the fallback alone does not pin.
	att, err := loadDir(t, svc, root, AgentLoadOptions{Attended: true})
	if err != nil || att.Mode != "unpinned" || !att.Changed {
		t.Fatalf("attended load = %+v, %v", att, err)
	}
	writeLoadDirRev(t, root, canonicalTempDir(t), "3", "Version three.", "mode: \"\"\n")
	// A dev label makes it live.
	res, err := loadDir(t, svc, root, AgentLoadOptions{Labels: DeployLabels{Env: "dev", Admin: "self"}})
	if err != nil || res.Mode != "live" || !res.Changed {
		t.Fatalf("dev-labeled load = %+v, %v", res, err)
	}
}

func TestAgentReloadPicksUpAnEditedDirectoryAndLeavesTheOldRow(t *testing.T) {
	ctx := context.Background()
	svc, root := loadFixture(t, "Version one.", "")
	first, err := loadDir(t, svc, root, AgentLoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	view, err := svc.Store.GetAgentView(ctx, first.Slug)
	if err != nil || view.HostSettings.Settings.AgentDir == "" || view.HostSettings.Settings.Mode != "live" {
		t.Fatalf("host row does not record its directory and mode: %+v, %v", view.HostSettings.Settings, err)
	}
	// Nothing changed: a reload is a no-op, not a new row.
	same, err := svc.Reload(ctx, first.Slug, AgentLoadOptions{}, "operator")
	if err != nil || same.Changed || same.HostSettingsID != first.HostSettingsID {
		t.Fatalf("reload of an unchanged directory = %+v, %v", same, err)
	}
	writeLoadDirRev(t, root, canonicalTempDir(t), "2", "Version two.", "")
	second, err := svc.Reload(ctx, first.HostSettingsID, AgentLoadOptions{}, "operator")
	if err != nil || !second.Changed || second.HostSettingsID == first.HostSettingsID || second.Previous != first.Slug {
		t.Fatalf("reload after an edit = %+v, %v", second, err)
	}
	if len(second.Tools) != 0 || second.AuthorityID != "" {
		t.Fatalf("a reload must not carry authority forward: %+v", second)
	}
	old, err := svc.Store.GetAgentHostSettings(ctx, first.HostSettingsID)
	if err != nil || old.Revision != first.HostSettings.Revision || !old.Enabled {
		t.Fatalf("the row a running session was admitted under changed: %+v, %v", old, err)
	}
}

func TestAgentReloadOfAPinnedAgentNeedsForce(t *testing.T) {
	ctx := context.Background()
	svc, root := loadFixture(t, "Version one.", "mode: pinned\n")
	first, err := loadDir(t, svc, root, AgentLoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	writeLoadDirRev(t, root, canonicalTempDir(t), "2", "Version two.", "mode: pinned\n")
	if _, err = svc.Reload(ctx, first.Slug, AgentLoadOptions{}, "operator"); !errors.Is(err, store.ErrAgentLoadPinned) {
		t.Fatalf("pinned reload = %v, want ErrAgentLoadPinned", err)
	}
	if res, err := svc.Reload(ctx, first.Slug, AgentLoadOptions{Force: true}, "operator"); err != nil || !res.Changed {
		t.Fatalf("forced reload = %+v, %v", res, err)
	}
}

func TestAgentReloadRefusals(t *testing.T) {
	ctx := context.Background()
	svc, root := loadFixture(t, "Version one.", "slug: alpha\n")
	first, err := loadDir(t, svc, root, AgentLoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Run("not from a directory", func(t *testing.T) {
		setup := &AgentSetupService{Store: svc.Store}
		res, setupErr := setup.Setup(ctx, AgentSetupRequest{Definition: []byte(setupFixtureDefinition), Provider: "claude", Runtime: "cli", Directories: []string{canonicalTempDir(t)}, Tools: []string{store.AllAvailableTools}, Reason: "x"}, "operator")
		if setupErr != nil {
			t.Skipf("setup unavailable here: %v", setupErr)
		}
		if _, err := svc.Reload(ctx, res.Slug, AgentLoadOptions{}, "operator"); !errors.Is(err, ErrAgentSetupInvalid) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("directory now names another agent", func(t *testing.T) {
		host, _ := os.ReadFile(filepath.Join(root, "host.yaml"))
		if err := os.WriteFile(filepath.Join(root, "host.yaml"), []byte(strings.Replace(string(host), "slug: alpha", "slug: beta", 1)), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Reload(ctx, first.Slug, AgentLoadOptions{}, "operator"); !errors.Is(err, ErrAgentSetupInvalid) || !strings.Contains(err.Error(), "beta") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("unknown host", func(t *testing.T) {
		if _, err := svc.Reload(ctx, "nope", AgentLoadOptions{}, "operator"); err == nil {
			t.Fatal("reloaded a host that does not exist")
		}
	})
}

func TestAgentLoadRefusesALiveAgentThatCanWriteItsOwnDefinition(t *testing.T) {
	root := t.TempDir()
	svc := &AgentLoadService{Store: newTestStore(t)}
	// host.yaml grants the directory that holds the definition itself.
	writeLoadDir(t, root, root, "Version one.", "")
	d, err := agentdir.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Load(context.Background(), d, AgentLoadOptions{}, "test"); !errors.Is(err, ErrAgentSetupInvalid) || !strings.Contains(err.Error(), "rewrite its own definition") {
		t.Fatalf("live load of a self-editable agent: %v", err)
	}
	// A directory inside the agent's directories is just as writable.
	parent := t.TempDir()
	nested := filepath.Join(parent, ".nanite")
	if err = os.MkdirAll(nested, 0o750); err != nil {
		t.Fatal(err)
	}
	writeLoadDir(t, nested, parent, "Version one.", "")
	d, err = agentdir.Load(nested)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Load(context.Background(), d, AgentLoadOptions{}, "test"); !errors.Is(err, ErrAgentSetupInvalid) {
		t.Fatalf("live load of a nested definition: %v", err)
	}
	// Pinned changes only by an explicit operator load, so it stays loadable.
	pinnedRoot := t.TempDir()
	writeLoadDir(t, pinnedRoot, pinnedRoot, "Version one.", "mode: pinned\n")
	d, err = agentdir.Load(pinnedRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Load(context.Background(), d, AgentLoadOptions{}, "test"); err != nil {
		t.Fatalf("pinned load: %v", err)
	}
}

const setupFixtureDefinition = "---\nschema_version: \"2\"\ndefinition_id: def:reload-setup\nrevision: \"1\"\nname: reload-setup\ndescription: Fixture.\nbehavior:\n  purpose: Complete the requested work.\nrequirements: {}\nharness_profile:\n  context: {}\n  permissions:\n    profile: default\ncontinuity:\n  mode: ephemeral\n---\nFixture.\n"
