package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestDefinitionFilesDropsAnythingOutsideTheFilesRoot(t *testing.T) {
	params := SetupParams{AgentProfile: &store.AgentProfile{DefinitionFiles: map[string][]byte{
		"agent/context/a.md": []byte("a"),
		"CLAUDE.md":          []byte("hijack"),
		".mcp.json":          []byte("{}"),
		"agent/../CLAUDE.md": []byte("hijack"),
	}}}
	got := definitionFiles(params)
	if len(got) != 1 || string(got["agent/context/a.md"]) != "a" {
		t.Fatalf("definitionFiles = %v", got)
	}
}

func TestPlantDefinitionFilesWritesUnderTheWorkspaceAndRefusesEscapes(t *testing.T) {
	ws, err := EnsureSessionWorkspace(t.TempDir(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if err = PlantDefinitionFiles(ws, map[string][]byte{"agent/context/a.md": []byte("a")}); err != nil {
		t.Fatal(err)
	}
	if b, readErr := os.ReadFile(filepath.Join(ws.Root, "agent", "context", "a.md")); readErr != nil || string(b) != "a" {
		t.Fatalf("planted file = %q, %v", b, readErr)
	}
	if err = PlantDefinitionFiles(ws, map[string][]byte{"../escape": []byte("x")}); err == nil {
		t.Fatal("planted a path outside the files root")
	}
}

// Every CLI runtime plants the definition's reference files through its own
// plant spec, so a CLI agent finds them in its boot root as an API agent finds
// them in its workspace.
func TestEveryLayoutPlantsDefinitionFiles(t *testing.T) {
	params := SetupParams{
		SessionID:    "sess-files",
		AgentProfile: &store.AgentProfile{Name: "Files", Slug: "files", DefinitionFiles: map[string][]byte{"agent/context/a.md": []byte("a")}},
		SystemPrompt: "be helpful",
		BootContent:  "# kickoff\n",
	}
	for name, spec := range map[string]func() (map[string][]byte, error){
		"claude":   func() (map[string][]byte, error) { s, err := claudePlantSpec(t.TempDir(), params); return s.Files, err },
		"codex":    func() (map[string][]byte, error) { s, err := codexPlantSpec(params); return s.Files, err },
		"opencode": func() (map[string][]byte, error) { s, err := opencodePlantSpec(params); return s.Files, err },
		"agy":      func() (map[string][]byte, error) { s, err := agyPlantSpec(params); return s.Files, err },
	} {
		files, err := spec()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(files["agent/context/a.md"]) != "a" {
			t.Errorf("%s did not plant agent/context/a.md; files: %d", name, len(files))
		}
	}
}

// A granted skill is planted by every CLI runtime under agent/skills/<name>/
// and, where the shared layout table has a skill location for the runtime, also
// at that runtime's own <root>/<name>/.
func TestEveryLayoutPlantsGrantedSkills(t *testing.T) {
	params := SetupParams{
		SessionID: "sess-skills",
		AgentProfile: &store.AgentProfile{Name: "Skills", Slug: "skills", DefinitionFiles: map[string][]byte{
			"agent/skills/review/SKILL.md":       []byte("# review\n"),
			"agent/skills/review/scripts/run.sh": []byte("echo ok\n"),
		}},
		SystemPrompt: "be helpful",
		BootContent:  "# kickoff\n",
	}
	for name, c := range map[string]struct {
		provider string
		spec     func() (map[string][]byte, error)
	}{
		"claude":   {"claude", func() (map[string][]byte, error) { s, err := claudePlantSpec(t.TempDir(), params); return s.Files, err }},
		"codex":    {"codex", func() (map[string][]byte, error) { s, err := codexPlantSpec(params); return s.Files, err }},
		"opencode": {"opencode", func() (map[string][]byte, error) { s, err := opencodePlantSpec(params); return s.Files, err }},
		"agy":      {"antigravity", func() (map[string][]byte, error) { s, err := agyPlantSpec(params); return s.Files, err }},
	} {
		files, err := c.spec()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(files["agent/skills/review/SKILL.md"]) != "# review\n" || string(files["agent/skills/review/scripts/run.sh"]) != "echo ok\n" {
			t.Errorf("%s did not plant the skill under agent/skills/", name)
		}
		if dest := skillDestPrefixesFor(c.provider); dest != nil {
			prefixes, ok := dest("review")
			if !ok || len(prefixes) == 0 {
				t.Fatalf("%s: no skill destination", name)
			}
			for _, p := range prefixes {
				if string(files[p+"/SKILL.md"]) != "# review\n" || string(files[p+"/scripts/run.sh"]) != "echo ok\n" {
					t.Errorf("%s did not plant the skill at its own location %s", name, p)
				}
			}
		} else {
			t.Logf("%s has no shared skill location; only agent/skills/ is planted", name)
		}
	}
	// A path the profile's own skills already planted is not replaced.
	taken := SetupParams{AgentProfile: &store.AgentProfile{DefinitionFiles: params.AgentProfile.DefinitionFiles}}
	if prefixes, ok := skillDestPrefixesFor("claude")("review"); ok {
		out := definitionFilesFor("claude", taken)
		if len(out) <= len(definitionFiles(taken)) || out[prefixes[0]+"/SKILL.md"] == nil {
			t.Fatalf("mirror missing: %v", out)
		}
	}
	// Files outside agent/skills/<name>/<file> are never mirrored.
	odd := SetupParams{AgentProfile: &store.AgentProfile{DefinitionFiles: map[string][]byte{"agent/skills/loose.md": []byte("x"), "agent/context/a.md": []byte("a")}}}
	if got := definitionFilesFor("claude", odd); len(got) != 2 {
		t.Fatalf("unexpected mirror: %v", got)
	}
}
