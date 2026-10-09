package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
	plant "github.com/hollis-labs/substrate/harness/agentlaunch/planting"
	"github.com/hollis-labs/substrate/harness/workspace/materialize"
)

// Historical roots retain their resume files and legacy manifest. The current
// inactive-only contract refuses replanting instead of guessing custody or
// deleting the manifest produced by an earlier operation.

// resumeState is what a provider or Nanite writes into a live boot dir
// outside the planter, and what a resumed session or durable agent reads
// back. None of it is in any planted tree.
var resumeState = map[string]map[string]string{
	"claude": {
		"recovery.md":                 "# recovery pack: prior turns\n",
		".claude/settings.local.json": `{"permissions":{"allow":["Bash(ls:*)"]}}`,
		".claude/projects/p/s.jsonl":  `{"sessionId":"ses_claude"}` + "\n",
	},
	"codex": {
		"recovery.md": "# recovery pack: prior turns\n",
		"sessions/2026/10/01/rollout-2026-10-01T00-00-00-ses_codex.jsonl": `{"type":"session_meta","payload":{"id":"ses_codex"}}` + "\n",
		"history.jsonl": `{"session_id":"ses_codex","text":"hi"}` + "\n",
	},
}

type upgradeLayout struct {
	name       string
	layout     Layout
	promptFile string
	skillPath  string
}

var upgradeLayouts = []upgradeLayout{
	{"claude", claudeLayout{}, "CLAUDE.md", ".claude/skills/demo/SKILL.md"},
	{"codex", codexLayout{}, "AGENTS.md", "skills/demo/SKILL.md"},
}

// prePlantedBootDir sets up a boot dir, then makes it look like one an
// agentkit before v0.7.0 planted: the manifest sits at the legacy path and
// the provider has written its resume state.
func prePlantedBootDir(t *testing.T, l upgradeLayout) (string, SetupParams) {
	t.Helper()
	params := SetupParams{
		SessionID:    "s-upgrade-" + l.name,
		AgentProfile: &store.AgentProfile{Name: "upgrade", Slug: "upgrade"},
		SystemPrompt: "prompt v1",
	}
	bootDir, err := l.layout.Setup(params)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(bootDir) })

	legacy := filepath.Join(bootDir, legacyManifestRelPath)
	if err := os.MkdirAll(filepath.Dir(legacy), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(materialize.ManifestPath(bootDir), legacy); err != nil {
		t.Fatalf("move manifest to the legacy path: %v", err)
	}
	for rel, body := range resumeState[l.name] {
		p := filepath.Join(bootDir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if !staleBootDir(bootDir) {
		t.Fatal("the pre-planted fixture is not recognized as stale")
	}
	params.SystemPrompt = "prompt v2"
	return bootDir, params
}

func TestBootDirManifestUpgrade_RefusesWithoutWiping(t *testing.T) {
	for _, l := range upgradeLayouts {
		populate := func(t *testing.T, bootDir string, params SetupParams) {
			t.Helper()
			if _, err := l.layout.Populate(bootDir, params); !errors.Is(err, ErrArtifactRefreshUnavailable) {
				t.Fatalf("Populate: %v", err)
			}
		}
		slot := func(t *testing.T, bootDir string, params SetupParams) {
			t.Helper()
			if err := l.layout.RegenerateSystemPromptSlot(bootDir, params); !errors.Is(err, ErrArtifactRefreshUnavailable) {
				t.Fatalf("RegenerateSystemPromptSlot: %v", err)
			}
		}
		// The write PlantAgentSkillFiles makes, minus the skill resolution.
		skill := func(t *testing.T, bootDir string, _ SetupParams) {
			t.Helper()
			spec := plant.PlantSpec{Files: map[string][]byte{l.skillPath: []byte("skill body")}}
			if _, err := plantSpec(context.Background(), bootDir, spec, plantConfig{provider: l.name}); !errors.Is(err, ErrArtifactRefreshUnavailable) {
				t.Fatalf("mid-session skill plant: %v", err)
			}
		}
		type step func(*testing.T, string, SetupParams)
		for _, seq := range []struct {
			name  string
			steps []step
		}{
			{"populate", []step{populate}},
			{"slot then populate", []step{slot, populate}},
			{"skill then populate", []step{skill, populate}},
			{"populate, slot, populate", []step{populate, slot, populate}},
		} {
			t.Run(l.name+"/"+seq.name, func(t *testing.T) {
				bootDir, params := prePlantedBootDir(t, l)
				for _, s := range seq.steps {
					s(t, bootDir, params)
				}

				for rel, want := range resumeState[l.name] {
					got, err := os.ReadFile(filepath.Join(bootDir, rel)) //nolint:gosec // reads a file this test wrote into its temp boot dir
					if err != nil || string(got) != want {
						t.Errorf("resume state %s = %q (err %v), want it untouched: %q", rel, got, err, want)
					}
				}
				prompt, err := os.ReadFile(filepath.Join(bootDir, l.promptFile)) //nolint:gosec // reads a file this test planted into its temp boot dir
				if err != nil || !strings.Contains(string(prompt), "prompt v1") {
					t.Errorf("%s was not re-planted with the new prompt (err %v)", l.promptFile, err)
				}
				if _, err := os.Stat(filepath.Join(bootDir, legacyManifestRelPath)); err != nil {
					t.Errorf("legacy manifest marker: %v, want it left in place", err)
				}
				if !staleBootDir(bootDir) {
					t.Error("a stale boot dir gained a current manifest; the next partial plant would make a full Populate refuse")
				}
			})
		}
	}
}

// A boot dir the current engine planted keeps its manifest and its
// ownership checks. The stale-dir handling must not reach it.
func TestBootDirManifestUpgrade_CurrentDirKeepsManifest(t *testing.T) {
	params := SetupParams{SessionID: "s-current", AgentProfile: &store.AgentProfile{Name: "current", Slug: "current"}}
	bootDir, err := claudeLayout{}.Setup(params)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(bootDir) })
	if err := (claudeLayout{}).RegenerateSystemPromptSlot(bootDir, params); !errors.Is(err, ErrArtifactRefreshUnavailable) {
		t.Fatalf("RegenerateSystemPromptSlot: %v", err)
	}
	if _, err := os.Stat(materialize.ManifestPath(bootDir)); err != nil {
		t.Fatalf("current manifest after a re-plant: %v, want it kept", err)
	}
	if staleBootDir(bootDir) {
		t.Fatal("a current boot dir reads as stale")
	}
}
