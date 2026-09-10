package agentimport

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/agent"
	adapterclaude "github.com/hollis-labs/nanite/internal/plugin/builtin/adapter-claude"
	"github.com/hollis-labs/nanite/internal/store"
)

const claudeSubagent = `---
name: code-reviewer
description: Expert code review specialist
tools: Read, Grep, Bash
model: sonnet
---
You are a code reviewer.
`

func claudeRegistry() *agent.AdapterRegistry {
	r := agent.NewAdapterRegistry()
	r.Register(adapterclaude.New().Adapter())
	return r
}

// chain mirrors what cmd/nanite builds: Nanite's own format first, adapters
// after.
func chain(r *agent.AdapterRegistry) Parser {
	return ChainParser{Parsers: []Parser{NativeParser{}, RegistryParser{Registry: r}}}
}

// TestSeam_ClaudeSubagentReachesTheDatabase is CW-20260910-0012's "prove the
// seam rather than assert it" test: a real Claude subagent file, through the
// real adapter, through the real pipeline, into a real agent_profiles row —
// with the right ownership on the other side.
func TestSeam_ClaudeSubagentReachesTheDatabase(t *testing.T) {
	st := newTestStore(t)
	root := t.TempDir()
	path := filepath.Join(root, adapterclaude.SubagentsDir, "code-reviewer.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(claudeSubagent), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	imp := &Importer{Store: st, Parse: chain(claudeRegistry())}
	// The operator names the project root, not the file — the same shape a
	// Cairn-planted boot directory has.
	res, err := imp.Import(context.Background(), Source{Path: root})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if created, _, _ := res.Counts(); created != 1 {
		t.Fatalf("want 1 created, got %+v", res.Outcomes)
	}

	row, err := st.GetAgentBySlug(context.Background(), "code-reviewer")
	if err != nil {
		t.Fatalf("GetAgentBySlug: %v", err)
	}
	if row.SystemPrompt != "You are a code reviewer." {
		t.Errorf("SystemPrompt = %q — the body IS the agent, charter/lens already collapsed upstream", row.SystemPrompt)
	}
	// Two columns, two questions: where it came from, and how it got here.
	if row.OriginSystem != adapterclaude.AdapterName {
		t.Errorf("OriginSystem = %q, want %q", row.OriginSystem, adapterclaude.AdapterName)
	}
	if row.Source != SourceProvenance {
		t.Errorf("Source = %q, want %q — an adapter never declares an imported agent operator-owned",
			row.Source, SourceProvenance)
	}
	if class := agent.NewClassification().Classify(row.Source); class != agent.ManageClassExternal {
		t.Errorf("class = %q, want external", class)
	}
	// The decided rules, observed at the far end of the seam.
	if row.DefaultModel != "" {
		t.Errorf("DefaultModel = %q, want blank — a Claude alias is not a Nanite model ID", row.DefaultModel)
	}
	if row.Tools != "[]" {
		t.Errorf("Tools = %q, want empty — Claude tool names would produce grants that never resolve", row.Tools)
	}
}

// TestChain_NativeFormatWinsOverAdapters — a definition authored for Nanite is
// the ordinary case and must not be re-read through a foreign format's lens.
func TestChain_NativeFormatWinsOverAdapters(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "reviewer.md")
	// Declares BOTH a Nanite slug and a Claude-style name.
	body := "---\nname: code-reviewer\nslug: nanite-authored\n---\nbody\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	defs, err := chain(claudeRegistry()).Parse(path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(defs) != 1 {
		t.Fatalf("want 1, got %d", len(defs))
	}
	if defs[0].Slug != "nanite-authored" {
		t.Errorf("Slug = %q — an explicit slug means the file was authored for Nanite", defs[0].Slug)
	}
	if defs[0].Source != NativeOriginSystem {
		t.Errorf("Source = %q, want %q", defs[0].Source, NativeOriginSystem)
	}
}

// TestChain_NativeDeclinesAForeignFile is the discrimination that makes
// "native first" safe rather than "native grabs everything." Without the
// explicit-slug requirement, ParseMDFile's filename fallback would let the
// native reader claim a Claude subagent and import it under the wrong format.
func TestChain_NativeDeclinesAForeignFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code-reviewer.md")
	if err := os.WriteFile(path, []byte(claudeSubagent), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := (NativeParser{}).Parse(path); !errors.Is(err, ErrNotThisFormat) {
		t.Fatalf("NativeParser must decline a file with no explicit slug, got %v", err)
	}

	defs, err := chain(claudeRegistry()).Parse(path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(defs) != 1 || defs[0].Source != adapterclaude.AdapterName {
		t.Fatalf("the chain must fall through to the claude adapter: %+v", defs)
	}
}

// TestChain_NothingClaimsItReportsWhy — an operator who mistyped a definition
// gets told what is wrong with it, not just that nothing was found.
func TestChain_NothingClaimsItReportsWhy(t *testing.T) {
	st := newTestStore(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(path, []byte("# Just prose, no frontmatter\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	imp := &Importer{Store: st, Parse: chain(claudeRegistry())}
	_, err := imp.Import(context.Background(), Source{Path: path})
	if !errors.Is(err, ErrNoDefinitions) {
		t.Fatalf("err = %v, want ErrNoDefinitions", err)
	}
	if !strings.Contains(err.Error(), "frontmatter") {
		t.Errorf("err = %v, want the native reader's decline reason carried through", err)
	}
}

// TestRegistryParser_UnknownAdapterIsAnError — `--adapter typo` must not fall
// back to guessing.
func TestRegistryParser_UnknownAdapterIsAnError(t *testing.T) {
	p := RegistryParser{Registry: claudeRegistry(), Adapter: "cladue"}
	_, err := p.Parse("/tmp/whatever")
	if err == nil {
		t.Fatal("expected an error for an unknown adapter name")
	}
	if !strings.Contains(err.Error(), "claude") {
		t.Errorf("err = %v, want the registered names listed so the typo is fixable", err)
	}
}

// TestSeam_ImportedClaudeAgentIsStillRefusedOverAnIncumbent — the ownership
// boundary does not weaken just because the definition arrived through an
// adapter.
func TestSeam_ImportedClaudeAgentIsStillRefusedOverAnIncumbent(t *testing.T) {
	st := newTestStore(t)
	if err := st.CreateAgent(context.Background(), &store.AgentProfile{
		Name: "Reviewer", Slug: "code-reviewer", SystemPrompt: "incumbent", Source: "internal",
	}); err != nil {
		t.Fatalf("seed incumbent: %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "code-reviewer.md")
	if err := os.WriteFile(path, []byte(claudeSubagent), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	imp := &Importer{Store: st, Parse: chain(claudeRegistry())}
	res, err := imp.Import(context.Background(), Source{Path: path})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if _, _, skipped := res.Counts(); skipped != 1 {
		t.Fatalf("want 1 skipped, got %+v", res.Outcomes)
	}
	row, _ := st.GetAgentBySlug(context.Background(), "code-reviewer")
	if row.SystemPrompt != "incumbent" {
		t.Errorf("incumbent overwritten: %q", row.SystemPrompt)
	}
}
