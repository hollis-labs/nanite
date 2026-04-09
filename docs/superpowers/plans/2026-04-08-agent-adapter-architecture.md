# Agent Adapter Architecture Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Nanite a universal agent harness with adapter plugins for every major CLI ecosystem, a three-layer config override cascade, SQLite-native messaging, and the .agentrc → .nanite rename.

**Architecture:** Plugin-per-adapter model with core interface in `internal/agent/adapter.go`. Each CLI ecosystem (Claude, Codex, Gemini, Opencode) is a built-in plugin implementing `CLIAgentAdapter`. The nanite-native adapter (replacing agentrc-sync) adds `AgentComposer` for role-based prompt composition. Config overrides cascade Agent Base → Project → Session with per-field merge semantics.

**Tech Stack:** Go 1.26, SQLite (WAL), Nanite plugin system (`github.com/hollis-labs/go-plugin`), YAML/Markdown agent definitions.

**Spec:** `docs/superpowers/specs/2026-04-08-agent-adapter-architecture-design.md`

---

## File Map

### New Files

| File | Responsibility |
|---|---|
| `internal/agent/adapter.go` | CLIAgentAdapter + AgentComposer interfaces, AdapterRegistry, SandboxContext, RoleInfo, SkillInfo types |
| `internal/agent/managed_section.go` | Read/write `<!-- nanite:start -->` managed sections in markdown files |
| `internal/agent/managed_section_test.go` | Tests for managed section read/write/idempotency |
| `internal/agent/adapter_test.go` | Tests for AdapterRegistry (register, discover, dedup, priority) |
| `internal/agent/override/merge.go` | Resolve() function, OverrideConfig type, per-field merge logic |
| `internal/agent/override/merge_test.go` | Tests for all merge rules (scalar, list +/-, map deep-merge, wildcard) |
| `internal/plugin/builtin/adapter-nanite-native/plugin.go` | Nanite-native adapter (replaces agentrc-sync), implements CLIAgentAdapter + AgentComposer |
| `internal/plugin/builtin/adapter-claude/plugin.go` | Claude Code adapter, absorbs sandbox.go logic |
| `internal/plugin/builtin/adapter-codex/plugin.go` | Codex/Copilot adapter (AGENTS.md) |
| `internal/plugin/builtin/adapter-gemini/plugin.go` | Gemini adapter (GEMINI.md) |
| `internal/plugin/builtin/adapter-opencode/plugin.go` | Opencode adapter |
| `internal/store/messaging.go` | SQLite MessageStore implementation |
| `internal/store/messaging_test.go` | Tests for MessageStore CRUD + status lifecycle |
| `internal/store/migrations/004_messaging_and_overrides.sql` | messages table + session_agent_overrides table |

### Modified Files

| File | Changes |
|---|---|
| `internal/agent/discovery.go` | Replace tiers 5-6 with AdapterRegistry.DiscoverAll() call |
| `internal/sandbox/sandbox.go` | Delegate content writing to AdapterRegistry.PopulateAllSandboxes() |
| `internal/service/agent.go` | Add override resolution via Resolve(); wire AdapterRegistry |
| `internal/service/container.go` | Create AdapterRegistry, register adapters, wire into AgentService |
| `internal/api/a2a.go` | Replace nexus/messaging with store.MessageStore; drop conversion functions |
| `cmd/nanite/main.go` | Remove Nexus Postgres init (lines 389-409); remove nexus import |
| `go.mod` | Remove `github.com/hollis-labs/nexus` + `github.com/lib/pq` |
| `internal/plugin/builtin/agentrc/plugin.go` | Delete (replaced by adapter-nanite-native) |

---

## Task 1: Core Adapter Interface + Registry

**Files:**
- Create: `internal/agent/adapter.go`
- Create: `internal/agent/adapter_test.go`

- [ ] **Step 1: Write failing test for AdapterRegistry**

```go
// internal/agent/adapter_test.go
package agent

import (
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// stubAdapter implements CLIAgentAdapter for testing.
type stubAdapter struct {
	name     string
	priority int
	agents   []Definition
}

func (s *stubAdapter) Name() string      { return s.name }
func (s *stubAdapter) Priority() int     { return s.priority }
func (s *stubAdapter) Discover(projectDir string) ([]Definition, error) {
	return s.agents, nil
}
func (s *stubAdapter) PopulateSandbox(sandboxDir string, agent store.AgentProfile, ctx SandboxContext) error {
	return nil
}
func (s *stubAdapter) SyncProjectRoot(projectDir string, agents []store.AgentProfile) error {
	return nil
}

func TestAdapterRegistry_Register_And_Priority(t *testing.T) {
	reg := NewAdapterRegistry()
	reg.Register(&stubAdapter{name: "low", priority: 70})
	reg.Register(&stubAdapter{name: "high", priority: 50})

	adapters := reg.Adapters()
	if len(adapters) != 2 {
		t.Fatalf("expected 2 adapters, got %d", len(adapters))
	}
	if adapters[0].Name() != "high" {
		t.Errorf("expected highest priority first, got %s", adapters[0].Name())
	}
}

func TestAdapterRegistry_DiscoverAll_Deduplicates(t *testing.T) {
	reg := NewAdapterRegistry()
	reg.Register(&stubAdapter{
		name:     "first",
		priority: 50,
		agents:   []Definition{{Slug: "agent-a"}},
	})
	reg.Register(&stubAdapter{
		name:     "second",
		priority: 70,
		agents:   []Definition{{Slug: "agent-a"}, {Slug: "agent-b"}},
	})

	defs, err := reg.DiscoverAll("/tmp/project")
	if err != nil {
		t.Fatal(err)
	}
	// agent-a from "first" (higher priority) wins; agent-b from "second" included
	if len(defs) != 2 {
		t.Fatalf("expected 2 unique agents, got %d", len(defs))
	}
}

func TestAdapterRegistry_GetAdapter(t *testing.T) {
	reg := NewAdapterRegistry()
	reg.Register(&stubAdapter{name: "claude", priority: 60})

	a, ok := reg.GetAdapter("claude")
	if !ok || a.Name() != "claude" {
		t.Error("expected to find claude adapter")
	}
	_, ok = reg.GetAdapter("nonexistent")
	if ok {
		t.Error("expected not found for nonexistent adapter")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/chrispian/Projects-apps/nanite && go test ./internal/agent/ -run TestAdapter -v`
