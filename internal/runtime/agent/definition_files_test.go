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
