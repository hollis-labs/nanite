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

func TestSeam_ClaudeSubagentReachesTheDatabase(t *testing.T) {
	st := newTestStore(t)
	root := t.TempDir()
	path := filepath.Join(root, adapterclaude.SubagentsDir, "code-reviewer.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(claudeSubagent), 0o600); err != nil {
		t.Fatal(err)
	}
	parser := chain(claudeRegistry())
	defs, err := parser.Parse(root)
	if err != nil || len(defs) != 1 {
		t.Fatalf("real adapter parse: %+v, %v", defs, err)
	}
	def := defs[0]
	if def.Slug != "code-reviewer" || def.SystemPrompt != "You are a code reviewer." || def.Source != adapterclaude.AdapterName || def.Model != "" || len(def.Tools) != 0 || len(def.RoleTools) != 0 {
		t.Fatalf("adapter lost body/provenance or retained foreign model: %+v", def)
	}
	before := importBoundarySnapshot(t, st)
	requireRetiredImport(t, &Importer{Store: st, Parse: parser}, root)
	requireImportStateUnchanged(t, st, before)
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
	retainedImportProfile(t, st, "code-reviewer", "internal")
	path := writeDef(t, "code-reviewer.md", claudeSubagent)
	before := importBoundarySnapshot(t, st)
	requireRetiredImport(t, &Importer{Store: st, Parse: chain(claudeRegistry())}, path)
	requireImportStateUnchanged(t, st, before)
}