Expected: FAIL — types and functions not defined.

- [ ] **Step 3: Write the adapter interface and registry**

```go
// internal/agent/adapter.go
package agent

import (
	"sort"
	"sync"

	"github.com/hollis-labs/nanite/internal/store"
)

// CLIAgentAdapter reads agent definitions from an external CLI ecosystem
// and writes sandbox config files that the ecosystem's CLI understands.
type CLIAgentAdapter interface {
	// Name returns the adapter identifier (e.g., "nanite-native", "claude", "codex").
	Name() string

	// Discover scans projectDir for agent definitions in this ecosystem's format.
	// Returns normalized Definitions.
	Discover(projectDir string) ([]Definition, error)

	// PopulateSandbox writes CLI-specific config files into sandboxDir.
	PopulateSandbox(sandboxDir string, agent store.AgentProfile, session SandboxContext) error

	// SyncProjectRoot writes/updates managed sections in the project root.
	SyncProjectRoot(projectDir string, agents []store.AgentProfile) error

	// Priority determines discovery order. Lower = checked first.
	Priority() int
}

// AgentComposer is an optional extension for adapters that compose agents
// from multiple sources (roles, skills, context files).
type AgentComposer interface {
	CLIAgentAdapter

	// ComposePrompt assembles a system prompt from the adapter's composition model.
	ComposePrompt(agentConfig interface{}, projectDir string) (string, error)

	// ListRoles returns available roles this composer can resolve.
	ListRoles() ([]RoleInfo, error)

	// ListSkills returns available skills this composer can resolve.
	ListSkills() ([]SkillInfo, error)
}

// SandboxContext carries session-specific data for sandbox population.
type SandboxContext struct {
	SessionID  string
	WorkingDir string
	DBPath     string
	MCPServers []string
	Overrides  map[string]interface{}
}

// RoleInfo describes an available role for prompt composition.
type RoleInfo struct {
	Name        string
	Type        string // "domain", "stack", "meta"
	Description string
	FilePath    string
}

// SkillInfo describes an available skill.
type SkillInfo struct {
	Name        string
	Description string
	FilePath    string
}

// AdapterRegistry manages loaded CLI agent adapters.
type AdapterRegistry struct {
	mu       sync.RWMutex
	adapters []CLIAgentAdapter
}

// NewAdapterRegistry creates an empty adapter registry.
func NewAdapterRegistry() *AdapterRegistry {
	return &AdapterRegistry{}
}

// Register adds an adapter and re-sorts by priority.
func (r *AdapterRegistry) Register(a CLIAgentAdapter) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.adapters = append(r.adapters, a)
	sort.Slice(r.adapters, func(i, j int) bool {
		return r.adapters[i].Priority() < r.adapters[j].Priority()
	})
}

// Adapters returns a copy of the registered adapters sorted by priority.
func (r *AdapterRegistry) Adapters() []CLIAgentAdapter {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]CLIAgentAdapter, len(r.adapters))
	copy(out, r.adapters)
	return out
}

// DiscoverAll iterates all adapters in priority order and returns
// deduplicated agent definitions. First adapter to define a slug wins.
func (r *AdapterRegistry) DiscoverAll(projectDir string) ([]Definition, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	seen := make(map[string]bool)
	var result []Definition

	for _, a := range r.adapters {
		defs, err := a.Discover(projectDir)
		if err != nil {
			return nil, err
		}
		for _, d := range defs {
			if !seen[d.Slug] {
				seen[d.Slug] = true
				result = append(result, d)
			}
		}
	}
	return result, nil
}

// PopulateAllSandboxes calls PopulateSandbox on every registered adapter.
func (r *AdapterRegistry) PopulateAllSandboxes(sandboxDir string, agent store.AgentProfile, session SandboxContext) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, a := range r.adapters {
		if err := a.PopulateSandbox(sandboxDir, agent, session); err != nil {
			return err
		}
	}
	return nil
}

// SyncAllProjectRoots calls SyncProjectRoot on every registered adapter.
func (r *AdapterRegistry) SyncAllProjectRoots(projectDir string, agents []store.AgentProfile) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, a := range r.adapters {
		if err := a.SyncProjectRoot(projectDir, agents); err != nil {
			return err
		}
	}
	return nil
}

// GetAdapter returns a registered adapter by name.
func (r *AdapterRegistry) GetAdapter(name string) (CLIAgentAdapter, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, a := range r.adapters {
		if a.Name() == name {
			return a, true
		}
	}
	return nil, false
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/chrispian/Projects-apps/nanite && go test ./internal/agent/ -run TestAdapter -v`
Expected: PASS (3 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/agent/adapter.go internal/agent/adapter_test.go
git commit -m "feat(agent): add CLIAgentAdapter interface and AdapterRegistry"
```

---

## Task 2: Managed Section Helper

**Files:**
- Create: `internal/agent/managed_section.go`
- Create: `internal/agent/managed_section_test.go`

- [ ] **Step 1: Write failing tests for managed section operations**

```go
// internal/agent/managed_section_test.go
package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteManagedSection_NewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")

	err := WriteManagedSection(path, "Hello from Nanite")
	if err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	content := string(data)

	if !strings.Contains(content, "<!-- nanite:start -->") {
		t.Error("missing start marker")
	}
	if !strings.Contains(content, "Hello from Nanite") {
		t.Error("missing managed content")
	}
	if !strings.Contains(content, "<!-- nanite:end -->") {
		t.Error("missing end marker")
	}
}

func TestWriteManagedSection_PreservesUserContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")

	// Write existing user content
	os.WriteFile(path, []byte("# My Project\n\nUser content here.\n"), 0644)

	err := WriteManagedSection(path, "Nanite content")
	if err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	content := string(data)

	if !strings.Contains(content, "# My Project") {
		t.Error("user content was lost")
	}
	if !strings.Contains(content, "User content here.") {
		t.Error("user content was lost")
	}
	if !strings.Contains(content, "Nanite content") {
		t.Error("managed content missing")
	}
}

