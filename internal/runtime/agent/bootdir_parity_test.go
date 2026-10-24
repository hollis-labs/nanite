package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/harness/core/adapters/layout/plan"
	"github.com/hollis-labs/libs/llm/core/contracts/runtimes"
	"github.com/hollis-labs/nanite/internal/store"
)

// Every boot-layer file the shared harness layout table (the table Tether
// plants from) places for a runtime is planted by Nanite at the same path, with
// the table's file mode where it names one. A divergence is either a Nanite
// bug or a deliberate difference that belongs in the exceptions below.
func TestPlantedFilesMatchTheSharedLayoutTable(t *testing.T) {
	// Intentional differences, each with its reason.
	skip := map[string]string{
		"codex:auth.json": "linked from the host login by codexLayout at Setup, only when the host is logged in",
		// The shared table places a .mcp.json mirror that neither runtime reads; Nanite
		// deliberately does not plant it, so the agent credential has one copy fewer
		// in the boot dir (CW-20261024-0011).
		"codex:.mcp.json":    "deliberately not planted: codex reads config.toml, the mirror only duplicates the credential",
		"opencode:.mcp.json": "deliberately not planted: opencode reads opencode.json, the mirror only duplicates the credential",
	}
	fields := map[plan.Field]bool{plan.Instructions: true, plan.Kickoff: true, plan.Settings: true, plan.PlantingPlugin: true, plan.MCP: true}
	for _, id := range []runtimes.ID{runtimes.Claude, runtimes.Codex, runtimes.OpenCode, runtimes.Antigravity} {
		t.Run(string(id), func(t *testing.T) {
			boot, err := bootdirLayoutFor(string(id)).Setup(SetupParams{
				SessionID: "parity-" + string(id), AgentProfile: &store.AgentProfile{ID: "a1", Name: "Parity", Slug: "parity"},
				SystemPrompt: "x", BootContent: "boot", ProjectDir: t.TempDir(),
				MCPConfig: MCPConfig{BinaryPath: "/bin/nanite", DBPath: "/tmp/x.db", ServerID: "nanite", APIBaseURL: "http://127.0.0.1:1",
					AgentCredential: func(string) (string, error) { return "c", nil }},
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(boot) })
			checked := map[string]bool{}
			for _, row := range plan.Table() {
				if row.Provider != id || row.Layer != plan.Boot || row.Capability != plan.Supported || row.Root != plan.RootBoot ||
					!fields[row.Field] || row.Form == plan.Link {
					continue
				}
				rel := row.CompatibilityMCPPath
				if rel == "" {
					rel = row.Path
				}
				rel = strings.ReplaceAll(rel, "{agent}", "parity")
				if checked[rel] {
					continue
				}
				checked[rel] = true
				if why, ok := skip[string(id)+":"+rel]; ok {
					t.Logf("skip %s: %s", rel, why)
					continue
				}
				info, err := os.Stat(filepath.Join(boot, rel))
				if err != nil {
					t.Errorf("table places %s (%s) but Nanite did not plant it: %v", rel, row.Field, err)
					continue
				}
				if want := row.CompatibilityModeBits; want != 0 && uint32(info.Mode().Perm()) != want {
					t.Errorf("%s mode = %o, table says %o", rel, info.Mode().Perm(), want)
				}
			}
			if len(checked) == 0 {
				t.Fatal("the layout table returned no rows for the runtime")
			}
		})
	}
}

func TestHasBootdirLayoutCoversEveryImplementedLayout(t *testing.T) {
	for _, p := range []string{"claude", "codex", "opencode", "antigravity"} {
		if !HasBootdirLayout(p) {
			t.Errorf("HasBootdirLayout(%q) = false, but %s has a layout", p, p)
		}
		if _, unsupported := bootdirLayoutFor(p).(unsupportedLayout); unsupported {
			t.Errorf("%s resolves to unsupportedLayout", p)
		}
	}
	for _, p := range []string{"gemini", "copilot", "aider", "junie", "anthropic"} {
		if HasBootdirLayout(p) {
			t.Errorf("HasBootdirLayout(%q) = true for a provider without a layout", p)
		}
	}
}