func TestWriteManagedSection_ReplacesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")

	// Write file with existing managed section
	os.WriteFile(path, []byte(
		"# My Project\n\n"+
			"<!-- nanite:start -->\n<!-- DO NOT EDIT — managed by Nanite. Edit NANITE.md instead. -->\n\nOld content\n\n<!-- nanite:end -->\n\n"+
			"More user content\n",
	), 0644)

	err := WriteManagedSection(path, "New content")
	if err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	content := string(data)

	if strings.Contains(content, "Old content") {
		t.Error("old managed content should be replaced")
	}
	if !strings.Contains(content, "New content") {
		t.Error("new managed content missing")
	}
	if !strings.Contains(content, "More user content") {
		t.Error("user content after section was lost")
	}
}

func TestWriteManagedSection_Idempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")

	WriteManagedSection(path, "Same content")
	data1, _ := os.ReadFile(path)

	WriteManagedSection(path, "Same content")
	data2, _ := os.ReadFile(path)

	if string(data1) != string(data2) {
		t.Error("writing same content should be idempotent")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/chrispian/Projects-apps/nanite && go test ./internal/agent/ -run TestWriteManagedSection -v`
Expected: FAIL — `WriteManagedSection` not defined.

- [ ] **Step 3: Implement managed section helper**

```go
// internal/agent/managed_section.go
package agent

import (
	"fmt"
	"os"
	"strings"
)

const (
	managedStart  = "<!-- nanite:start -->"
	managedNotice = "<!-- DO NOT EDIT — managed by Nanite. Edit NANITE.md instead. -->"
	managedEnd    = "<!-- nanite:end -->"
)

// WriteManagedSection writes or replaces the Nanite-managed section in a markdown file.
// User content outside the markers is preserved. If the file doesn't exist, it is created
// with just the managed section. If markers don't exist, the section is appended.
func WriteManagedSection(path string, content string) error {
	managed := fmt.Sprintf("%s\n%s\n\n%s\n\n%s", managedStart, managedNotice, content, managedEnd)

	existing, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return os.WriteFile(path, []byte(managed+"\n"), 0644)
		}
		return err
	}

	text := string(existing)
	startIdx := strings.Index(text, managedStart)
	endIdx := strings.Index(text, managedEnd)

	var result string
	if startIdx >= 0 && endIdx >= 0 && endIdx > startIdx {
		// Replace existing managed section
		before := text[:startIdx]
		after := text[endIdx+len(managedEnd):]
		result = before + managed + after
	} else {
		// Append managed section
		result = strings.TrimRight(text, "\n") + "\n\n" + managed + "\n"
	}

	return os.WriteFile(path, []byte(result), 0644)
}

// ReadManagedSection returns the content between nanite markers, or empty string if none.
func ReadManagedSection(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	text := string(data)
	startIdx := strings.Index(text, managedStart)
	endIdx := strings.Index(text, managedEnd)

	if startIdx < 0 || endIdx < 0 || endIdx <= startIdx {
		return "", nil
	}

	section := text[startIdx+len(managedStart) : endIdx]
	// Strip the notice line
	section = strings.Replace(section, managedNotice, "", 1)
	return strings.TrimSpace(section), nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/chrispian/Projects-apps/nanite && go test ./internal/agent/ -run TestWriteManagedSection -v`
Expected: PASS (4 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/agent/managed_section.go internal/agent/managed_section_test.go
git commit -m "feat(agent): add managed section helper for CLI config files"
```

---

## Task 3: Config Override Cascade

**Files:**
- Create: `internal/agent/override/merge.go`
- Create: `internal/agent/override/merge_test.go`

- [ ] **Step 1: Write failing tests for merge logic**

```go
// internal/agent/override/merge_test.go
package override

import (
	"testing"
)

func TestResolve_ScalarLastWriterWins(t *testing.T) {
	base := OverrideConfig{Model: "claude-sonnet"}
	project := OverrideConfig{} // no override
	session := OverrideConfig{Model: "claude-opus"}

	result := Resolve(base, &project, &session)
	if result.Model != "claude-opus" {
		t.Errorf("expected claude-opus, got %s", result.Model)
	}
}

func TestResolve_ScalarZeroValueSkipped(t *testing.T) {
	base := OverrideConfig{Model: "claude-sonnet", Provider: "anthropic"}
	project := OverrideConfig{Model: "claude-opus"} // Provider not set
	session := OverrideConfig{}                      // nothing set

	result := Resolve(base, &project, &session)
	if result.Model != "claude-opus" {
		t.Errorf("expected claude-opus, got %s", result.Model)
	}
	if result.Provider != "anthropic" {
		t.Errorf("expected anthropic preserved, got %s", result.Provider)
	}
}

func TestResolve_ListUnion(t *testing.T) {
	base := OverrideConfig{Tools: []string{"search", "code_exec"}}
	project := OverrideConfig{Tools: []string{"+database"}}
	session := OverrideConfig{}

	result := Resolve(base, &project, &session)
	expected := map[string]bool{"search": true, "code_exec": true, "database": true}
	if len(result.Tools) != len(expected) {
		t.Fatalf("expected %d tools, got %d: %v", len(expected), len(result.Tools), result.Tools)
	}
	for _, tool := range result.Tools {
		if !expected[tool] {
			t.Errorf("unexpected tool: %s", tool)
		}
	}
}

func TestResolve_ListRemoveWithPrefix(t *testing.T) {
	base := OverrideConfig{Tools: []string{"search", "code_exec", "deploy"}}
	project := OverrideConfig{Tools: []string{"+database"}}
	session := OverrideConfig{Tools: []string{"-code_exec", "+monitor"}}

	result := Resolve(base, &project, &session)
	expected := map[string]bool{"search": true, "deploy": true, "database": true, "monitor": true}
	if len(result.Tools) != len(expected) {
		t.Fatalf("expected %d tools, got %d: %v", len(expected), len(result.Tools), result.Tools)
	}
	for _, tool := range result.Tools {
		if !expected[tool] {
			t.Errorf("unexpected tool: %s", tool)
		}
	}
}

func TestResolve_MapDeepMerge(t *testing.T) {
	base := OverrideConfig{
		Constraints: map[string]interface{}{
			"maxTurns":       float64(25),
			"maxTimeSeconds": float64(3600),
		},
	}
	session := OverrideConfig{
		Constraints: map[string]interface{}{
			"maxTurns": float64(50),
		},
	}

	result := Resolve(base, nil, &session)
	if result.Constraints["maxTurns"] != float64(50) {
		t.Errorf("expected maxTurns=50, got %v", result.Constraints["maxTurns"])
	}
	if result.Constraints["maxTimeSeconds"] != float64(3600) {
		t.Errorf("expected maxTimeSeconds preserved, got %v", result.Constraints["maxTimeSeconds"])
	}
}

func TestResolve_WildcardThenSpecific(t *testing.T) {
	wildcard := OverrideConfig{Tools: []string{"+internal_docs"}}
	specific := OverrideConfig{Tools: []string{"+database"}}
	configs := map[string]OverrideConfig{
		"*":               wildcard,
		"file-researcher": specific,
	}

	base := OverrideConfig{Tools: []string{"search"}}
	result := ResolveWithMap(base, configs, "file-researcher", nil)

	expected := map[string]bool{"search": true, "internal_docs": true, "database": true}
	if len(result.Tools) != len(expected) {
		t.Fatalf("expected %d tools, got %d: %v", len(expected), len(result.Tools), result.Tools)
	}
}

func TestResolve_NilLayersSkipped(t *testing.T) {
	base := OverrideConfig{Model: "claude-sonnet", Tools: []string{"search"}}
	result := Resolve(base, nil, nil)
	if result.Model != "claude-sonnet" {
		t.Errorf("expected base preserved, got %s", result.Model)
	}
	if len(result.Tools) != 1 || result.Tools[0] != "search" {
		t.Errorf("expected base tools preserved, got %v", result.Tools)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/chrispian/Projects-apps/nanite && go test ./internal/agent/override/ -v`
Expected: FAIL — package doesn't exist.

- [ ] **Step 3: Implement merge logic**

```go
// internal/agent/override/merge.go
package override

import "strings"

// OverrideConfig represents a layer in the override cascade.
// Zero-value fields are skipped during merge.
type OverrideConfig struct {
	Model       string                 `yaml:"model,omitempty"       json:"model,omitempty"`
	Provider    string                 `yaml:"provider,omitempty"    json:"provider,omitempty"`
	Description string                 `yaml:"description,omitempty" json:"description,omitempty"`
	Tools       []string               `yaml:"tools,omitempty"       json:"tools,omitempty"`
	Skills      []string               `yaml:"skills,omitempty"      json:"skills,omitempty"`
	MCPServers  []string               `yaml:"mcpServers,omitempty"  json:"mcpServers,omitempty"`
	Tags        []string               `yaml:"tags,omitempty"        json:"tags,omitempty"`
	Directories []string               `yaml:"directories,omitempty" json:"directories,omitempty"`
	Permissions map[string]interface{} `yaml:"permissions,omitempty" json:"permissions,omitempty"`
	Settings    map[string]interface{} `yaml:"settings,omitempty"    json:"settings,omitempty"`
	Constraints map[string]interface{} `yaml:"constraints,omitempty" json:"constraints,omitempty"`
}

// Resolve computes the effective config by applying layers: base → project → session.
// Nil layers are skipped. Scalars are last-writer-wins. Lists are union with +/- prefix.
// Maps are deep-merged.
func Resolve(base OverrideConfig, project, session *OverrideConfig) OverrideConfig {
	result := base
	if project != nil {
		result = mergeLayer(result, *project)
	}
	if session != nil {
		result = mergeLayer(result, *session)
	}
	return result
}

// ResolveWithMap resolves using a map of named overrides (supports "*" wildcard).
// Applies wildcard first, then agent-specific, then session.
func ResolveWithMap(base OverrideConfig, overrides map[string]OverrideConfig, agentKey string, session *OverrideConfig) OverrideConfig {
	result := base
	if wc, ok := overrides["*"]; ok {
		result = mergeLayer(result, wc)
	}
	if agentKey != "*" {
		if specific, ok := overrides[agentKey]; ok {
			result = mergeLayer(result, specific)
		}
	}
	if session != nil {
		result = mergeLayer(result, *session)
	}
	return result
}

func mergeLayer(base, layer OverrideConfig) OverrideConfig {
	// Scalars: last-writer-wins (non-empty only)
	if layer.Model != "" {
		base.Model = layer.Model
	}
	if layer.Provider != "" {
		base.Provider = layer.Provider
	}
	if layer.Description != "" {
		base.Description = layer.Description
	}

	// Lists: union with +/- prefix
	base.Tools = mergeLists(base.Tools, layer.Tools)
	base.Skills = mergeLists(base.Skills, layer.Skills)
	base.MCPServers = mergeLists(base.MCPServers, layer.MCPServers)
	base.Tags = mergeLists(base.Tags, layer.Tags)
	base.Directories = mergeLists(base.Directories, layer.Directories)

	// Maps: deep merge
	base.Permissions = mergeMaps(base.Permissions, layer.Permissions)
	base.Settings = mergeMaps(base.Settings, layer.Settings)
	base.Constraints = mergeMaps(base.Constraints, layer.Constraints)

	return base
}

// mergeLists performs union merge with +/- prefix support.
// Items without prefix or with "+" are added. Items with "-" are removed.
func mergeLists(base, overlay []string) []string {
	if len(overlay) == 0 {
		return base
	}

	set := make(map[string]bool)
	for _, item := range base {
		set[item] = true
	}

	for _, item := range overlay {
		if strings.HasPrefix(item, "-") {
			delete(set, item[1:])
		} else if strings.HasPrefix(item, "+") {
			set[item[1:]] = true
		} else {
			set[item] = true
		}
	}

	result := make([]string, 0, len(set))
	for item := range set {
		result = append(result, item)
	}
	return result
}

// mergeMaps performs a shallow deep-merge. Nested scalars are last-writer-wins.
func mergeMaps(base, overlay map[string]interface{}) map[string]interface{} {
	if len(overlay) == 0 {
		return base
	}
	if base == nil {
		base = make(map[string]interface{})
	}
	for k, v := range overlay {
		base[k] = v
	}
	return base
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/chrispian/Projects-apps/nanite && go test ./internal/agent/override/ -v`
Expected: PASS (7 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/agent/override/merge.go internal/agent/override/merge_test.go
git commit -m "feat(agent): add config override cascade with per-field merge semantics"
```

---

## Task 4: Messaging Absorption (SQLite)

**Files:**
- Create: `internal/store/migrations/004_messaging_and_overrides.sql`
- Create: `internal/store/messaging.go`
- Create: `internal/store/messaging_test.go`

- [ ] **Step 1: Write migration**

```sql
-- internal/store/migrations/004_messaging_and_overrides.sql

-- Agent messaging (replaces Nexus messaging)
CREATE TABLE IF NOT EXISTS messages (
    id          TEXT PRIMARY KEY,
    from_agent  TEXT NOT NULL,
    to_agent    TEXT NOT NULL,
    thread_id   TEXT,
    type        TEXT NOT NULL DEFAULT 'message',
    status      TEXT NOT NULL DEFAULT 'unread',
    subject     TEXT,
    body        TEXT,
    metadata    TEXT,
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_messages_to_agent ON messages(to_agent, status);
CREATE INDEX IF NOT EXISTS idx_messages_thread ON messages(thread_id);
CREATE INDEX IF NOT EXISTS idx_messages_from_agent ON messages(from_agent);

-- Session agent overrides (for config cascade)
CREATE TABLE IF NOT EXISTS session_agent_overrides (
    session_id TEXT NOT NULL,
    overrides  TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (session_id)
);
```

- [ ] **Step 2: Write failing tests for MessageStore**

```go
// internal/store/messaging_test.go
package store

import (
	"context"
	"testing"
)

func TestMessageStore_SendAndInbox(t *testing.T) {
	db := newTestDB(t) // assumes existing test helper pattern
	ctx := context.Background()

	msg, err := db.SendMessage(ctx, SendMessageInput{
		FromAgent: "agent-a",
		ToAgent:   "agent-b",
		Type:      "message",
		Subject:   "Hello",
		Body:      "Test message body",
	})
	if err != nil {
		t.Fatal(err)
	}
	if msg.ID == "" {
		t.Error("expected generated ID")
	}
	if msg.Status != "unread" {
		t.Errorf("expected unread status, got %s", msg.Status)
	}

	inbox, err := db.MessageInbox(ctx, "agent-b", InboxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(inbox) != 1 {
		t.Fatalf("expected 1 message in inbox, got %d", len(inbox))
	}
	if inbox[0].Subject != "Hello" {
		t.Errorf("expected Hello, got %s", inbox[0].Subject)
	}
}

func TestMessageStore_AckAndResolve(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	msg, _ := db.SendMessage(ctx, SendMessageInput{
		FromAgent: "agent-a",
		ToAgent:   "agent-b",
		Type:      "message",
		Body:      "test",
	})

	if err := db.AckMessage(ctx, msg.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := db.GetMessage(ctx, msg.ID)
	if got.Status != "read" {
		t.Errorf("expected read after ack, got %s", got.Status)
	}

	if err := db.ResolveMessage(ctx, msg.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = db.GetMessage(ctx, msg.ID)
	if got.Status != "resolved" {
		t.Errorf("expected resolved, got %s", got.Status)
	}
}

func TestMessageStore_Thread(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	threadID := "thread-123"
	db.SendMessage(ctx, SendMessageInput{
		FromAgent: "a", ToAgent: "b", Body: "msg1", ThreadID: threadID,
	})
	db.SendMessage(ctx, SendMessageInput{
		FromAgent: "b", ToAgent: "a", Body: "msg2", ThreadID: threadID,
	})
	db.SendMessage(ctx, SendMessageInput{
		FromAgent: "a", ToAgent: "b", Body: "unrelated",
	})

	thread, err := db.MessageThread(ctx, threadID)
	if err != nil {
		t.Fatal(err)
	}
	if len(thread) != 2 {
		t.Fatalf("expected 2 messages in thread, got %d", len(thread))
	}
}

func TestMessageStore_UnreadCount(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	db.SendMessage(ctx, SendMessageInput{FromAgent: "a", ToAgent: "b", Body: "1"})
	db.SendMessage(ctx, SendMessageInput{FromAgent: "a", ToAgent: "b", Body: "2"})
	db.SendMessage(ctx, SendMessageInput{FromAgent: "a", ToAgent: "c", Body: "3"})

	count, err := db.MessageUnreadCount(ctx, "b")
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("expected 2 unread for b, got %d", count)
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `cd /Users/chrispian/Projects-apps/nanite && go test ./internal/store/ -run TestMessageStore -v`
Expected: FAIL — types and methods not defined.

- [ ] **Step 4: Implement MessageStore**

```go
// internal/store/messaging.go
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Message represents an agent-to-agent message.
type Message struct {
	ID        string `json:"id"`
	FromAgent string `json:"from_agent"`
	ToAgent   string `json:"to_agent"`
	ThreadID  string `json:"thread_id,omitempty"`
	Type      string `json:"type"`
	Status    string `json:"status"`
	Subject   string `json:"subject,omitempty"`
	Body      string `json:"body"`
	Metadata  string `json:"metadata,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// SendMessageInput is the input for creating a new message.
type SendMessageInput struct {
	FromAgent string `json:"from_agent"`
	ToAgent   string `json:"to_agent"`
	ThreadID  string `json:"thread_id,omitempty"`
	Type      string `json:"type"`
	Subject   string `json:"subject,omitempty"`
	Body      string `json:"body"`
	Metadata  string `json:"metadata,omitempty"`
}

// InboxOptions filters inbox queries.
type InboxOptions struct {
	Status string // filter by status; empty = all
	Limit  int    // 0 = no limit
}

// SendMessage creates a new message.
func (db *DB) SendMessage(ctx context.Context, input SendMessageInput) (Message, error) {
	id := uuid.New().String()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	msgType := input.Type
	if msgType == "" {
		msgType = "message"
	}

	_, err := db.sql.ExecContext(ctx,
		`INSERT INTO messages (id, from_agent, to_agent, thread_id, type, status, subject, body, metadata, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, 'unread', ?, ?, ?, ?, ?)`,
		id, input.FromAgent, input.ToAgent, input.ThreadID, msgType,
		input.Subject, input.Body, input.Metadata, now, now,
	)
	if err != nil {
		return Message{}, fmt.Errorf("send message: %w", err)
	}

	return Message{
		ID: id, FromAgent: input.FromAgent, ToAgent: input.ToAgent,
		ThreadID: input.ThreadID, Type: msgType, Status: "unread",
		Subject: input.Subject, Body: input.Body, Metadata: input.Metadata,
		CreatedAt: now, UpdatedAt: now,
	}, nil
}

// MessageInbox returns messages sent to an agent.
func (db *DB) MessageInbox(ctx context.Context, agentID string, opts InboxOptions) ([]Message, error) {
	query := `SELECT id, from_agent, to_agent, thread_id, type, status, subject, body, metadata, created_at, updated_at
		FROM messages WHERE to_agent = ?`
	args := []interface{}{agentID}

	if opts.Status != "" {
		query += " AND status = ?"
		args = append(args, opts.Status)
	}
	query += " ORDER BY created_at DESC"
	if opts.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", opts.Limit)
	}

	return db.scanMessages(ctx, query, args...)
}

// MessageThread returns all messages in a thread ordered by creation time.
func (db *DB) MessageThread(ctx context.Context, threadID string) ([]Message, error) {
	return db.scanMessages(ctx,
		`SELECT id, from_agent, to_agent, thread_id, type, status, subject, body, metadata, created_at, updated_at
		 FROM messages WHERE thread_id = ? ORDER BY created_at ASC`, threadID)
}

// GetMessage returns a single message by ID.
func (db *DB) GetMessage(ctx context.Context, id string) (Message, error) {
	msgs, err := db.scanMessages(ctx,
		`SELECT id, from_agent, to_agent, thread_id, type, status, subject, body, metadata, created_at, updated_at
		 FROM messages WHERE id = ?`, id)
	if err != nil {
		return Message{}, err
	}
	if len(msgs) == 0 {
		return Message{}, fmt.Errorf("message not found: %s", id)
	}
	return msgs[0], nil
}

// AckMessage marks a message as read.
func (db *DB) AckMessage(ctx context.Context, id string) error {
	return db.updateMessageStatus(ctx, id, "read")
}

// ResolveMessage marks a message as resolved.
func (db *DB) ResolveMessage(ctx context.Context, id string) error {
	return db.updateMessageStatus(ctx, id, "resolved")
}

// MessageUnreadCount returns the number of unread messages for an agent.
func (db *DB) MessageUnreadCount(ctx context.Context, agentID string) (int, error) {
	var count int
	err := db.sql.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM messages WHERE to_agent = ? AND status = 'unread'`,
		agentID,
	).Scan(&count)
	return count, err
}

func (db *DB) updateMessageStatus(ctx context.Context, id, status string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := db.sql.ExecContext(ctx,
		`UPDATE messages SET status = ?, updated_at = ? WHERE id = ?`,
		status, now, id,
	)
	if err != nil {
		return fmt.Errorf("update message status: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("message not found: %s", id)
	}
	return nil
}

func (db *DB) scanMessages(ctx context.Context, query string, args ...interface{}) ([]Message, error) {
	rows, err := db.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query messages: %w", err)
	}
	defer rows.Close()

	var msgs []Message
	for rows.Next() {
		var m Message
		var threadID, subject, metadata *string
		if err := rows.Scan(&m.ID, &m.FromAgent, &m.ToAgent, &threadID, &m.Type,
			&m.Status, &subject, &m.Body, &metadata, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		if threadID != nil {
			m.ThreadID = *threadID
		}
		if subject != nil {
			m.Subject = *subject
		}
		if metadata != nil {
			m.Metadata = *metadata
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /Users/chrispian/Projects-apps/nanite && go test ./internal/store/ -run TestMessageStore -v`
Expected: PASS (4 tests)

- [ ] **Step 6: Commit**

```bash
git add internal/store/migrations/004_messaging_and_overrides.sql internal/store/messaging.go internal/store/messaging_test.go
git commit -m "feat(store): add SQLite messaging store, replacing Nexus dependency"
```

---

## Task 5: Nanite-Native Adapter (replaces agentrc-sync)

**Files:**
- Create: `internal/plugin/builtin/adapter-nanite-native/plugin.go`
- Delete: `internal/plugin/builtin/agentrc/plugin.go`

- [ ] **Step 1: Write the nanite-native adapter plugin**

This is a refactor of the existing `agentrc/plugin.go` (244 lines). The core logic (read config, resolve roles, compose prompts) stays the same but is restructured to implement `CLIAgentAdapter` + `AgentComposer`.

Read the existing `internal/plugin/builtin/agentrc/plugin.go` for the current implementation. The new plugin:

1. Moves from `agentrc` package to `nanitenative` package
2. Implements `agent.CLIAgentAdapter` interface (Name, Discover, PopulateSandbox, SyncProjectRoot, Priority)
3. Implements `agent.AgentComposer` interface (ComposePrompt, ListRoles, ListSkills)
4. Reads from `.nanite/config.yaml` (primary) with `.agentrc/config.yaml` fallback + deprecation warning
5. Reads global config from `~/.nanite/config.yaml` with `~/.agentrc/config.yaml` fallback
6. `Source` field set to `"nanite"` instead of `"agentrc"`
7. Plugin registration name: `"adapter-nanite-native"`

Key implementation notes:
- `Discover()` returns `[]agent.Definition` instead of directly upserting to DB. The registry handles dedup and the service layer handles persistence.
- `PopulateSandbox()` writes `.nanite/` structure (config subset, role pointers) into sandbox dir.
- `SyncProjectRoot()` is a no-op — nanite-native manages its own files.
- `ComposePrompt()` extracts the role-resolution + context-append logic from the old `composeSystemPrompt()`.
- `ListRoles()` reads `~/.nanite/roles/` (fallback `~/.agentrc/roles/`), returns `[]agent.RoleInfo`.
- `ListSkills()` reads `~/.nanite/skills/` (fallback `~/.agentrc/skills/`), returns `[]agent.SkillInfo`.

- [ ] **Step 2: Run build to verify compilation**

Run: `cd /Users/chrispian/Projects-apps/nanite && go build ./...`
Expected: PASS

- [ ] **Step 3: Delete old agentrc plugin**

Remove `internal/plugin/builtin/agentrc/plugin.go` and its directory.

- [ ] **Step 4: Update plugin registration in any init() or loader that references "agentrc-sync"**

Search for references to `"agentrc-sync"` or `agentrc.New()` in the codebase and update to `"adapter-nanite-native"` / `nanitenative.New()`.

- [ ] **Step 5: Run full test suite**

Run: `cd /Users/chrispian/Projects-apps/nanite && go test ./...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/plugin/builtin/adapter-nanite-native/plugin.go
git rm -r internal/plugin/builtin/agentrc/
git add -u  # catch any import updates
git commit -m "feat(agent): replace agentrc-sync with nanite-native adapter plugin

Implements CLIAgentAdapter + AgentComposer interfaces. Reads from
.nanite/ with .agentrc/ fallback and deprecation warning."
```

---

## Task 6: Claude Code Adapter

**Files:**
- Create: `internal/plugin/builtin/adapter-claude/plugin.go`
- Modify: `internal/sandbox/sandbox.go` — extract content generation, delegate to adapter

- [ ] **Step 1: Write the Claude adapter plugin**

Absorbs the content-generation logic from `internal/sandbox/sandbox.go` (389 lines). The adapter:

1. `Discover()` — reads `.claude/agents/*.md`, parses with `agent.ParseMDFile()`, sets `Source: "claude"`.
2. `PopulateSandbox()` — writes `CLAUDE.md`, `.mcp.json`, `.sandbox/envelope-schema.md`, `.sandbox/agent-context.md`. Move the `buildCLAUDEMD()`, `writeMCPJSON()`, `buildAgentContext()` logic from `sandbox.go` into this adapter.
3. `SyncProjectRoot()` — calls `agent.WriteManagedSection()` on the project-root `CLAUDE.md` with a summary of Nanite agent context and tool definitions.
4. `Priority()` returns 60.

- [ ] **Step 2: Refactor sandbox.go to delegate to AdapterRegistry**

`sandbox.Populate()` currently writes all files directly. Change it to:
1. Keep `Dir()` and directory lifecycle management.
2. Replace the direct file writes with a call to `AdapterRegistry.PopulateAllSandboxes()`.
3. The adapter registry is passed in via `PopulateOpts` (add an `Adapters *agent.AdapterRegistry` field).

- [ ] **Step 3: Run build to verify compilation**

Run: `cd /Users/chrispian/Projects-apps/nanite && go build ./...`
Expected: PASS

- [ ] **Step 4: Run full test suite**

Run: `cd /Users/chrispian/Projects-apps/nanite && go test ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/plugin/builtin/adapter-claude/plugin.go internal/sandbox/sandbox.go
git commit -m "feat(agent): add Claude Code adapter, delegate sandbox writing to adapters"
```

---

## Task 7: Codex, Gemini, and Opencode Adapters

**Files:**
- Create: `internal/plugin/builtin/adapter-codex/plugin.go`
- Create: `internal/plugin/builtin/adapter-gemini/plugin.go`
- Create: `internal/plugin/builtin/adapter-opencode/plugin.go`

- [ ] **Step 1: Write the Codex/Copilot adapter**

1. `Discover()` — reads `AGENTS.md` from project root. Parses as markdown (may have YAML frontmatter). Returns `[]agent.Definition` with `Source: "codex"`.
2. `PopulateSandbox()` — writes `AGENTS.md` into sandbox with agent identity, tools, constraints formatted for OpenAI/Copilot CLI conventions.
3. `SyncProjectRoot()` — calls `agent.WriteManagedSection()` on project-root `AGENTS.md`.
4. `Priority()` returns 70.

- [ ] **Step 2: Write the Gemini adapter**

Same pattern as Codex but targets `GEMINI.md`. `Source: "gemini"`. Priority 70.

- [ ] **Step 3: Write the Opencode adapter**

Same pattern targeting Opencode's config file. `Source: "opencode"`. Priority 70. Note in a comment that the exact file format should be confirmed — use `OPENCODE.md` as the default path.

- [ ] **Step 4: Run build to verify compilation**

Run: `cd /Users/chrispian/Projects-apps/nanite && go build ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/plugin/builtin/adapter-codex/ internal/plugin/builtin/adapter-gemini/ internal/plugin/builtin/adapter-opencode/
git commit -m "feat(agent): add Codex, Gemini, and Opencode CLI adapters"
```

---

## Task 8: Wire Adapters into Discovery + Service Layer

**Files:**
- Modify: `internal/agent/discovery.go` (127 lines)
- Modify: `internal/service/agent.go` (217 lines)
- Modify: `internal/service/container.go` (503 lines)

- [ ] **Step 1: Update discovery.go to use AdapterRegistry for tiers 5+**

Current `Discover()` has 6 hardcoded discovery locations. Change it to:
1. Keep tiers 1-4 (CLI flag, `.nanite/agents/`, `~/.nanite/agents/`, `plugins/*/agents/`) as native discovery.
2. Replace tiers 5-6 (`.agentrc/agents/`, `.claude/agents/`) with a call to the `AdapterRegistry.DiscoverAll()` passed via `DiscoverOptions`.
3. Deduplicate: native tiers take priority over adapter-discovered agents (same slug = native wins).
4. Update directory references from `.agentrc/` to `.nanite/` in tiers 1-4.

- [ ] **Step 2: Add override resolution to AgentService**

In `internal/service/agent.go`, update `ResolveForSession()` to:
1. After resolving the base agent profile, load project overrides from `.nanite/config.yaml` `agent_overrides` section.
2. Load session overrides from `session_agent_overrides` table.
3. Call `override.ResolveWithMap()` to compute effective config.
4. Apply effective config fields back to the `AgentProfile` before returning.

- [ ] **Step 3: Wire AdapterRegistry in container.go**

In `NewContainer()`:
1. Create `agent.NewAdapterRegistry()`.
2. Register all adapter plugins after plugin loading (adapters self-register via their `Load(host)` method — the host provides access to the registry).
3. Pass the registry to `AgentService` config and `sandbox.PopulateOpts`.

- [ ] **Step 4: Run full test suite**

Run: `cd /Users/chrispian/Projects-apps/nanite && go test ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agent/discovery.go internal/service/agent.go internal/service/container.go
git commit -m "feat(agent): wire adapter registry into discovery, service layer, and container"
```

---

## Task 9: Replace Nexus Messaging in API Layer

**Files:**
- Modify: `internal/api/a2a.go` (225 lines)
- Modify: `cmd/nanite/main.go` (585 lines)
- Modify: `go.mod`

- [ ] **Step 1: Update a2a.go to use store.MessageStore**

1. Remove `nexusMessageToA2A()` and `nexusMessagesToA2A()` conversion functions.
2. Replace all `a.NexusMsg.Xxx()` calls with `a.Store.Xxx()` calls using the new `MessageStore` methods.
3. Remove the Nexus fallback pattern — SQLite is now the only path.
4. Update handler signatures if needed (the JSON shapes stay the same).

- [ ] **Step 2: Remove Nexus initialization from main.go**

Remove lines ~389-409 in `cmd/nanite/main.go` that:
- Read `ENGINE_POSTGRES_DSN`
- Connect to PostgreSQL
- Create `messaging.NewPostgresStore(engineDB)`
- Assign to `a.NexusMsg`

Also remove the `NexusMsg` field from the API struct if it exists.

- [ ] **Step 3: Remove Nexus from go.mod**

```bash
cd /Users/chrispian/Projects-apps/nanite
go mod edit -droprequire github.com/hollis-labs/nexus
go mod edit -dropreplace github.com/hollis-labs/nexus
go mod edit -droprequire github.com/lib/pq
go mod tidy
```

- [ ] **Step 4: Run build + tests**

Run: `cd /Users/chrispian/Projects-apps/nanite && go build ./... && go test ./...`
Expected: PASS — no Nexus references remain.

- [ ] **Step 5: Verify no remaining Nexus references**

Run: `grep -r "nexus" internal/ cmd/ --include="*.go" -l`
Expected: No results (or only comments/docs).

- [ ] **Step 6: Commit**

```bash
git add -u
git commit -m "feat(messaging): absorb Nexus messaging into SQLite, drop Nexus dependency

Replaces PostgreSQL-backed nexus/messaging with native SQLite
MessageStore. Same API surface, no external database required."
```

---

## Task 10: Rename .agentrc References to .nanite

**Files:**
- Modify: Multiple files across `internal/`, `cmd/`, config files
- Rename: `.agentrc/` directory contents conceptually (actual project files handled by user)

- [ ] **Step 1: Find all .agentrc references in Go code**

Run: `grep -r "agentrc\|\.agentrc" internal/ cmd/ --include="*.go" -l`

Update each file:
- String literals `".agentrc"` → `".nanite"`
- Directory paths `~/.agentrc/` → `~/.nanite/`
- Source values `"agentrc"` → `"nanite"`
- Comments referencing agentrc → nanite

- [ ] **Step 2: Update CLAUDE.md references**

Update `CLAUDE.md` and `.claude/CLAUDE.md` to reference `.nanite/` instead of `.agentrc/`.

- [ ] **Step 3: Update config files**

If `.agentrc/config.yaml` exists in the repo, move its content to `.nanite/config.yaml`. Same for `.agentrc/agents/` → `.nanite/agents/` and `.agentrc/boot-prompt.md` → `.nanite/boot-prompt.md`.

- [ ] **Step 4: Run full build + tests**

Run: `cd /Users/chrispian/Projects-apps/nanite && go build ./... && go test ./...`
Expected: PASS

- [ ] **Step 5: Verify no remaining .agentrc references in Go code**

Run: `grep -r "\.agentrc" internal/ cmd/ --include="*.go"`
Expected: No results (except backward-compat fallback in nanite-native adapter).

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "refactor: rename .agentrc to .nanite across codebase

Updates all Go code, config references, and project files.
Nanite-native adapter retains .agentrc fallback with deprecation warning."
```

---

## Task 11: Integration Test + Final Verification

**Files:**
- No new files — verification pass across entire codebase

- [ ] **Step 1: Full build**

Run: `cd /Users/chrispian/Projects-apps/nanite && go build ./cmd/nanite/`
Expected: Clean build, no warnings.

- [ ] **Step 2: Full test suite**

Run: `cd /Users/chrispian/Projects-apps/nanite && go test ./...`
Expected: All tests pass.

- [ ] **Step 3: Vet check**

Run: `cd /Users/chrispian/Projects-apps/nanite && go vet ./...`
Expected: No issues.

- [ ] **Step 4: Verify adapter plugin registration**

Search for all `RegisterPlugin` calls to confirm the 5 adapter plugins are registered:
- `adapter-nanite-native`
- `adapter-claude`
- `adapter-codex`
- `adapter-gemini`
- `adapter-opencode`

- [ ] **Step 5: Verify Nexus is fully removed**

Run: `grep -r "nexus\|hollis-labs/nexus" go.mod go.sum internal/ cmd/ --include="*.go" -l`
Expected: No results.

- [ ] **Step 6: Commit any final fixes**

```bash
git add -u
git commit -m "chore: final verification pass for agent adapter architecture"
```
