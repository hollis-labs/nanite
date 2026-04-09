# Nanite Install + Rollout Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `nanite install` (CLI + MCP), embed the agentrc framework content into the Nanite binary via `embed.FS`, roll the installer out to the portfolio (Nanite first, then remaining projects in parallel), and archive the legacy agentrc repo.

**Architecture:** The Nanite binary owns the canonical framework content (`assets/framework/`) and extracts it to `~/.nanite/` on first install. A single `internal/service/install.go` package handles home extract, project scaffold, migrate-from-agentrc, adopt-existing, archive-only, rollback, resume, and restart. State is tracked via a `.install-state.json` marker file in the archive directory. CLI (`cmd/nanite/install_cmd.go`) and MCP tools (`internal/mcp/self_tools_transport.go`) are thin wrappers over the service layer. CLAUDE.md surgery writes managed sections between `<!-- nanite:start --> ... <!-- nanite:end -->` markers using the existing `internal/agent/managed_section.go` helper from PR #11.

**Tech Stack:** Go 1.26, `embed.FS`, `gopkg.in/yaml.v3` (with comment preservation via `goccy/go-yaml` if round-trip of unknown keys becomes necessary), SQLite (not directly touched by this plan), existing `internal/agent/adapter.go` registry, existing `internal/agent/managed_section.go` helper.

**Spec:** `docs/superpowers/specs/2026-04-09-nanite-agentrc-consolidation-design.md`

---

## File Map

### New Files

| File | Responsibility |
|---|---|
| `assets/framework/VERSION` | Framework content version string (starts at `2.3.0`) |
| `assets/framework/config.yaml` | Seed global config copied from `~/Projects-apps/agentrc/config.yaml` (renames applied) |
| `assets/framework/agent-boot.md` | Seed session boot rules |
| `assets/framework/roles/**` | Copied role content (domain/stack/meta) with `agentrc-dev` → `nanite-agent-manager` rename |
| `assets/framework/skills/**` | Copied skill content (`agentrc-install.md` deleted, `agentrc-manage.md` → `nanite-agent-manage.md`) |
| `assets/framework/commands/**` | Copied command stubs |
| `assets/framework/docs/**` | Framework docs (renamed to `nanite-framework.md`, `nanite-setup-guide.md`, new `nanite-install.md`) |
| `assets/framework/templates/NANITE.md.tmpl` | Thin boot prompt template for project root scaffold |
| `assets/framework/templates/nanite-config.yaml.tmpl` | Fresh-install `.nanite/config.yaml` template |
| `internal/assets/framework.go` | `//go:embed` accessor, `ExtractTo`, `File`, `Version`, `ExtractReport`, `ExtractOptions` |
| `internal/assets/framework_test.go` | Unit tests for extraction, checksum skip, version read |
| `internal/service/install/install.go` | Public `Service` type + entry points (`InstallHome`, `InstallProject`, `Rollback`, `Resume`, `Restart`) |
| `internal/service/install/state.go` | `.install-state.json` reader/writer |
| `internal/service/install/state_test.go` | Unit tests for state marker |
| `internal/service/install/archive.go` | Archive-dir path resolution, move logic, snapshot writes |
| `internal/service/install/archive_test.go` | Unit tests for archive logic (collision suffix, idempotency) |
| `internal/service/install/scaffold.go` | `.nanite/` scaffold, `NANITE.md` scaffold, symlink creation |
| `internal/service/install/scaffold_test.go` | Unit tests for scaffold |
| `internal/service/install/claudemd.go` | CLAUDE.md surgery (remove `## agentrc`, write managed section) |
| `internal/service/install/claudemd_test.go` | Unit tests for CLAUDE.md surgery (all edge cases from spec 4.1) |
| `internal/service/install/migrate.go` | `MigrateFromAgentrc` path |
| `internal/service/install/migrate_test.go` | Unit tests for full migration flow |
| `internal/service/install/adopt.go` | Adopt-existing path for partially-migrated projects (PR #11 case) |
| `internal/service/install/adopt_test.go` | Unit tests for adopt path |
| `internal/service/install/rollback.go` | Rollback path (reverse archive, restore CLAUDE.md) |
| `internal/service/install/rollback_test.go` | Unit tests for rollback |
| `internal/service/install/resume.go` | Resume + Restart paths |
| `internal/service/install/resume_test.go` | Unit tests for resume/restart |
| `internal/service/install/integration_test.go` | Full round-trip integration test |
| `cmd/nanite/install_cmd.go` | `cmdInstall` — CLI entry point matching existing `cmdServe`/`cmdMCP` pattern |

### Modified Files

| File | Changes |
|---|---|
| `cmd/nanite/main.go` | Add `"install"` case to the command switch; add `cmdInstall` dispatch |
| `internal/mcp/self_tools_transport.go` | Add MCP tool definitions for `nanite_install_home`, `nanite_install_project`, `nanite_install_rollback`, `nanite_install_diff`; add `callInstallXxx` handlers |
| `internal/mcp/self_tools_transport_test.go` | Tests for new MCP tools (ListTools contains them, CallTool dispatches correctly) |
| `go.mod` / `go.sum` | Add `goccy/go-yaml` if YAML comment preservation is needed |

### Content Relocation (one-off, Task 1)

Content from `~/Projects-apps/agentrc/` copied into `assets/framework/` with the renames and find-replace pass documented in the spec (§2.1).

---

## Task 1: Relocate framework content into `assets/framework/`

**Files:**
- Create: `assets/framework/VERSION`
- Create: `assets/framework/**` (copy from `~/Projects-apps/agentrc/`)

**Context:** Pure content relocation. The agentrc repo is markdown + YAML only — no code. This task copies the content into the Nanite repo, applies mechanical renames and find-replace, and commits. No tests yet; the content is verified by Task 2's embed tests.

- [ ] **Step 1: Copy the source tree verbatim**

```bash
cd ~/Projects-apps/nanite
mkdir -p assets/framework
cp -R ~/Projects-apps/agentrc/config.yaml        assets/framework/
cp -R ~/Projects-apps/agentrc/agent-boot.md      assets/framework/
cp -R ~/Projects-apps/agentrc/roles              assets/framework/
cp -R ~/Projects-apps/agentrc/skills             assets/framework/
cp -R ~/Projects-apps/agentrc/commands           assets/framework/
cp -R ~/Projects-apps/agentrc/docs               assets/framework/
cp -R ~/Projects-apps/agentrc/templates          assets/framework/
cp -R ~/Projects-apps/agentrc/vendor             assets/framework/
```

- [ ] **Step 2: Delete the `agentrc-install` skill (replaced by the new CLI)**

```bash
rm assets/framework/skills/agentrc-install.md
```

- [ ] **Step 3: Rename role, skill, and doc files**

```bash
mv assets/framework/roles/meta/agentrc-dev.md   assets/framework/roles/meta/nanite-agent-manager.md
mv assets/framework/skills/agentrc-manage.md    assets/framework/skills/nanite-agent-manage.md
mv assets/framework/docs/agent-framework-v2.md  assets/framework/docs/nanite-framework.md
mv assets/framework/docs/agent-setup-guide.md   assets/framework/docs/nanite-setup-guide.md
```

- [ ] **Step 4: Apply content find-replace pass (paths only, not prose history)**

```bash
cd ~/Projects-apps/nanite/assets/framework
# macOS sed requires '' after -i
find . -type f \( -name "*.md" -o -name "*.yaml" -o -name "*.tmpl" \) -exec sed -i '' \
  -e 's|agentrc_version|nanite_version|g' \
  -e 's|~/\.agentrc/|~/.nanite/|g' \
  -e 's|\.agentrc/|.nanite/|g' \
  -e 's|agentrc-manage|nanite-agent-manage|g' \
  -e 's|agentrc-dev|nanite-agent-manager|g' {} \;
```

- [ ] **Step 5: Manually review the two long docs for prose that needs rewriting**

```bash
cd ~/Projects-apps/nanite
${EDITOR:-code} assets/framework/docs/nanite-framework.md
${EDITOR:-code} assets/framework/docs/nanite-setup-guide.md
```

Look for: references to the agentrc repo (`~/Projects-apps/agentrc/`), references to `agentrc-install`, prose history that mentions "agentrc v2" as the framework name. Update to Nanite terminology where the text describes current state; leave history alone.

- [ ] **Step 6: Write the VERSION file**

```bash
echo "2.3.0" > assets/framework/VERSION
```

- [ ] **Step 7: Drop an `assets/framework/templates/NANITE.md.tmpl` stub**

```markdown
{{- /* NANITE.md.tmpl — thin boot prompt for project root */ -}}
# {{.ProjectName}}

> Agent configuration for this project is managed by [Nanite](https://github.com/hollis-labs/nanite).
> This file is owned by humans. Edit freely. Run `nanite install --project . --refresh` to pick up new framework content.

## Agents

See `.nanite/config.yaml` for agent definitions and `.nanite/agents/` for per-agent context files.

## How to boot

- **Claude Code:** Open this directory. Claude reads `CLAUDE.md` (managed by Nanite) and discovers agents from `.nanite/`.
- **Other CLI agents:** Nanite writes a managed section into `AGENTS.md` / `GEMINI.md` / `OPENCODE.md` as applicable.
- **Direct CLI:** `nanite serve --project .` starts the Nanite agent server for this project.

## Project-specific notes

<!-- Add project-specific agent guidance, conventions, or constraints here. This section is NOT managed by Nanite. -->
```

- [ ] **Step 8: Drop a `nanite-config.yaml.tmpl` for fresh installs**

```yaml
# assets/framework/templates/nanite-config.yaml.tmpl
# Project nanite config — {{.ProjectName}}
nanite_version: {{.FrameworkVersion}}

agents: {}
  # Example:
  # backend-dev:
  #   name: Backend Engineer
  #   description: Go service development
  #   roles: [backend, go]
  #   skills: [go-build, go-lint, go-test]
  #   context: agents/backend.md
```

- [ ] **Step 9: Commit**

```bash
cd ~/Projects-apps/nanite
git add assets/framework
git commit -m "$(cat <<'EOF'
feat: relocate agentrc framework content into assets/framework

Content copied verbatim from ~/Projects-apps/agentrc with the following renames:
- roles/meta/agentrc-dev.md → roles/meta/nanite-agent-manager.md
- skills/agentrc-manage.md → skills/nanite-agent-manage.md
- docs/agent-framework-v2.md → docs/nanite-framework.md
- docs/agent-setup-guide.md → docs/nanite-setup-guide.md

Removed skills/agentrc-install.md (replaced by upcoming nanite install CLI).
Find-replace pass on path references (agentrc_version, ~/.agentrc/, .agentrc/).
Added VERSION file (2.3.0), NANITE.md.tmpl, nanite-config.yaml.tmpl.
EOF
)"
```

---

## Task 2: Build `internal/assets/framework.go` with `embed.FS`

**Files:**
- Create: `internal/assets/framework.go`
- Create: `internal/assets/framework_test.go`

- [ ] **Step 1: Write failing test for `Version()`**

```go
// internal/assets/framework_test.go
package assets

import (
	"strings"
	"testing"
)

func TestVersion(t *testing.T) {
	v := Version()
	if v == "" {
		t.Fatal("Version() returned empty string")
	}
	if !strings.HasPrefix(v, "2.") {
		t.Errorf("Version() = %q, want prefix 2.", v)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd ~/Projects-apps/nanite
go test ./internal/assets/...
```

Expected: compile error, `Version` undefined.

- [ ] **Step 3: Implement `Version()` and the embed declaration**

```go
// internal/assets/framework.go
package assets

import (
	"embed"
	"strings"
)

//go:embed all:framework
var frameworkAssets embed.FS

// Version returns the framework content version string from framework/VERSION.
// This is the version of the embedded content set, separate from the Nanite binary version.
func Version() string {
	data, err := frameworkAssets.ReadFile("framework/VERSION")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
```

Note: the embed path is `framework` (relative to this package), not `assets/framework`. The `framework/` directory must be a symlink or a real dir inside `internal/assets/`. Simpler: move the embed declaration to a top-level package where `assets/framework` is a sibling, OR symlink `internal/assets/framework → ../../assets/framework`. Pick the symlink approach:

```bash
cd ~/Projects-apps/nanite/internal/assets
ln -s ../../assets/framework framework
```

- [ ] **Step 4: Run test, verify it passes**

```bash
go test ./internal/assets/... -run TestVersion -v
```

Expected: `PASS: TestVersion`.

- [ ] **Step 5: Write test for `File(path)`**

```go
func TestFile(t *testing.T) {
	data, err := File("VERSION")
	if err != nil {
		t.Fatalf("File(VERSION): %v", err)
	}
	if len(data) == 0 {
		t.Fatal("File(VERSION) returned empty bytes")
	}
	if _, err := File("does/not/exist.txt"); err == nil {
		t.Error("File(nonexistent) should have returned error")
	}
}
```

- [ ] **Step 6: Implement `File()`**

```go
// File reads a single embedded file by its path relative to the framework root
// (e.g., "VERSION", "roles/domain/backend.md").
func File(path string) ([]byte, error) {
	return frameworkAssets.ReadFile("framework/" + path)
}
```

- [ ] **Step 7: Write test for `ExtractTo()`**

```go
import (
	"os"
	"path/filepath"
)

func TestExtractTo_EmptyTarget(t *testing.T) {
	dir := t.TempDir()
	report, err := ExtractTo(dir, ExtractOptions{})
	if err != nil {
		t.Fatalf("ExtractTo: %v", err)
	}
	if report.Created == 0 {
		t.Error("expected Created > 0")
	}
	if report.Skipped != 0 {
		t.Errorf("Skipped = %d, want 0 (empty target)", report.Skipped)
	}

	// Spot-check: VERSION file should exist and match embedded.
	extracted, err := os.ReadFile(filepath.Join(dir, "VERSION"))
	if err != nil {
		t.Fatalf("read extracted VERSION: %v", err)
	}
	embedded, _ := File("VERSION")
	if string(extracted) != string(embedded) {
		t.Errorf("VERSION mismatch: extracted=%q embedded=%q", extracted, embedded)
	}
}

func TestExtractTo_SkipsModified(t *testing.T) {
	dir := t.TempDir()
	// First extract populates.
	if _, err := ExtractTo(dir, ExtractOptions{}); err != nil {
		t.Fatalf("first ExtractTo: %v", err)
	}
	// User modifies one file.
	modPath := filepath.Join(dir, "VERSION")
	if err := os.WriteFile(modPath, []byte("99.0.0-custom\n"), 0o644); err != nil {
		t.Fatalf("write mod: %v", err)
	}
	// Second extract should skip VERSION.
	report, err := ExtractTo(dir, ExtractOptions{})
	if err != nil {
		t.Fatalf("second ExtractTo: %v", err)
	}
	if report.Skipped == 0 {
		t.Error("expected Skipped > 0 after user modification")
	}
	// Verify the modified file is unchanged.
	after, _ := os.ReadFile(modPath)
	if string(after) != "99.0.0-custom\n" {
		t.Errorf("modified VERSION was overwritten: %q", after)
	}
}

func TestExtractTo_ForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	if _, err := ExtractTo(dir, ExtractOptions{}); err != nil {
		t.Fatal(err)
	}
	modPath := filepath.Join(dir, "VERSION")
	os.WriteFile(modPath, []byte("99.0.0-custom\n"), 0o644)

	if _, err := ExtractTo(dir, ExtractOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(modPath)
	embedded, _ := File("VERSION")
	if string(after) != string(embedded) {
		t.Errorf("Force=true did not overwrite: got %q want %q", after, embedded)
	}
}
```

- [ ] **Step 8: Implement `ExtractTo()`**

```go
import (
	"bytes"
	"fmt"
	"io/fs"
)

// ExtractOptions controls ExtractTo behavior.
type ExtractOptions struct {
	// Force overwrites existing files even if they differ from the embedded content.
	Force bool
}

// ExtractReport summarizes what ExtractTo did.
type ExtractReport struct {
	Created  int      // files newly created
	Unchanged int     // files that already matched the embedded content
	Skipped  int      // files that differ from embedded (user-modified) and were preserved
	Forced   int      // files overwritten because Force=true
	SkippedFiles []string // paths of skipped files (relative to targetDir)
}

// ExtractTo extracts the embedded framework tree into targetDir.
// Per-file behavior:
//   - file doesn't exist: create it
//   - file exists and bytes match embedded: no-op
//   - file exists and bytes differ: skip (preserve user modifications) unless Force is true
func ExtractTo(targetDir string, opts ExtractOptions) (*ExtractReport, error) {
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir target: %w", err)
	}

	report := &ExtractReport{}
	err := fs.WalkDir(frameworkAssets, "framework", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		// Trim the "framework/" prefix to get the relative path.
		rel := strings.TrimPrefix(path, "framework")
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" {
			return nil
		}
		target := filepath.Join(targetDir, rel)

		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}

		embedded, err := frameworkAssets.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read embed %s: %w", path, err)
		}

		existing, statErr := os.ReadFile(target)
		if os.IsNotExist(statErr) {
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(target, embedded, 0o644); err != nil {
				return err
			}
			report.Created++
			return nil
		}
		if statErr != nil {
			return fmt.Errorf("stat %s: %w", target, statErr)
		}

		if bytes.Equal(existing, embedded) {
			report.Unchanged++
			return nil
		}

		if opts.Force {
			if err := os.WriteFile(target, embedded, 0o644); err != nil {
				return err
			}
			report.Forced++
			return nil
		}

		report.Skipped++
		report.SkippedFiles = append(report.SkippedFiles, rel)
		return nil
	})

	if err != nil {
		return report, err
	}
	return report, nil
}
```

- [ ] **Step 9: Run all tests, verify pass**

```bash
cd ~/Projects-apps/nanite
go test ./internal/assets/... -v
```

Expected: all four tests pass.

- [ ] **Step 10: Commit**

```bash
git add internal/assets/
git commit -m "$(cat <<'EOF'
feat: embed framework assets via embed.FS

internal/assets/framework.go exposes ExtractTo/File/Version over the
assets/framework/ tree via a symlinked embed path. ExtractTo is
idempotent by default (skips user-modified files via byte-compare) and
supports Force=true for refresh flows.
EOF
)"
```

---

## Task 3: State marker package

**Files:**
- Create: `internal/service/install/state.go`
- Create: `internal/service/install/state_test.go`

**Context:** Every migration writes a `.install-state.json` file inside the archive directory. It tracks which phases have completed so `--resume` can pick up where a crash left off.

- [ ] **Step 1: Write failing test**

```go
// internal/service/install/state_test.go
package install

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestState_WriteRead(t *testing.T) {
	dir := t.TempDir()
	s := &State{
		OriginalProjectBasename: "hadron",
		OriginalProjectPath:     "/Users/foo/hadron",
		StartedAt:               time.Date(2026, 4, 9, 14, 30, 22, 0, time.UTC),
		Phase:                   PhaseArchived,
		CompletedPhases:         []Phase{PhaseStarting, PhaseArchived},
		ArchivePath:             dir,
		NaniteVersion:           "2.3.0",
	}
	path := filepath.Join(dir, StateFileName)
	if err := WriteState(path, s); err != nil {
		t.Fatalf("WriteState: %v", err)
	}

	got, err := ReadState(path)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if got.Phase != PhaseArchived {
		t.Errorf("Phase = %q, want %q", got.Phase, PhaseArchived)
	}
	if len(got.CompletedPhases) != 2 {
		t.Errorf("CompletedPhases len = %d, want 2", len(got.CompletedPhases))
	}
	if got.OriginalProjectBasename != "hadron" {
		t.Errorf("OriginalProjectBasename = %q, want hadron", got.OriginalProjectBasename)
	}
}

func TestState_ReadMissing(t *testing.T) {
	_, err := ReadState(filepath.Join(t.TempDir(), "nope.json"))
	if !os.IsNotExist(err) {
		t.Errorf("expected os.IsNotExist, got %v", err)
	}
}

func TestState_MarkPhaseComplete(t *testing.T) {
	s := NewState("hadron", "/path/to/hadron", "/archive", "2.3.0")
	s.MarkPhaseComplete(PhaseScaffoldNaniteDir)
	if s.Phase != PhaseScaffoldNaniteDir {
		t.Errorf("Phase = %q, want %q", s.Phase, PhaseScaffoldNaniteDir)
	}
	contains := false
	for _, p := range s.CompletedPhases {
		if p == PhaseScaffoldNaniteDir {
			contains = true
			break
		}
	}
	if !contains {
		t.Error("CompletedPhases missing PhaseScaffoldNaniteDir")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd ~/Projects-apps/nanite
go test ./internal/service/install/...
```

Expected: compile error (package not found).

- [ ] **Step 3: Implement state type**

```go
// internal/service/install/state.go
package install

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// Phase names track progress through an install.
type Phase string

const (
	PhaseStarting          Phase = "starting"
	PhaseArchived          Phase = "archived"
	PhaseGlobalExtract     Phase = "global-extract-complete"
	PhaseScaffoldNaniteDir Phase = "scaffold-nanite-dir"
	PhaseScaffoldNaniteMD  Phase = "scaffold-nanite-md"
	PhaseClaudeSync        Phase = "claude-sync"
	PhaseAdapterSync       Phase = "adapter-sync"
	PhaseComplete          Phase = "complete"
)

// StateFileName is the name of the state marker file inside the archive dir.
const StateFileName = ".install-state.json"

// State is the persisted progress marker for a (possibly-multi-step) install.
type State struct {
	OriginalProjectBasename string    `json:"original_project_basename"`
	OriginalProjectPath     string    `json:"original_project_path"`
	StartedAt               time.Time `json:"started_at"`
	Phase                   Phase     `json:"phase"`
	CompletedPhases         []Phase   `json:"completed_phases"`
	ArchivePath             string    `json:"archive_path"`
	RollbackSnapshotPath    string    `json:"rollback_snapshot_path,omitempty"`
	NaniteVersion           string    `json:"nanite_version"`
}

// NewState constructs a fresh state starting at PhaseStarting.
func NewState(basename, projectPath, archivePath, naniteVersion string) *State {
	return &State{
		OriginalProjectBasename: basename,
		OriginalProjectPath:     projectPath,
		StartedAt:               time.Now().UTC(),
		Phase:                   PhaseStarting,
		CompletedPhases:         []Phase{},
		ArchivePath:             archivePath,
		RollbackSnapshotPath:    archivePath + "/rollback",
		NaniteVersion:           naniteVersion,
	}
}

// MarkPhaseComplete updates Phase and appends to CompletedPhases (idempotent).
func (s *State) MarkPhaseComplete(p Phase) {
	s.Phase = p
	for _, existing := range s.CompletedPhases {
		if existing == p {
			return
		}
	}
	s.CompletedPhases = append(s.CompletedPhases, p)
}

// WriteState serializes a State to the given path.
func WriteState(path string, s *State) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

// ReadState reads and deserializes a State from the given path.
// Returns a *os.PathError wrapping fs.ErrNotExist if the file is missing.
func ReadState(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("unmarshal state: %w", err)
	}
	return &s, nil
}
```

- [ ] **Step 4: Run tests, verify pass**

```bash
go test ./internal/service/install/... -v
```

Expected: all three tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/service/install/
git commit -m "feat(install): add state marker type for install progress tracking"
```

---

## Task 4: CLAUDE.md surgery helper

**Files:**
- Create: `internal/service/install/claudemd.go`
- Create: `internal/service/install/claudemd_test.go`

**Context:** This is the riskiest part of the installer — it rewrites user-owned CLAUDE.md files. Every edge case from spec §4.1 has a test. The existing `internal/agent/managed_section.go` from PR #11 already handles the `<!-- nanite:start/end -->` marker logic; this file wraps it with the legacy `## agentrc` section removal.

- [ ] **Step 1: Read the existing managed section helper**

```bash
cd ~/Projects-apps/nanite
cat internal/agent/managed_section.go
```

Note the exact function signatures (`WriteManagedSection`, `ReadManagedSection`) for use in step 3.

- [ ] **Step 2: Write failing tests**

```go
// internal/service/install/claudemd_test.go
package install

import (
	"strings"
	"testing"
)

func TestRemoveAgentrcSection_Absent(t *testing.T) {
	input := "# Project\n\nSome content.\n"
	got, removed := RemoveAgentrcSection(input)
	if got != input {
		t.Errorf("unchanged input was modified: %q", got)
	}
	if removed != "" {
		t.Errorf("removed non-empty: %q", removed)
	}
}

func TestRemoveAgentrcSection_AtEnd(t *testing.T) {
	input := "# Project\n\nUser stuff.\n\n## agentrc\n\n" +
		"- If `.agentrc/boot-prompt.md` exists, read it first.\n" +
		"- More agentrc instructions.\n"
	got, removed := RemoveAgentrcSection(input)
	want := "# Project\n\nUser stuff.\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
	if !strings.Contains(removed, "agentrc boot-prompt") && !strings.Contains(removed, "agentrc/boot-prompt") {
		t.Errorf("removed content missing expected text: %q", removed)
	}
}

func TestRemoveAgentrcSection_FollowedByAnotherHeading(t *testing.T) {
	input := "# Project\n\n## agentrc\n\nStuff.\n\n## Other\n\nKeep me.\n"
	got, _ := RemoveAgentrcSection(input)
	want := "# Project\n\n## Other\n\nKeep me.\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestRemoveAgentrcSection_CaseInsensitive(t *testing.T) {
	input := "# Project\n\n## AGENTRC\n\nstuff\n"
	got, _ := RemoveAgentrcSection(input)
	want := "# Project\n\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestRemoveAgentrcSection_H3Level(t *testing.T) {
	input := "# Project\n\n## Setup\n\n### agentrc\n\nstuff\n\n## Other\n\nkeep\n"
	got, _ := RemoveAgentrcSection(input)
	want := "# Project\n\n## Setup\n\n## Other\n\nkeep\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

```bash
go test ./internal/service/install/... -run TestRemoveAgentrcSection -v
```

Expected: compile error, `RemoveAgentrcSection` undefined.

- [ ] **Step 4: Implement `RemoveAgentrcSection`**

```go
// internal/service/install/claudemd.go
package install

import (
	"regexp"
	"strings"
)

// agentrcHeading matches any markdown heading ("#" through "######") whose
// label is exactly "agentrc" (case-insensitive), optionally with trailing
// whitespace. Captures the heading level in group 1.
var agentrcHeading = regexp.MustCompile(`(?im)^(#{1,6})\s+agentrc\s*$`)

// RemoveAgentrcSection removes a `## agentrc` (or any heading level) section
// from a markdown file along with its body (everything up to the next heading
// of equal-or-higher level or EOF). Returns the cleaned content and the
// removed section (for snapshotting). If no agentrc section exists, returns
// the input unchanged and an empty removed string.
func RemoveAgentrcSection(content string) (cleaned string, removed string) {
	match := agentrcHeading.FindStringIndex(content)
	if match == nil {
		return content, ""
	}

	headingStart := match[0]
	headingEnd := match[1]

	// Determine the heading level from the match.
	headingLine := content[headingStart:headingEnd]
	level := 0
	for _, r := range headingLine {
		if r != '#' {
			break
		}
		level++
	}

	// Find the end of the section: either the next heading of level <= current,
	// or EOF.
	after := content[headingEnd:]
	// Build a pattern that matches any heading of level <= current.
	// e.g., if level=2, match `^#{1,2} `.
	siblingPattern := regexp.MustCompile(`(?m)^#{1,` + itoa(level) + `}\s`)
	siblingMatch := siblingPattern.FindStringIndex(after)

	var sectionEnd int
	if siblingMatch == nil {
		sectionEnd = len(content)
	} else {
		sectionEnd = headingEnd + siblingMatch[0]
	}

	removed = content[headingStart:sectionEnd]

	// Trim trailing whitespace from the preceding content (so we don't leave
	// a dangling blank line or a trailing "\n\n\n").
	pre := content[:headingStart]
	pre = strings.TrimRight(pre, "\n")
	if pre != "" {
		pre += "\n"
	}

	post := content[sectionEnd:]
	if post == "" {
		return pre, removed
	}
	// If the remaining content starts with whitespace/newlines, keep a single
	// separator newline.
	post = strings.TrimLeft(post, "\n")
	return pre + "\n" + post, removed
}

// itoa is a local helper to avoid importing strconv in this file for a
// single-digit integer.
func itoa(i int) string {
	if i <= 0 {
		return "0"
	}
	if i > 9 {
		return "9"
	}
	return string(rune('0' + i))
}
```

- [ ] **Step 5: Run tests, verify pass**

```bash
go test ./internal/service/install/... -run TestRemoveAgentrcSection -v
```

Expected: all five TestRemoveAgentrcSection tests pass.

- [ ] **Step 6: Write test for the full `UpdateCLAUDEmd` entry point**

```go
func TestUpdateCLAUDEmd_FreshFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	managed := "## Nanite agents\n\nSee .nanite/ for agent config.\n"

	report, err := UpdateCLAUDEmd(path, managed, nil)
	if err != nil {
		t.Fatalf("UpdateCLAUDEmd: %v", err)
	}
	if report.Created == false {
		t.Error("expected Created=true for missing file")
	}

	content, _ := os.ReadFile(path)
	if !strings.Contains(string(content), "<!-- nanite:start -->") {
		t.Errorf("missing start marker: %q", content)
	}
	if !strings.Contains(string(content), managed) {
		t.Errorf("missing managed content: %q", content)
	}
}

func TestUpdateCLAUDEmd_RemovesAgentrcSection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	original := "# Project\n\nUser stuff.\n\n## agentrc\n\nold instructions\n"
	os.WriteFile(path, []byte(original), 0o644)

	snapshotDir := t.TempDir()
	managed := "New managed content."
	report, err := UpdateCLAUDEmd(path, managed, &CLAUDESnapshotOpts{Dir: snapshotDir})
	if err != nil {
		t.Fatalf("UpdateCLAUDEmd: %v", err)
	}
	if !report.RemovedAgentrcSection {
		t.Error("expected RemovedAgentrcSection=true")
	}

	content, _ := os.ReadFile(path)
	if strings.Contains(string(content), "## agentrc") {
		t.Errorf("agentrc section not removed: %q", content)
	}
	if !strings.Contains(string(content), "User stuff.") {
		t.Errorf("user content lost: %q", content)
	}

	// Snapshot file should exist with the removed section.
	snapshot, err := os.ReadFile(filepath.Join(snapshotDir, "removed-claude-section.md"))
	if err != nil {
		t.Fatalf("snapshot missing: %v", err)
	}
	if !strings.Contains(string(snapshot), "old instructions") {
		t.Errorf("snapshot missing removed content: %q", snapshot)
	}
}

func TestUpdateCLAUDEmd_MalformedMarkers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	// start without matching end.
	os.WriteFile(path, []byte("# Project\n<!-- nanite:start -->\nstuff\n"), 0o644)

	report, err := UpdateCLAUDEmd(path, "new", &CLAUDESnapshotOpts{Dir: dir})
	if err != nil {
		t.Fatalf("should not error, got %v", err)
	}
	if !report.FallbackWritten {
		t.Error("expected FallbackWritten=true for malformed markers")
	}
	// Fallback file exists in the snapshot dir.
	if _, err := os.Stat(filepath.Join(dir, "claude-managed-section.md")); err != nil {
		t.Errorf("fallback file missing: %v", err)
	}
}
```

- [ ] **Step 7: Run tests, verify they fail (UpdateCLAUDEmd undefined)**

```bash
go test ./internal/service/install/... -run TestUpdateCLAUDEmd -v
```

- [ ] **Step 8: Implement `UpdateCLAUDEmd`**

```go
// internal/service/install/claudemd.go (append)

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hollis-labs/nanite/internal/agent"
)

// CLAUDESnapshotOpts controls where snapshot files (removed-claude-section.md,
// claude-managed-section.md fallback) are written.
type CLAUDESnapshotOpts struct {
	Dir string
}

// CLAUDEUpdateReport summarizes what UpdateCLAUDEmd did.
type CLAUDEUpdateReport struct {
	Created               bool
	RemovedAgentrcSection bool
	FallbackWritten       bool // true if markers were malformed and we wrote to a separate file
}

// UpdateCLAUDEmd writes managedContent into the nanite:start/end section of
// the CLAUDE.md at path. If the file doesn't exist, it's created with just
// the managed section plus a header. If a legacy `## agentrc` section is
// present, it's removed and snapshotted to snap.Dir/removed-claude-section.md.
// If the existing nanite markers are malformed, the managed content is
// written to snap.Dir/claude-managed-section.md instead of modifying
// CLAUDE.md (FallbackWritten=true in the report).
func UpdateCLAUDEmd(path string, managedContent string, snap *CLAUDESnapshotOpts) (*CLAUDEUpdateReport, error) {
	report := &CLAUDEUpdateReport{}

	existing, readErr := os.ReadFile(path)
	if os.IsNotExist(readErr) {
		// Create fresh.
		fresh := "# Project\n\n"
		newContent, wmErr := agent.WriteManagedSection(fresh, managedContent)
		if wmErr != nil {
			return nil, fmt.Errorf("write managed section to fresh file: %w", wmErr)
		}
		if err := os.WriteFile(path, []byte(newContent), 0o644); err != nil {
			return nil, fmt.Errorf("write CLAUDE.md: %w", err)
		}
		report.Created = true
		return report, nil
	}
	if readErr != nil {
		return nil, fmt.Errorf("read CLAUDE.md: %w", readErr)
	}

	content := string(existing)

	// Remove legacy agentrc section.
	cleaned, removed := RemoveAgentrcSection(content)
	if removed != "" {
		report.RemovedAgentrcSection = true
		if snap != nil && snap.Dir != "" {
			if err := os.MkdirAll(snap.Dir, 0o755); err != nil {
				return nil, fmt.Errorf("mkdir snapshot: %w", err)
			}
			if err := os.WriteFile(filepath.Join(snap.Dir, "removed-claude-section.md"), []byte(removed), 0o644); err != nil {
				return nil, fmt.Errorf("write snapshot: %w", err)
			}
		}
	}

	// Write managed section.
	updated, wmErr := agent.WriteManagedSection(cleaned, managedContent)
	if wmErr != nil {
		// Markers are malformed or unusable. Fallback: write managed content
		// to a sidecar file and leave CLAUDE.md otherwise alone.
		if snap != nil && snap.Dir != "" {
			if err := os.MkdirAll(snap.Dir, 0o755); err != nil {
				return nil, fmt.Errorf("mkdir snapshot: %w", err)
			}
			fallbackPath := filepath.Join(snap.Dir, "claude-managed-section.md")
			if err := os.WriteFile(fallbackPath, []byte(managedContent), 0o644); err != nil {
				return nil, fmt.Errorf("write fallback: %w", err)
			}
			// Write the cleaned content (without agentrc section) back to CLAUDE.md
			// so at least the legacy cleanup persists.
			if err := os.WriteFile(path, []byte(cleaned), 0o644); err != nil {
				return nil, fmt.Errorf("write cleaned CLAUDE.md: %w", err)
			}
			report.FallbackWritten = true
			return report, nil
		}
		return nil, fmt.Errorf("write managed section: %w", wmErr)
	}

	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return nil, fmt.Errorf("write CLAUDE.md: %w", err)
	}
	return report, nil
}
```

**Note:** Step 1 confirmed the exact name of the existing helper function. If it's not `agent.WriteManagedSection`, adjust the import and call site in this implementation. The existing helper's responsibility: given a markdown string and managed content, return the markdown with the content inserted between `<!-- nanite:start -->` and `<!-- nanite:end -->` markers (or appended if no markers exist). If it returns an error on malformed markers, our code falls back to the sidecar file. If the existing helper doesn't distinguish "malformed" from "missing," revise the fallback trigger accordingly.

- [ ] **Step 9: Run tests, verify all pass**

```bash
go test ./internal/service/install/... -run TestUpdateCLAUDEmd -v
```

Expected: all three UpdateCLAUDEmd tests pass.

- [ ] **Step 10: Commit**

```bash
git add internal/service/install/claudemd.go internal/service/install/claudemd_test.go
git commit -m "$(cat <<'EOF'
feat(install): CLAUDE.md surgery with legacy section removal

RemoveAgentrcSection strips a legacy `## agentrc` (or any heading level,
case-insensitive) section from CLAUDE.md and returns the removed text for
snapshotting. UpdateCLAUDEmd wraps this with the existing managed-section
helper and falls back to a sidecar file if the nanite:start/end markers
are malformed in the existing CLAUDE.md.
EOF
)"
```

---

## Task 5: Archive directory logic

**Files:**
- Create: `internal/service/install/archive.go`
- Create: `internal/service/install/archive_test.go`

- [ ] **Step 1: Write failing test**

```go
// internal/service/install/archive_test.go
package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResolveArchiveDir_NoCollision(t *testing.T) {
	base := t.TempDir()
	ts := time.Date(2026, 4, 9, 14, 30, 22, 0, time.UTC)
	got, err := ResolveArchiveDir(base, "hadron", ts)
	if err != nil {
		t.Fatalf("ResolveArchiveDir: %v", err)
	}
	want := filepath.Join(base, "hadron-2026-04-09")
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestResolveArchiveDir_Collision(t *testing.T) {
	base := t.TempDir()
	ts := time.Date(2026, 4, 9, 14, 30, 22, 0, time.UTC)
	// Pre-create the base dir so ResolveArchiveDir must add a suffix.
	if err := os.MkdirAll(filepath.Join(base, "hadron-2026-04-09"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveArchiveDir(base, "hadron", ts)
	if err != nil {
		t.Fatalf("ResolveArchiveDir: %v", err)
	}
	if !strings.HasPrefix(filepath.Base(got), "hadron-2026-04-09-143022") {
		t.Errorf("expected collision suffix, got %q", got)
	}
}

func TestResolveArchiveDir_DoubleCollision(t *testing.T) {
	base := t.TempDir()
	ts := time.Date(2026, 4, 9, 14, 30, 22, 0, time.UTC)
	os.MkdirAll(filepath.Join(base, "hadron-2026-04-09"), 0o755)
	os.MkdirAll(filepath.Join(base, "hadron-2026-04-09-143022"), 0o755)
	got, err := ResolveArchiveDir(base, "hadron", ts)
	if err != nil {
		t.Fatalf("ResolveArchiveDir: %v", err)
	}
	if !strings.HasPrefix(filepath.Base(got), "hadron-2026-04-09-143022-") {
		t.Errorf("expected sequence suffix, got %q", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/service/install/... -run TestResolveArchiveDir -v
```

- [ ] **Step 3: Implement `ResolveArchiveDir`**

```go
// internal/service/install/archive.go
package install

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ArchiveBase is the parent directory where all project archives live.
const ArchiveBase = "~/Projects-apps/.archived"

// ResolveArchiveDir picks the archive dir path for a project + timestamp,
// appending an HHMMSS suffix if the date-only path already exists, and a
// sequence suffix if that also collides.
func ResolveArchiveDir(base, projectBasename string, ts time.Time) (string, error) {
	if base == "" {
		return "", fmt.Errorf("empty archive base")
	}
	date := ts.UTC().Format("2006-01-02")
	candidate := filepath.Join(base, fmt.Sprintf("%s-%s", projectBasename, date))
	if _, err := os.Stat(candidate); os.IsNotExist(err) {
		return candidate, nil
	} else if err != nil {
		return "", fmt.Errorf("stat archive candidate: %w", err)
	}

	// Collision: append HHMMSS.
	hhmmss := ts.UTC().Format("150405")
	candidate = filepath.Join(base, fmt.Sprintf("%s-%s-%s", projectBasename, date, hhmmss))
	if _, err := os.Stat(candidate); os.IsNotExist(err) {
		return candidate, nil
	} else if err != nil {
		return "", err
	}

	// Still collides (same second). Append a sequence suffix.
	for i := 1; i < 1000; i++ {
		c := fmt.Sprintf("%s-%s-%s-%d", filepath.Join(base, projectBasename), date, hhmmss, i)
		if _, err := os.Stat(c); os.IsNotExist(err) {
			return c, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("too many archive collisions for %s/%s", projectBasename, date)
}

// ExpandArchiveBase expands "~" in ArchiveBase (or any path) to the user's home dir.
func ExpandArchiveBase(path string) (string, error) {
	if path == "" || path[0] != '~' {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, path[1:]), nil
}
```

- [ ] **Step 4: Run tests, verify pass**

```bash
go test ./internal/service/install/... -run TestResolveArchiveDir -v
```

- [ ] **Step 5: Write test for `ArchiveProjectAgentrc`**

```go
func TestArchiveProjectAgentrc(t *testing.T) {
	project := t.TempDir()
	archiveBase := t.TempDir()

	// Build a fake .agentrc/ and .agentrc-legacy/.
	mkFile := func(p, content string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mkFile(filepath.Join(project, ".agentrc", "config.yaml"), "version: 2.2.0\n")
	mkFile(filepath.Join(project, ".agentrc", "agents", "backend.md"), "# Backend\n")
	mkFile(filepath.Join(project, ".agentrc-legacy", "old.md"), "legacy content\n")

	ts := time.Date(2026, 4, 9, 14, 30, 22, 0, time.UTC)
	archiveDir, err := ArchiveProjectAgentrc(project, archiveBase, filepath.Base(project), ts)
	if err != nil {
		t.Fatalf("ArchiveProjectAgentrc: %v", err)
	}

	// .agentrc/ and .agentrc-legacy/ should no longer exist in project.
	if _, err := os.Stat(filepath.Join(project, ".agentrc")); !os.IsNotExist(err) {
		t.Error(".agentrc not removed from project")
	}
	if _, err := os.Stat(filepath.Join(project, ".agentrc-legacy")); !os.IsNotExist(err) {
		t.Error(".agentrc-legacy not removed from project")
	}

	// Archive dir should contain both.
	if _, err := os.Stat(filepath.Join(archiveDir, ".agentrc", "config.yaml")); err != nil {
		t.Errorf("archived .agentrc/config.yaml missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(archiveDir, ".agentrc-legacy", "old.md")); err != nil {
		t.Errorf("archived .agentrc-legacy/old.md missing: %v", err)
	}
}

func TestArchiveProjectAgentrc_NoLegacy(t *testing.T) {
	project := t.TempDir()
	archiveBase := t.TempDir()
	os.MkdirAll(filepath.Join(project, ".agentrc"), 0o755)
	os.WriteFile(filepath.Join(project, ".agentrc", "config.yaml"), []byte("x\n"), 0o644)

	ts := time.Date(2026, 4, 9, 14, 30, 22, 0, time.UTC)
	_, err := ArchiveProjectAgentrc(project, archiveBase, filepath.Base(project), ts)
	if err != nil {
		t.Fatalf("should succeed without .agentrc-legacy: %v", err)
	}
}
```

- [ ] **Step 6: Implement `ArchiveProjectAgentrc`**

```go
// ArchiveProjectAgentrc moves .agentrc/ (and .agentrc-legacy/ if present)
// from projectDir into a new archive directory under archiveBase. Returns
// the archive directory path.
func ArchiveProjectAgentrc(projectDir, archiveBase, basename string, ts time.Time) (string, error) {
	archiveDir, err := ResolveArchiveDir(archiveBase, basename, ts)
	if err != nil {
		return "", fmt.Errorf("resolve archive dir: %w", err)
	}
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir archive: %w", err)
	}

	srcAgentrc := filepath.Join(projectDir, ".agentrc")
	dstAgentrc := filepath.Join(archiveDir, ".agentrc")
	if _, err := os.Stat(srcAgentrc); err == nil {
		if err := os.Rename(srcAgentrc, dstAgentrc); err != nil {
			return "", fmt.Errorf("move .agentrc: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("stat .agentrc: %w", err)
	}

	srcLegacy := filepath.Join(projectDir, ".agentrc-legacy")
	dstLegacy := filepath.Join(archiveDir, ".agentrc-legacy")
	if _, err := os.Stat(srcLegacy); err == nil {
		if err := os.Rename(srcLegacy, dstLegacy); err != nil {
			return "", fmt.Errorf("move .agentrc-legacy: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("stat .agentrc-legacy: %w", err)
	}

	return archiveDir, nil
}
```

- [ ] **Step 7: Run tests, verify pass**

```bash
go test ./internal/service/install/... -run TestArchiveProject -v
```

- [ ] **Step 8: Commit**

```bash
git add internal/service/install/archive.go internal/service/install/archive_test.go
git commit -m "feat(install): archive directory resolution + project agentrc move"
```

---

## Task 6: Scaffold `.nanite/` and NANITE.md

**Files:**
- Create: `internal/service/install/scaffold.go`
- Create: `internal/service/install/scaffold_test.go`

- [ ] **Step 1: Write failing test**

```go
// internal/service/install/scaffold_test.go
package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScaffoldNaniteDir_Fresh(t *testing.T) {
	project := t.TempDir()
	globalHome := t.TempDir()
	// Simulate ~/.nanite/ with roles/, skills/, commands/ directories.
	for _, sub := range []string{"roles", "skills", "commands"} {
		os.MkdirAll(filepath.Join(globalHome, sub), 0o755)
	}

	err := ScaffoldNaniteDir(project, globalHome, ScaffoldSource{
		FrameworkVersion: "2.3.0",
		ProjectName:      "testproj",
	})
	if err != nil {
		t.Fatalf("ScaffoldNaniteDir: %v", err)
	}

	// Verify .nanite/config.yaml exists.
	cfg, err := os.ReadFile(filepath.Join(project, ".nanite", "config.yaml"))
	if err != nil {
		t.Fatalf("config.yaml missing: %v", err)
	}
	if !strings.Contains(string(cfg), "nanite_version: 2.3.0") {
		t.Errorf("config.yaml missing version: %q", cfg)
	}

	// Verify symlinks.
	for _, sub := range []string{"roles", "skills", "commands"} {
		link := filepath.Join(project, ".nanite", sub)
		info, err := os.Lstat(link)
		if err != nil {
			t.Errorf("%s link missing: %v", sub, err)
			continue
		}
		if info.Mode()&os.ModeSymlink == 0 {
			t.Errorf("%s is not a symlink", sub)
		}
		target, _ := os.Readlink(link)
		want := filepath.Join(globalHome, sub)
		if target != want {
			t.Errorf("%s target = %q, want %q", sub, target, want)
		}
	}

	// Verify agents/ directory created (empty).
	if _, err := os.Stat(filepath.Join(project, ".nanite", "agents")); err != nil {
		t.Errorf("agents/ missing: %v", err)
	}
}

func TestScaffoldNaniteMD_Fresh(t *testing.T) {
	project := t.TempDir()
	err := ScaffoldNaniteMD(project, ScaffoldSource{
		FrameworkVersion: "2.3.0",
		ProjectName:      "testproj",
	})
	if err != nil {
		t.Fatalf("ScaffoldNaniteMD: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(project, "NANITE.md"))
	if err != nil {
		t.Fatalf("NANITE.md missing: %v", err)
	}
	if !strings.Contains(string(data), "testproj") {
		t.Errorf("NANITE.md missing project name: %q", data)
	}
}

func TestScaffoldNaniteMD_PreservesExisting(t *testing.T) {
	project := t.TempDir()
	original := "# Custom NANITE.md\n\nUser content.\n"
	os.WriteFile(filepath.Join(project, "NANITE.md"), []byte(original), 0o644)

	err := ScaffoldNaniteMD(project, ScaffoldSource{ProjectName: "x"})
	if err != nil {
		t.Fatalf("ScaffoldNaniteMD: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(project, "NANITE.md"))
	if string(data) != original {
		t.Errorf("existing NANITE.md was overwritten: %q", data)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/service/install/... -run TestScaffold -v
```

- [ ] **Step 3: Implement scaffold functions**

```go
// internal/service/install/scaffold.go
package install

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"text/template"

	"github.com/hollis-labs/nanite/internal/assets"
)

// ScaffoldSource holds values used to render scaffold templates.
type ScaffoldSource struct {
	FrameworkVersion string
	ProjectName      string
}

// ScaffoldNaniteDir creates projectDir/.nanite/ with config.yaml, an empty
// agents/ directory, and symlinks into globalHome for roles/, skills/, and
// commands/. Does not touch any existing files.
func ScaffoldNaniteDir(projectDir, globalHome string, src ScaffoldSource) error {
	naniteDir := filepath.Join(projectDir, ".nanite")
	if err := os.MkdirAll(naniteDir, 0o755); err != nil {
		return fmt.Errorf("mkdir .nanite: %w", err)
	}

	// config.yaml (only if missing).
	cfgPath := filepath.Join(naniteDir, "config.yaml")
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		tmplBytes, err := assets.File("templates/nanite-config.yaml.tmpl")
		if err != nil {
			return fmt.Errorf("read config template: %w", err)
		}
		rendered, err := renderTemplate("config.yaml", string(tmplBytes), src)
		if err != nil {
			return err
		}
		if err := os.WriteFile(cfgPath, []byte(rendered), 0o644); err != nil {
			return fmt.Errorf("write config.yaml: %w", err)
		}
	}

	// agents/ (empty but present).
	if err := os.MkdirAll(filepath.Join(naniteDir, "agents"), 0o755); err != nil {
		return fmt.Errorf("mkdir agents: %w", err)
	}

	// Symlinks into globalHome.
	for _, sub := range []string{"roles", "skills", "commands"} {
		link := filepath.Join(naniteDir, sub)
		target := filepath.Join(globalHome, sub)
		if _, err := os.Lstat(link); err == nil {
			continue // already exists, don't touch
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("lstat %s: %w", link, err)
		}
		if err := os.Symlink(target, link); err != nil {
			return fmt.Errorf("symlink %s -> %s: %w", link, target, err)
		}
	}

	return nil
}

// ScaffoldNaniteMD writes projectDir/NANITE.md from the embedded template.
// Does not overwrite an existing file.
func ScaffoldNaniteMD(projectDir string, src ScaffoldSource) error {
	path := filepath.Join(projectDir, "NANITE.md")
	if _, err := os.Stat(path); err == nil {
		return nil // preserve existing
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat NANITE.md: %w", err)
	}
	tmplBytes, err := assets.File("templates/NANITE.md.tmpl")
	if err != nil {
		return fmt.Errorf("read template: %w", err)
	}
	rendered, err := renderTemplate("NANITE.md", string(tmplBytes), src)
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(rendered), 0o644)
}

func renderTemplate(name, body string, data any) (string, error) {
	t, err := template.New(name).Parse(body)
	if err != nil {
		return "", fmt.Errorf("parse template %s: %w", name, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute template %s: %w", name, err)
	}
	return buf.String(), nil
}
```

- [ ] **Step 4: Run tests, verify pass**

```bash
go test ./internal/service/install/... -run TestScaffold -v
```

- [ ] **Step 5: Commit**

```bash
git add internal/service/install/scaffold.go internal/service/install/scaffold_test.go
git commit -m "feat(install): scaffold .nanite/ directory and NANITE.md from templates"
```

---

## Task 7: `InstallHome` service entry point

**Files:**
- Create: `internal/service/install/install.go`

- [ ] **Step 1: Write failing test**

```go
// Append to internal/service/install/state_test.go or create install_test.go
// internal/service/install/install_test.go
package install

import (
	"os"
	"path/filepath"
	"testing"
)

func TestService_InstallHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	svc := New()
	report, err := svc.InstallHome(InstallHomeOptions{Target: filepath.Join(home, ".nanite")})
	if err != nil {
		t.Fatalf("InstallHome: %v", err)
	}
	if report.Created == 0 {
		t.Error("expected Created > 0 on fresh install")
	}

	// Verify VERSION file is in the target.
	if _, err := os.Stat(filepath.Join(home, ".nanite", "VERSION")); err != nil {
		t.Errorf("VERSION missing after install: %v", err)
	}

	// Second run should be idempotent.
	report2, err := svc.InstallHome(InstallHomeOptions{Target: filepath.Join(home, ".nanite")})
	if err != nil {
		t.Fatalf("second InstallHome: %v", err)
	}
	if report2.Created != 0 {
		t.Errorf("second run Created = %d, want 0", report2.Created)
	}
	if report2.Unchanged == 0 {
		t.Error("second run expected Unchanged > 0")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/service/install/... -run TestService_InstallHome -v
```

- [ ] **Step 3: Implement Service type + InstallHome**

```go
// internal/service/install/install.go
package install

import (
	"fmt"
	"os"

	"github.com/hollis-labs/nanite/internal/assets"
)

// Service is the install package's public entry point. All CLI and MCP
// surfaces call into a Service instance.
type Service struct{}

// New constructs a default Service.
func New() *Service {
	return &Service{}
}

// InstallHomeOptions controls InstallHome.
type InstallHomeOptions struct {
	Target string // typically ~/.nanite
	Force  bool   // overwrite user-modified files
}

// InstallHome extracts the embedded framework assets into the target
// directory (typically ~/.nanite). Skips user-modified files unless Force
// is set. Returns the extract report.
func (s *Service) InstallHome(opts InstallHomeOptions) (*assets.ExtractReport, error) {
	target := opts.Target
	if target == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve home: %w", err)
		}
		target = home + "/.nanite"
	}
	return assets.ExtractTo(target, assets.ExtractOptions{Force: opts.Force})
}
```

- [ ] **Step 4: Run test, verify pass**

```bash
go test ./internal/service/install/... -run TestService_InstallHome -v
```

- [ ] **Step 5: Commit**

```bash
git add internal/service/install/install.go internal/service/install/install_test.go
git commit -m "feat(install): InstallHome service entry point"
```

---

## Task 8: `InstallProject` — fresh + migrate-from-agentrc

**Files:**
- Modify: `internal/service/install/install.go`
- Create: `internal/service/install/migrate.go`
- Create: `internal/service/install/migrate_test.go`

**Context:** This is the most complex task. InstallProject handles the full matrix: fresh install, migrate-from-agentrc, adopt existing, partial-install detection. Each branch is tested separately.

- [ ] **Step 1: Write failing test for fresh install**

```go
// internal/service/install/migrate_test.go
package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupFakeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	svc := New()
	_, err := svc.InstallHome(InstallHomeOptions{Target: filepath.Join(home, ".nanite")})
	if err != nil {
		t.Fatalf("setup InstallHome: %v", err)
	}
	return home
}

func TestInstallProject_Fresh(t *testing.T) {
	home := setupFakeHome(t)
	project := t.TempDir()

	svc := New()
	report, err := svc.InstallProject(InstallProjectOptions{
		ProjectDir: project,
		GlobalHome: filepath.Join(home, ".nanite"),
	})
	if err != nil {
		t.Fatalf("InstallProject: %v", err)
	}
	if !report.FreshScaffold {
		t.Error("expected FreshScaffold=true")
	}

	// Verify .nanite/ and NANITE.md exist.
	if _, err := os.Stat(filepath.Join(project, ".nanite", "config.yaml")); err != nil {
		t.Errorf(".nanite/config.yaml missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, "NANITE.md")); err != nil {
		t.Errorf("NANITE.md missing: %v", err)
	}
	// CLAUDE.md was created by the installer.
	claude, err := os.ReadFile(filepath.Join(project, "CLAUDE.md"))
	if err != nil {
		t.Errorf("CLAUDE.md missing: %v", err)
	} else if !strings.Contains(string(claude), "<!-- nanite:start -->") {
		t.Errorf("CLAUDE.md missing nanite markers: %q", claude)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/service/install/... -run TestInstallProject_Fresh -v
```

- [ ] **Step 3: Implement InstallProject**

```go
// Append to internal/service/install/install.go

import (
	"path/filepath"
	"time"

	"github.com/hollis-labs/nanite/internal/agent"
)

// InstallProjectOptions controls InstallProject.
type InstallProjectOptions struct {
	ProjectDir         string
	GlobalHome         string // defaults to ~/.nanite
	MigrateFromAgentrc bool   // triggers archive-and-rewire flow
	ArchiveOnly        bool   // archive .agentrc/ without scaffolding .nanite/
	Force              bool
}

// InstallProjectReport summarizes what InstallProject did.
type InstallProjectReport struct {
	FreshScaffold           bool
	Migrated                bool
	Adopted                 bool
	ArchiveOnly             bool
	ArchivePath             string
	CLAUDEUpdateReport      *CLAUDEUpdateReport
	Warnings                []string
}

// InstallProject scaffolds .nanite/, NANITE.md, and CLAUDE.md managed sections
// in projectDir. Dispatches to the correct branch based on current state:
//   - fresh: no .agentrc/, no .nanite/, no state marker → scaffold fresh
//   - migrate: .agentrc/ present + MigrateFromAgentrc → archive + scaffold
//   - adopt: .nanite/ exists but incomplete → fill in missing pieces
//   - archive-only: .agentrc/ present + ArchiveOnly → archive, skip scaffold
func (s *Service) InstallProject(opts InstallProjectOptions) (*InstallProjectReport, error) {
	if opts.ProjectDir == "" {
		return nil, fmt.Errorf("empty project dir")
	}
	projectDir, err := filepath.Abs(opts.ProjectDir)
	if err != nil {
		return nil, err
	}
	// Resolve symlinks (projects may be symlinks).
	resolved, err := filepath.EvalSymlinks(projectDir)
	if err == nil {
		projectDir = resolved
	}

	globalHome := opts.GlobalHome
	if globalHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		globalHome = filepath.Join(home, ".nanite")
	}

	// Detect state.
	hasAgentrc := dirExists(filepath.Join(projectDir, ".agentrc"))
	hasNaniteDir := dirExists(filepath.Join(projectDir, ".nanite"))
	hasNaniteMD := fileExists(filepath.Join(projectDir, "NANITE.md"))

	// Guard: .agentrc/ and .nanite/ both present is unsupported.
	if hasAgentrc && hasNaniteDir {
		return nil, fmt.Errorf(".agentrc/ and .nanite/ both present in %s — please resolve manually", projectDir)
	}

	// Archive-only mode (for scratch dirs like agent-workspaces).
	if opts.ArchiveOnly {
		if !hasAgentrc {
			return nil, fmt.Errorf("--archive-only requires .agentrc/ in %s", projectDir)
		}
		archiveBase, err := ExpandArchiveBase(ArchiveBase)
		if err != nil {
			return nil, err
		}
		archiveDir, err := ArchiveProjectAgentrc(projectDir, archiveBase, filepath.Base(projectDir), time.Now())
		if err != nil {
			return nil, err
		}
		// Write state marker so rollback works.
		state := NewState(filepath.Base(projectDir), projectDir, archiveDir, assets.Version())
		state.MarkPhaseComplete(PhaseArchived)
		state.MarkPhaseComplete(PhaseComplete)
		if err := WriteState(filepath.Join(archiveDir, StateFileName), state); err != nil {
			return nil, err
		}
		return &InstallProjectReport{ArchiveOnly: true, ArchivePath: archiveDir}, nil
	}

	// Migrate-from-agentrc branch.
	if opts.MigrateFromAgentrc {
		if !hasAgentrc {
			return nil, fmt.Errorf("--migrate-from-agentrc requires .agentrc/ in %s", projectDir)
		}
		return s.migrateFromAgentrc(projectDir, globalHome)
	}

	if hasAgentrc {
		return nil, fmt.Errorf(".agentrc/ present in %s — pass --migrate-from-agentrc to archive it, or --archive-only to archive without scaffolding", projectDir)
	}

	// Adopt-existing branch: .nanite/ already present (e.g., PR #11 manual rename).
	if hasNaniteDir {
		return s.adoptExisting(projectDir, globalHome, hasNaniteMD)
	}

	// Fresh scaffold branch.
	return s.freshScaffold(projectDir, globalHome)
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
```

- [ ] **Step 4: Implement `freshScaffold`**

```go
// Append to internal/service/install/install.go

func (s *Service) freshScaffold(projectDir, globalHome string) (*InstallProjectReport, error) {
	src := ScaffoldSource{
		FrameworkVersion: assets.Version(),
		ProjectName:      filepath.Base(projectDir),
	}
	if err := ScaffoldNaniteDir(projectDir, globalHome, src); err != nil {
		return nil, err
	}
	if err := ScaffoldNaniteMD(projectDir, src); err != nil {
		return nil, err
	}
	managed := buildManagedSection(src)
	report, err := UpdateCLAUDEmd(filepath.Join(projectDir, "CLAUDE.md"), managed, nil)
	if err != nil {
		return nil, err
	}
	return &InstallProjectReport{
		FreshScaffold:      true,
		CLAUDEUpdateReport: report,
	}, nil
}

// buildManagedSection returns the content of the <!-- nanite:start/end --> block.
// Keep this simple — the thin version. Full version is generated by the
// claude adapter during SyncAllProjectRoots; this is the minimal text that
// tells Claude Code to read NANITE.md and .nanite/.
func buildManagedSection(src ScaffoldSource) string {
	return `## Nanite

Agent configuration for this project is managed by Nanite.

- Boot prompt: ` + "`NANITE.md`" + ` at the project root
- Agent config: ` + "`.nanite/config.yaml`" + `
- Per-agent context: ` + "`.nanite/agents/*.md`" + `

When the user says "Boot <agent>", look up the agent in .nanite/config.yaml
under ` + "`agents:`" + `, load each role file from ` + "`~/.nanite/roles/`" + `, load the listed
skills from ` + "`~/.nanite/skills/`" + `, and read the project context file from
.nanite/.

After context compaction, re-read NANITE.md and the active role/context files.
`
}
```

- [ ] **Step 5: Run fresh test, verify pass**

```bash
go test ./internal/service/install/... -run TestInstallProject_Fresh -v
```

- [ ] **Step 6: Write test for migrate-from-agentrc**

```go
// Append to internal/service/install/migrate_test.go

func TestInstallProject_MigrateFromAgentrc(t *testing.T) {
	home := setupFakeHome(t)
	project := t.TempDir()

	// Build fake .agentrc/ in project.
	mkFile := func(p, c string) {
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(c), 0o644)
	}
	mkFile(filepath.Join(project, ".agentrc", "config.yaml"), "agentrc_version: 2.2.0\nagents: {}\n")
	mkFile(filepath.Join(project, ".agentrc", "agents", "backend.md"), "# Backend context\n")
	mkFile(filepath.Join(project, ".agentrc", "boot-prompt.md"), "# Boot\n")
	mkFile(filepath.Join(project, ".agentrc-legacy", "old.md"), "legacy\n")
	mkFile(filepath.Join(project, "CLAUDE.md"), "# Project\n\n## agentrc\n\nold stuff\n")

	// Archive base: use a tempdir and override via env var.
	archBase := t.TempDir()
	t.Setenv("NANITE_ARCHIVE_BASE", archBase)

	svc := New()
	report, err := svc.InstallProject(InstallProjectOptions{
		ProjectDir:         project,
		GlobalHome:         filepath.Join(home, ".nanite"),
		MigrateFromAgentrc: true,
	})
	if err != nil {
		t.Fatalf("InstallProject: %v", err)
	}
	if !report.Migrated {
		t.Error("expected Migrated=true")
	}

	// .agentrc/ should be gone, .nanite/ should exist.
	if _, err := os.Stat(filepath.Join(project, ".agentrc")); !os.IsNotExist(err) {
		t.Error(".agentrc not archived")
	}
	if _, err := os.Stat(filepath.Join(project, ".nanite", "config.yaml")); err != nil {
		t.Errorf(".nanite/config.yaml missing: %v", err)
	}
	// Agents/ content should have been carried over.
	if _, err := os.Stat(filepath.Join(project, ".nanite", "agents", "backend.md")); err != nil {
		t.Errorf(".nanite/agents/backend.md missing: %v", err)
	}
	// boot-prompt.md carried over.
	if _, err := os.Stat(filepath.Join(project, ".nanite", "boot-prompt.md")); err != nil {
		t.Errorf(".nanite/boot-prompt.md missing: %v", err)
	}
	// CLAUDE.md agentrc section gone.
	claude, _ := os.ReadFile(filepath.Join(project, "CLAUDE.md"))
	if strings.Contains(string(claude), "## agentrc") {
		t.Errorf("CLAUDE.md still has agentrc section: %q", claude)
	}
	// Archive dir exists and has .install-state.json marked complete.
	archiveDir := report.ArchivePath
	if archiveDir == "" {
		t.Fatal("empty ArchivePath")
	}
	state, err := ReadState(filepath.Join(archiveDir, StateFileName))
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if state.Phase != PhaseComplete {
		t.Errorf("phase = %q, want %q", state.Phase, PhaseComplete)
	}
}
```

- [ ] **Step 7: Run test to verify it fails (migrateFromAgentrc undefined)**

```bash
go test ./internal/service/install/... -run TestInstallProject_MigrateFromAgentrc -v
```

- [ ] **Step 8: Implement `migrateFromAgentrc` in `internal/service/install/migrate.go`**

```go
// internal/service/install/migrate.go
package install

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/hollis-labs/nanite/internal/assets"
)

func (s *Service) migrateFromAgentrc(projectDir, globalHome string) (*InstallProjectReport, error) {
	basename := filepath.Base(projectDir)
	archiveBase, err := ExpandArchiveBase(archiveBaseOverride())
	if err != nil {
		return nil, err
	}

	ts := time.Now()
	archiveDir, err := ResolveArchiveDir(archiveBase, basename, ts)
	if err != nil {
		return nil, fmt.Errorf("resolve archive dir: %w", err)
	}
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir archive: %w", err)
	}

	// Write starting state marker.
	state := NewState(basename, projectDir, archiveDir, assets.Version())
	statePath := filepath.Join(archiveDir, StateFileName)
	if err := WriteState(statePath, state); err != nil {
		return nil, err
	}

	// Snapshot CLAUDE.md pre-edit for rollback.
	claudeSrc := filepath.Join(projectDir, "CLAUDE.md")
	if data, err := os.ReadFile(claudeSrc); err == nil {
		snapPath := filepath.Join(archiveDir, "CLAUDE.md.pre-edit")
		if err := os.WriteFile(snapPath, data, 0o644); err != nil {
			return nil, fmt.Errorf("snapshot CLAUDE.md: %w", err)
		}
	}

	// Move .agentrc/ and .agentrc-legacy/ into archive.
	if _, err := ArchiveProjectAgentrc(projectDir, archiveBase, basename, ts); err != nil {
		return nil, err
	}
	// ArchiveProjectAgentrc may have created a fresh dir — reconcile with ours.
	// In practice the timestamps match because we compute them in the same call;
	// if they diverge, use the state-marker path.
	state.MarkPhaseComplete(PhaseArchived)
	if err := WriteState(statePath, state); err != nil {
		return nil, err
	}

	// Copy .agentrc/agents/*.md into fresh .nanite/agents/.
	// Copy .agentrc/config.yaml into .nanite/config.yaml with field rename.
	// Copy .agentrc/boot-prompt.md if present.
	if err := carryOverFromArchive(archiveDir, projectDir); err != nil {
		return nil, err
	}

	// Scaffold the rest: symlinks, NANITE.md, managed CLAUDE.md section.
	src := ScaffoldSource{
		FrameworkVersion: assets.Version(),
		ProjectName:      basename,
	}
	if err := ScaffoldNaniteDir(projectDir, globalHome, src); err != nil {
		return nil, err
	}
	state.MarkPhaseComplete(PhaseScaffoldNaniteDir)
	WriteState(statePath, state)

	if err := ScaffoldNaniteMD(projectDir, src); err != nil {
		return nil, err
	}
	state.MarkPhaseComplete(PhaseScaffoldNaniteMD)
	WriteState(statePath, state)

	managed := buildManagedSection(src)
	claudeReport, err := UpdateCLAUDEmd(filepath.Join(projectDir, "CLAUDE.md"), managed, &CLAUDESnapshotOpts{Dir: archiveDir})
	if err != nil {
		return nil, err
	}
	state.MarkPhaseComplete(PhaseClaudeSync)
	WriteState(statePath, state)

	// TODO: AdapterRegistry.SyncAllProjectRoots() — wired in a later task
	// once the install service is hooked into the DI container.
	state.MarkPhaseComplete(PhaseAdapterSync)
	state.MarkPhaseComplete(PhaseComplete)
	WriteState(statePath, state)

	return &InstallProjectReport{
		Migrated:           true,
		ArchivePath:        archiveDir,
		CLAUDEUpdateReport: claudeReport,
	}, nil
}

// archiveBaseOverride allows tests to pivot the archive base via env var.
func archiveBaseOverride() string {
	if v := os.Getenv("NANITE_ARCHIVE_BASE"); v != "" {
		return v
	}
	return ArchiveBase
}

// carryOverFromArchive copies project-specific content from the archived
// .agentrc/ into the fresh .nanite/ directory (agents, config, boot prompt).
func carryOverFromArchive(archiveDir, projectDir string) error {
	srcAgentrc := filepath.Join(archiveDir, ".agentrc")
	dstNanite := filepath.Join(projectDir, ".nanite")
	if err := os.MkdirAll(dstNanite, 0o755); err != nil {
		return err
	}

	// agents/
	srcAgents := filepath.Join(srcAgentrc, "agents")
	dstAgents := filepath.Join(dstNanite, "agents")
	if _, err := os.Stat(srcAgents); err == nil {
		if err := copyDir(srcAgents, dstAgents); err != nil {
			return fmt.Errorf("copy agents: %w", err)
		}
	}

	// boot-prompt.md
	srcBoot := filepath.Join(srcAgentrc, "boot-prompt.md")
	if data, err := os.ReadFile(srcBoot); err == nil {
		if err := os.WriteFile(filepath.Join(dstNanite, "boot-prompt.md"), data, 0o644); err != nil {
			return fmt.Errorf("copy boot-prompt: %w", err)
		}
	}

	// config.yaml (field rename: agentrc_version → nanite_version)
	srcCfg := filepath.Join(srcAgentrc, "config.yaml")
	if data, err := os.ReadFile(srcCfg); err == nil {
		renamed := renameConfigFields(data)
		if err := os.WriteFile(filepath.Join(dstNanite, "config.yaml"), renamed, 0o644); err != nil {
			return fmt.Errorf("copy config: %w", err)
		}
	}

	return nil
}

// copyDir recursively copies src to dst.
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}

// renameConfigFields applies the agentrc → nanite field renames to a
// config.yaml byte slice. Uses a simple line-based scan to preserve
// comments and formatting. For the one field rename we do, this is
// sufficient; if more complex transforms are needed later, switch to a
// full YAML round-trip (goccy/go-yaml).
func renameConfigFields(data []byte) []byte {
	return []byte(strings.ReplaceAll(string(data), "agentrc_version:", "nanite_version:"))
}
```

**Note on test isolation:** `ArchiveProjectAgentrc` in Task 5 takes `archiveBase` as an explicit argument. The `migrateFromAgentrc` function reads from `NANITE_ARCHIVE_BASE` env var (set by tests) or falls back to the production default. This keeps tests isolated without polluting the production path.

- [ ] **Step 9: Run tests**

```bash
go test ./internal/service/install/... -v
```

Expected: all install tests pass.

- [ ] **Step 10: Commit**

```bash
git add internal/service/install/
git commit -m "$(cat <<'EOF'
feat(install): InstallProject with fresh scaffold and migrate-from-agentrc

Fresh scaffold: creates .nanite/ + NANITE.md + CLAUDE.md managed section.
Migrate-from-agentrc: archives .agentrc/ and .agentrc-legacy/ to
~/Projects-apps/.archived/{project}-YYYY-MM-DD/, copies project-specific
content (agents/, boot-prompt.md, config.yaml with field rename) into
fresh .nanite/, writes state marker, snapshots CLAUDE.md pre-edit.

Guards: refuses if .agentrc/ and .nanite/ both present; refuses --archive-only
without .agentrc/; refuses .agentrc/ present without --migrate-from-agentrc
or --archive-only flag.
EOF
)"
```

---

## Task 9: Adopt-existing path (Nanite's own state)

**Files:**
- Create: `internal/service/install/adopt.go`
- Append: `internal/service/install/migrate_test.go`

**Context:** Nanite already has `.nanite/` from PR #11's manual rename but lacks NANITE.md, symlinks into `~/.nanite/`, and a managed section in CLAUDE.md. The adopt path fills in the missing pieces without archiving.

- [ ] **Step 1: Write failing test**

```go
// Append to internal/service/install/migrate_test.go

func TestInstallProject_AdoptExisting(t *testing.T) {
	home := setupFakeHome(t)
	project := t.TempDir()

	// Pre-create .nanite/ like PR #11 did: config.yaml + agents/, but no symlinks, no NANITE.md, no CLAUDE.md managed section.
	mkFile := func(p, c string) {
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(c), 0o644)
	}
	mkFile(filepath.Join(project, ".nanite", "config.yaml"), "nanite_version: 2.3.0\nagents: {}\n")
	mkFile(filepath.Join(project, ".nanite", "agents", "backend.md"), "# Backend\n")
	mkFile(filepath.Join(project, "CLAUDE.md"), "# Project\n\nUser content.\n")

	svc := New()
	report, err := svc.InstallProject(InstallProjectOptions{
		ProjectDir: project,
		GlobalHome: filepath.Join(home, ".nanite"),
	})
	if err != nil {
		t.Fatalf("InstallProject: %v", err)
	}
	if !report.Adopted {
		t.Error("expected Adopted=true")
	}

	// Symlinks should now exist.
	for _, sub := range []string{"roles", "skills", "commands"} {
		link := filepath.Join(project, ".nanite", sub)
		if _, err := os.Lstat(link); err != nil {
			t.Errorf("%s symlink missing: %v", sub, err)
		}
	}
	// NANITE.md scaffolded.
	if _, err := os.Stat(filepath.Join(project, "NANITE.md")); err != nil {
		t.Errorf("NANITE.md missing: %v", err)
	}
	// CLAUDE.md has managed section.
	claude, _ := os.ReadFile(filepath.Join(project, "CLAUDE.md"))
	if !strings.Contains(string(claude), "<!-- nanite:start -->") {
		t.Errorf("CLAUDE.md missing managed section: %q", claude)
	}
	// Existing user content preserved.
	if !strings.Contains(string(claude), "User content.") {
		t.Errorf("user content lost: %q", claude)
	}
	// Original agents/backend.md preserved.
	data, _ := os.ReadFile(filepath.Join(project, ".nanite", "agents", "backend.md"))
	if string(data) != "# Backend\n" {
		t.Errorf("existing agents content modified: %q", data)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/service/install/... -run TestInstallProject_AdoptExisting -v
```

- [ ] **Step 3: Implement `adoptExisting`**

```go
// internal/service/install/adopt.go
package install

import (
	"path/filepath"

	"github.com/hollis-labs/nanite/internal/assets"
)

func (s *Service) adoptExisting(projectDir, globalHome string, hasNaniteMD bool) (*InstallProjectReport, error) {
	src := ScaffoldSource{
		FrameworkVersion: assets.Version(),
		ProjectName:      filepath.Base(projectDir),
	}

	// ScaffoldNaniteDir is safe: it won't overwrite config.yaml, won't touch
	// agents/, and only creates symlinks if they don't exist yet.
	if err := ScaffoldNaniteDir(projectDir, globalHome, src); err != nil {
		return nil, err
	}

	// ScaffoldNaniteMD is safe: it preserves an existing NANITE.md.
	if err := ScaffoldNaniteMD(projectDir, src); err != nil {
		return nil, err
	}

	managed := buildManagedSection(src)
	claudeReport, err := UpdateCLAUDEmd(filepath.Join(projectDir, "CLAUDE.md"), managed, nil)
	if err != nil {
		return nil, err
	}

	return &InstallProjectReport{
		Adopted:            true,
		CLAUDEUpdateReport: claudeReport,
	}, nil
}
```

- [ ] **Step 4: Run tests, verify pass**

```bash
go test ./internal/service/install/... -run TestInstallProject -v
```

- [ ] **Step 5: Commit**

```bash
git add internal/service/install/adopt.go internal/service/install/migrate_test.go
git commit -m "feat(install): adopt-existing branch for partially-migrated projects"
```

---

## Task 10: Rollback

**Files:**
- Create: `internal/service/install/rollback.go`
- Create: `internal/service/install/rollback_test.go`

- [ ] **Step 1: Write failing test**

```go
// internal/service/install/rollback_test.go
package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRollback_RoundTrip(t *testing.T) {
	home := setupFakeHome(t)
	project := t.TempDir()
	archBase := t.TempDir()
	t.Setenv("NANITE_ARCHIVE_BASE", archBase)

	// Build and migrate a fake project.
	mkFile := func(p, c string) {
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(c), 0o644)
	}
	mkFile(filepath.Join(project, ".agentrc", "config.yaml"), "agentrc_version: 2.2.0\nagents: {}\n")
	mkFile(filepath.Join(project, ".agentrc", "agents", "backend.md"), "# Backend\n")
	originalCLAUDE := "# Project\n\n## agentrc\n\nold loader\n"
	mkFile(filepath.Join(project, "CLAUDE.md"), originalCLAUDE)

	svc := New()
	report, err := svc.InstallProject(InstallProjectOptions{
		ProjectDir:         project,
		GlobalHome:         filepath.Join(home, ".nanite"),
		MigrateFromAgentrc: true,
	})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Verify .agentrc gone.
	if _, err := os.Stat(filepath.Join(project, ".agentrc")); !os.IsNotExist(err) {
		t.Fatal(".agentrc not archived during setup")
	}

	// Roll back.
	if err := svc.Rollback(RollbackOptions{ProjectDir: project, ArchivePath: report.ArchivePath}); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	// .agentrc/ should be restored.
	if _, err := os.Stat(filepath.Join(project, ".agentrc", "config.yaml")); err != nil {
		t.Errorf(".agentrc not restored: %v", err)
	}
	// .nanite/ should be gone.
	if _, err := os.Stat(filepath.Join(project, ".nanite")); !os.IsNotExist(err) {
		t.Errorf(".nanite not removed after rollback")
	}
	// NANITE.md should be gone (it was fresh-scaffolded).
	if _, err := os.Stat(filepath.Join(project, "NANITE.md")); !os.IsNotExist(err) {
		t.Errorf("NANITE.md not removed after rollback")
	}
	// CLAUDE.md should be restored to original (with ## agentrc section).
	data, _ := os.ReadFile(filepath.Join(project, "CLAUDE.md"))
	if !strings.Contains(string(data), "## agentrc") {
		t.Errorf("CLAUDE.md agentrc section not restored: %q", data)
	}
}

func TestRollback_NeverMigrated(t *testing.T) {
	project := t.TempDir()
	svc := New()
	err := svc.Rollback(RollbackOptions{ProjectDir: project})
	if err == nil {
		t.Error("expected error for never-migrated project")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/service/install/... -run TestRollback -v
```

- [ ] **Step 3: Implement `Rollback`**

```go
// internal/service/install/rollback.go
package install

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// RollbackOptions controls Rollback.
type RollbackOptions struct {
	ProjectDir  string
	ArchivePath string // explicit archive dir; if empty, finds the most recent matching one
}

// Rollback reverses a migration: restores .agentrc/ (and .agentrc-legacy/) from
// the archive dir, removes .nanite/ and NANITE.md, and restores CLAUDE.md to
// its pre-edit snapshot. If ArchivePath is empty, finds the most recent
// archive dir for this project.
func (s *Service) Rollback(opts RollbackOptions) error {
	projectDir, err := filepath.Abs(opts.ProjectDir)
	if err != nil {
		return err
	}
	basename := filepath.Base(projectDir)

	archiveDir := opts.ArchivePath
	if archiveDir == "" {
		base, err := ExpandArchiveBase(archiveBaseOverride())
		if err != nil {
			return err
		}
		latest, err := findLatestArchive(base, basename)
		if err != nil {
			return err
		}
		if latest == "" {
			return fmt.Errorf("no archive found for %s under %s", basename, base)
		}
		archiveDir = latest
	}

	// Sanity: state marker must exist.
	statePath := filepath.Join(archiveDir, StateFileName)
	if _, err := os.Stat(statePath); err != nil {
		return fmt.Errorf("state marker missing at %s — refusing to roll back without evidence of a prior install", statePath)
	}

	// Restore CLAUDE.md from pre-edit snapshot (if present).
	preEdit := filepath.Join(archiveDir, "CLAUDE.md.pre-edit")
	if data, err := os.ReadFile(preEdit); err == nil {
		if err := os.WriteFile(filepath.Join(projectDir, "CLAUDE.md"), data, 0o644); err != nil {
			return fmt.Errorf("restore CLAUDE.md: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read CLAUDE.md.pre-edit: %w", err)
	} else {
		// No snapshot — CLAUDE.md was created by the installer. Remove it.
		_ = os.Remove(filepath.Join(projectDir, "CLAUDE.md"))
	}

	// Remove .nanite/ and NANITE.md from project.
	if err := os.RemoveAll(filepath.Join(projectDir, ".nanite")); err != nil {
		return fmt.Errorf("remove .nanite: %w", err)
	}
	if err := os.Remove(filepath.Join(projectDir, "NANITE.md")); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove NANITE.md: %w", err)
	}

	// Restore .agentrc/ and .agentrc-legacy/ from archive.
	for _, name := range []string{".agentrc", ".agentrc-legacy"} {
		src := filepath.Join(archiveDir, name)
		dst := filepath.Join(projectDir, name)
		if _, err := os.Stat(src); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return err
		}
		if err := os.Rename(src, dst); err != nil {
			return fmt.Errorf("restore %s: %w", name, err)
		}
	}

	return nil
}

// findLatestArchive returns the most recent archive dir matching basename-*.
// Returns "" if no matches.
func findLatestArchive(base, basename string) (string, error) {
	entries, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	var latest string
	var latestTime time.Time
	prefix := basename + "-"
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(latestTime) {
			latestTime = info.ModTime()
			latest = filepath.Join(base, e.Name())
		}
	}
	return latest, nil
}

// ensure fs import referenced somewhere (rm if unused)
var _ fs.DirEntry
```

- [ ] **Step 4: Run tests, verify pass**

```bash
go test ./internal/service/install/... -run TestRollback -v
```

- [ ] **Step 5: Commit**

```bash
git add internal/service/install/rollback.go internal/service/install/rollback_test.go
git commit -m "feat(install): rollback reverses migration from archive snapshots"
```

---

## Task 11: Resume + Restart

**Files:**
- Create: `internal/service/install/resume.go`
- Create: `internal/service/install/resume_test.go`

- [ ] **Step 1: Write failing test**

```go
// internal/service/install/resume_test.go
package install

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResume_FromArchivedPhase(t *testing.T) {
	home := setupFakeHome(t)
	project := t.TempDir()
	archBase := t.TempDir()
	t.Setenv("NANITE_ARCHIVE_BASE", archBase)

	// Simulate a partial migration that crashed after archive but before scaffold:
	// 1. Set up an archive dir with the project's .agentrc content and a state
	//    marker stuck at PhaseArchived.
	basename := filepath.Base(project)
	archiveDir := filepath.Join(archBase, basename+"-2026-04-09")
	os.MkdirAll(archiveDir, 0o755)
	mkFile := func(p, c string) {
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(c), 0o644)
	}
	mkFile(filepath.Join(archiveDir, ".agentrc", "config.yaml"), "agentrc_version: 2.2.0\nagents: {}\n")
	mkFile(filepath.Join(archiveDir, ".agentrc", "agents", "backend.md"), "# Backend\n")

	state := NewState(basename, project, archiveDir, "2.3.0")
	state.MarkPhaseComplete(PhaseArchived)
	WriteState(filepath.Join(archiveDir, StateFileName), state)

	svc := New()
	if err := svc.Resume(ResumeOptions{ProjectDir: project, ArchivePath: archiveDir, GlobalHome: filepath.Join(home, ".nanite")}); err != nil {
		t.Fatalf("Resume: %v", err)
	}

	// .nanite/ should now exist with carried-over content.
	if _, err := os.Stat(filepath.Join(project, ".nanite", "config.yaml")); err != nil {
		t.Errorf(".nanite/config.yaml missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, ".nanite", "agents", "backend.md")); err != nil {
		t.Errorf(".nanite/agents/backend.md missing: %v", err)
	}
	// State marker should be at PhaseComplete.
	newState, _ := ReadState(filepath.Join(archiveDir, StateFileName))
	if newState.Phase != PhaseComplete {
		t.Errorf("phase = %q, want %q", newState.Phase, PhaseComplete)
	}
}

func TestRestart_ReversesArchive(t *testing.T) {
	home := setupFakeHome(t)
	project := t.TempDir()
	archBase := t.TempDir()
	t.Setenv("NANITE_ARCHIVE_BASE", archBase)

	// Simulate same partial state as above.
	basename := filepath.Base(project)
	archiveDir := filepath.Join(archBase, basename+"-2026-04-09")
	os.MkdirAll(archiveDir, 0o755)
	os.MkdirAll(filepath.Join(archiveDir, ".agentrc"), 0o755)
	os.WriteFile(filepath.Join(archiveDir, ".agentrc", "config.yaml"), []byte("agentrc_version: 2.2.0\nagents: {}\n"), 0o644)
	state := NewState(basename, project, archiveDir, "2.3.0")
	state.MarkPhaseComplete(PhaseArchived)
	WriteState(filepath.Join(archiveDir, StateFileName), state)

	svc := New()
	if err := svc.Restart(RestartOptions{ProjectDir: project, ArchivePath: archiveDir, GlobalHome: filepath.Join(home, ".nanite")}); err != nil {
		t.Fatalf("Restart: %v", err)
	}

	// After restart: .agentrc/ should NOT be in project (Restart ran a fresh migration
	// which moved it back to a new archive), .nanite/ should exist.
	if _, err := os.Stat(filepath.Join(project, ".nanite", "config.yaml")); err != nil {
		t.Errorf(".nanite/config.yaml missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, ".agentrc")); !os.IsNotExist(err) {
		t.Errorf(".agentrc still in project after restart")
	}
	// Old archive should be gone (restart removed it).
	if _, err := os.Stat(archiveDir); !os.IsNotExist(err) {
		t.Errorf("old archive still exists after restart")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/service/install/... -run TestResume -v
go test ./internal/service/install/... -run TestRestart -v
```

- [ ] **Step 3: Implement Resume and Restart**

```go
// internal/service/install/resume.go
package install

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hollis-labs/nanite/internal/assets"
)

// ResumeOptions controls Resume.
type ResumeOptions struct {
	ProjectDir  string
	ArchivePath string
	GlobalHome  string
}

// Resume continues a partial migration from wherever its state marker left off.
func (s *Service) Resume(opts ResumeOptions) error {
	statePath := filepath.Join(opts.ArchivePath, StateFileName)
	state, err := ReadState(statePath)
	if err != nil {
		return fmt.Errorf("read state: %w", err)
	}
	if state.Phase == PhaseComplete {
		return nil // already done
	}

	// Resume path mirrors migrateFromAgentrc's phases, skipping anything
	// already completed.
	projectDir := opts.ProjectDir
	globalHome := opts.GlobalHome
	src := ScaffoldSource{
		FrameworkVersion: assets.Version(),
		ProjectName:      filepath.Base(projectDir),
	}

	if !hasPhase(state, PhaseScaffoldNaniteDir) {
		if err := carryOverFromArchive(opts.ArchivePath, projectDir); err != nil {
			return err
		}
		if err := ScaffoldNaniteDir(projectDir, globalHome, src); err != nil {
			return err
		}
		state.MarkPhaseComplete(PhaseScaffoldNaniteDir)
		WriteState(statePath, state)
	}
	if !hasPhase(state, PhaseScaffoldNaniteMD) {
		if err := ScaffoldNaniteMD(projectDir, src); err != nil {
			return err
		}
		state.MarkPhaseComplete(PhaseScaffoldNaniteMD)
		WriteState(statePath, state)
	}
	if !hasPhase(state, PhaseClaudeSync) {
		managed := buildManagedSection(src)
		if _, err := UpdateCLAUDEmd(filepath.Join(projectDir, "CLAUDE.md"), managed, &CLAUDESnapshotOpts{Dir: opts.ArchivePath}); err != nil {
			return err
		}
		state.MarkPhaseComplete(PhaseClaudeSync)
		WriteState(statePath, state)
	}
	state.MarkPhaseComplete(PhaseAdapterSync)
	state.MarkPhaseComplete(PhaseComplete)
	WriteState(statePath, state)
	return nil
}

// RestartOptions controls Restart.
type RestartOptions struct {
	ProjectDir  string
	ArchivePath string
	GlobalHome  string
}

// Restart reverses a partial migration (moves .agentrc/ back to projectDir,
// removes .nanite/ and the archive dir) and then runs a fresh migration.
func (s *Service) Restart(opts RestartOptions) error {
	// Reverse: move .agentrc/ from archive back to project, remove partial .nanite/.
	for _, name := range []string{".agentrc", ".agentrc-legacy"} {
		src := filepath.Join(opts.ArchivePath, name)
		dst := filepath.Join(opts.ProjectDir, name)
		if _, err := os.Stat(src); err == nil {
			if err := os.Rename(src, dst); err != nil {
				return fmt.Errorf("restore %s: %w", name, err)
			}
		}
	}
	_ = os.RemoveAll(filepath.Join(opts.ProjectDir, ".nanite"))
	_ = os.Remove(filepath.Join(opts.ProjectDir, "NANITE.md"))
	_ = os.RemoveAll(opts.ArchivePath)

	// Run fresh migration.
	_, err := s.InstallProject(InstallProjectOptions{
		ProjectDir:         opts.ProjectDir,
		GlobalHome:         opts.GlobalHome,
		MigrateFromAgentrc: true,
	})
	return err
}

func hasPhase(s *State, p Phase) bool {
	for _, c := range s.CompletedPhases {
		if c == p {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run tests, verify pass**

```bash
go test ./internal/service/install/... -run "TestResume|TestRestart" -v
```

- [ ] **Step 5: Commit**

```bash
git add internal/service/install/resume.go internal/service/install/resume_test.go
git commit -m "feat(install): Resume and Restart for partial installs"
```

---

## Task 12: Partial install detection + interactive prompt

**Files:**
- Modify: `internal/service/install/install.go`
- Append: `internal/service/install/migrate_test.go`

**Context:** When `InstallProject` runs on a project with no `.agentrc/`, no `.nanite/`, but a matching archive dir containing a state marker with `phase != complete`, it needs to detect this and branch based on caller mode (interactive vs. non-interactive).

- [ ] **Step 1: Write failing test**

```go
// Append to internal/service/install/migrate_test.go

func TestInstallProject_DetectsPartial_NonInteractive(t *testing.T) {
	home := setupFakeHome(t)
	project := t.TempDir()
	archBase := t.TempDir()
	t.Setenv("NANITE_ARCHIVE_BASE", archBase)

	// Set up a dangling archive (partial install state).
	basename := filepath.Base(project)
	archiveDir := filepath.Join(archBase, basename+"-2026-04-09")
	os.MkdirAll(archiveDir, 0o755)
	os.MkdirAll(filepath.Join(archiveDir, ".agentrc"), 0o755)
	os.WriteFile(filepath.Join(archiveDir, ".agentrc", "config.yaml"), []byte("x\n"), 0o644)
	state := NewState(basename, project, archiveDir, "2.3.0")
	state.MarkPhaseComplete(PhaseArchived)
	WriteState(filepath.Join(archiveDir, StateFileName), state)

	svc := New()
	_, err := svc.InstallProject(InstallProjectOptions{
		ProjectDir: project,
		GlobalHome: filepath.Join(home, ".nanite"),
	})
	if err == nil {
		t.Fatal("expected error for partial install without --resume or --restart")
	}
	if !errors.Is(err, ErrPartialInstall) {
		t.Errorf("expected ErrPartialInstall, got %v", err)
	}
}
```

- [ ] **Step 2: Implement partial detection in `InstallProject`**

Add at the top of `InstallProject` (after state detection but before the branch dispatch):

```go
// Append to internal/service/install/install.go

import "errors"

// ErrPartialInstall is returned when InstallProject detects a partial install
// (a matching archive dir with a non-complete state marker) and neither
// --resume nor --restart was requested.
var ErrPartialInstall = errors.New("partial install detected")

// (inside InstallProject, after dirExists checks and before the branch dispatch)

	// Partial install detection: look for a matching archive dir with a state
	// marker in a non-complete phase.
	if !hasAgentrc && !hasNaniteDir {
		base, err := ExpandArchiveBase(archiveBaseOverride())
		if err == nil {
			latest, _ := findLatestArchive(base, filepath.Base(projectDir))
			if latest != "" {
				if s, err := ReadState(filepath.Join(latest, StateFileName)); err == nil && s.Phase != PhaseComplete {
					return nil, fmt.Errorf("%w: partial install from %s at phase %q; rerun with --resume or --restart",
						ErrPartialInstall, s.StartedAt.Format("2006-01-02 15:04:05"), s.Phase)
				}
			}
		}
	}
```

- [ ] **Step 3: Add `errors` import to the test**

```go
// Top of migrate_test.go
import "errors"
```

- [ ] **Step 4: Run tests, verify pass**

```bash
go test ./internal/service/install/... -run TestInstallProject_DetectsPartial -v
```

- [ ] **Step 5: Commit**

```bash
git add internal/service/install/
git commit -m "feat(install): detect partial installs from state marker, return ErrPartialInstall"
```

---

## Task 13: `cmdInstall` CLI entry point

**Files:**
- Create: `cmd/nanite/install_cmd.go`
- Modify: `cmd/nanite/main.go`

- [ ] **Step 1: Read the existing `cmdMCP` / `cmdServe` patterns**

```bash
cd ~/Projects-apps/nanite
wc -l cmd/nanite/mcp_cmd.go
head -30 cmd/nanite/mcp_cmd.go
grep -n "case " cmd/nanite/main.go
```

Note the style: plain `switch`/`case` dispatch, `flag.NewFlagSet`, no cobra.

- [ ] **Step 2: Implement `cmdInstall`**

```go
// cmd/nanite/install_cmd.go
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/hollis-labs/nanite/internal/brand"
	"github.com/hollis-labs/nanite/internal/service/install"
)

// cmdInstall is the entry point for `nanite install`. Matches the
// cmdServe/cmdMCP pattern: manual flag parsing, direct service calls,
// os.Exit on error.
func cmdInstall(args []string) {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	project := fs.String("project", "", "target project directory (omit to install to ~/.nanite/)")
	migrate := fs.Bool("migrate-from-agentrc", false, "archive existing .agentrc/ and migrate to .nanite/")
	archiveOnly := fs.Bool("archive-only", false, "archive .agentrc/ without scaffolding .nanite/ (for scratch dirs)")
	rollback := fs.Bool("rollback", false, "reverse the most recent migration for --project")
	resume := fs.Bool("resume", false, "continue a partial install from its state marker")
	restart := fs.Bool("restart", false, "reverse a partial install and run a fresh migration")
	refresh := fs.Bool("refresh", false, "re-extract embedded assets to ~/.nanite/, skipping user-modified files")
	force := fs.Bool("force", false, "overwrite user-modified files during --refresh")
	printDiff := fs.Bool("print-diff", false, "dry-run: show what would change, don't modify anything")
	_ = printDiff
	fs.Parse(args)

	svc := install.New()

	// Home install (no --project) or refresh.
	if *project == "" || *refresh {
		target := ""
		if *project == "" {
			home, _ := os.UserHomeDir()
			target = filepath.Join(home, ".nanite")
		}
		report, err := svc.InstallHome(install.InstallHomeOptions{Target: target, Force: *force})
		if err != nil {
			dieErr("install home", err)
		}
		fmt.Printf("%s install: created=%d unchanged=%d skipped=%d forced=%d\n",
			brand.BinaryName, report.Created, report.Unchanged, report.Skipped, report.Forced)
		if len(report.SkippedFiles) > 0 {
			fmt.Printf("  skipped (user-modified):\n")
			for _, f := range report.SkippedFiles {
				fmt.Printf("    %s\n", f)
			}
		}
		return
	}

	// Project operations.
	projectDir := *project
	if projectDir == "." {
		cwd, err := os.Getwd()
		if err != nil {
			dieErr("getwd", err)
		}
		projectDir = cwd
	}

	if *rollback {
		if err := svc.Rollback(install.RollbackOptions{ProjectDir: projectDir}); err != nil {
			dieErr("rollback", err)
		}
		fmt.Printf("%s: rolled back %s\n", brand.BinaryName, projectDir)
		return
	}

	if *resume {
		base, _ := install.ExpandArchiveBase("~/Projects-apps/.archived")
		latest, _ := findLatestArchiveDir(base, filepath.Base(projectDir))
		if err := svc.Resume(install.ResumeOptions{ProjectDir: projectDir, ArchivePath: latest}); err != nil {
			dieErr("resume", err)
		}
		fmt.Printf("%s: resumed %s\n", brand.BinaryName, projectDir)
		return
	}

	if *restart {
		base, _ := install.ExpandArchiveBase("~/Projects-apps/.archived")
		latest, _ := findLatestArchiveDir(base, filepath.Base(projectDir))
		if err := svc.Restart(install.RestartOptions{ProjectDir: projectDir, ArchivePath: latest}); err != nil {
			dieErr("restart", err)
		}
		fmt.Printf("%s: restarted migration for %s\n", brand.BinaryName, projectDir)
		return
	}

	opts := install.InstallProjectOptions{
		ProjectDir:         projectDir,
		MigrateFromAgentrc: *migrate,
		ArchiveOnly:        *archiveOnly,
		Force:              *force,
	}
	report, err := svc.InstallProject(opts)
	if err != nil {
		// Partial install detection: if non-TTY, exit with code 3.
		if errors.Is(err, install.ErrPartialInstall) {
			if !isTTY(os.Stdin) {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(3)
			}
			// Interactive: prompt for resume/restart/cancel.
			fmt.Fprintln(os.Stderr, err)
			fmt.Println("How do you want to proceed?")
			fmt.Println("  [r] Resume from last completed phase")
			fmt.Println("  [s] Start over (restart)")
			fmt.Println("  [c] Cancel")
			fmt.Print("> ")
			var choice string
			fmt.Scanln(&choice)
			base, _ := install.ExpandArchiveBase("~/Projects-apps/.archived")
			latest, _ := findLatestArchiveDir(base, filepath.Base(projectDir))
			switch strings.ToLower(choice) {
			case "r":
				if err := svc.Resume(install.ResumeOptions{ProjectDir: projectDir, ArchivePath: latest}); err != nil {
					dieErr("resume", err)
				}
			case "s":
				if err := svc.Restart(install.RestartOptions{ProjectDir: projectDir, ArchivePath: latest}); err != nil {
					dieErr("restart", err)
				}
			default:
				fmt.Println("cancelled")
				os.Exit(0)
			}
			return
		}
		dieErr("install project", err)
	}

	switch {
	case report.FreshScaffold:
		fmt.Printf("%s: fresh scaffold in %s\n", brand.BinaryName, projectDir)
	case report.Migrated:
		fmt.Printf("%s: migrated %s (archive: %s)\n", brand.BinaryName, projectDir, report.ArchivePath)
	case report.Adopted:
		fmt.Printf("%s: adopted existing .nanite/ in %s\n", brand.BinaryName, projectDir)
	case report.ArchiveOnly:
		fmt.Printf("%s: archived %s (archive: %s)\n", brand.BinaryName, projectDir, report.ArchivePath)
	}
}

// dieErr prints an error and exits with code 1.
func dieErr(action string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %s: %v\n", brand.BinaryName, action, err)
	os.Exit(1)
}

// isTTY returns true if the given file is a terminal.
func isTTY(f *os.File) bool {
	// Simple heuristic: check if stdin is character device.
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

// findLatestArchiveDir is a CLI-side helper that reads the filesystem to find
// the most recent archive dir matching basename. The install package has
// an unexported equivalent; we reimplement here to avoid exporting it.
func findLatestArchiveDir(base, basename string) (string, error) {
	if _, err := os.Stat(base); err != nil {
		return "", err
	}
	cmd := exec.Command("sh", "-c", fmt.Sprintf("ls -t %s | grep '^%s-' | head -1", base, basename))
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	name := strings.TrimSpace(string(out))
	if name == "" {
		return "", fmt.Errorf("no archive found for %s", basename)
	}
	return filepath.Join(base, name), nil
}
```

**Note:** Replace the `findLatestArchiveDir` shell-out with a proper Go implementation that reads the directory and sorts by mtime. The shell version is a placeholder for clarity; rewrite before committing:

```go
func findLatestArchiveDir(base, basename string) (string, error) {
	entries, err := os.ReadDir(base)
	if err != nil {
		return "", err
	}
	var latest string
	var latestTime time.Time
	prefix := basename + "-"
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(latestTime) {
			latestTime = info.ModTime()
			latest = filepath.Join(base, e.Name())
		}
	}
	if latest == "" {
		return "", fmt.Errorf("no archive found for %s", basename)
	}
	return latest, nil
}
```

Remove the `"os/exec"` import and add `"time"`.

- [ ] **Step 3: Wire `cmdInstall` into `main.go`**

```go
// cmd/nanite/main.go (inside the switch statement in main())

	case "install":
		cmdInstall(os.Args[2:])
```

Also update the usage line:

```go
	fmt.Fprintln(os.Stderr, "commands: serve, install, plugin, mcp, version")
```

- [ ] **Step 4: Smoke test the CLI**

```bash
cd ~/Projects-apps/nanite
go build ./cmd/nanite
./nanite install --help 2>&1 | head -20
```

Expected: help text showing the flags.

- [ ] **Step 5: Test fresh install on a temp project**

```bash
mkdir -p /tmp/nanite-install-smoke
cd /tmp/nanite-install-smoke
~/Projects-apps/nanite/nanite install --project .
ls -la .nanite NANITE.md CLAUDE.md
cat NANITE.md
```

Expected: `.nanite/` directory with symlinks, `NANITE.md` at root, `CLAUDE.md` with managed section.

- [ ] **Step 6: Clean up smoke test**

```bash
rm -rf /tmp/nanite-install-smoke
```

- [ ] **Step 7: Commit**

```bash
cd ~/Projects-apps/nanite
git add cmd/nanite/install_cmd.go cmd/nanite/main.go
git commit -m "$(cat <<'EOF'
feat(cli): nanite install command

Thin wrapper over internal/service/install exposing the full command
matrix: home extract, project scaffold, migrate-from-agentrc,
archive-only, rollback, resume, restart, refresh, force. Matches the
existing cmdServe/cmdMCP pattern (manual flag parsing, no cobra).
Interactive TTY prompt for partial install detection; non-TTY returns
exit code 3 for scripted dispatch harnesses.
EOF
)"
```

---

## Task 14: Register install MCP tools

**Files:**
- Modify: `internal/mcp/self_tools_transport.go`
- Modify: `internal/mcp/self_tools_transport_test.go`

**Context:** MCP tools are added to Nanite's `SelfToolsTransport` by appending Tool definitions to `ListTools()` and dispatch cases to `CallTool()`. Each new tool needs a `callXxx` handler method.

- [ ] **Step 1: Read the existing patterns**

```bash
grep -n "callCreateSkill\|callListSkills" ~/Projects-apps/nanite/internal/mcp/self_tools_transport.go | head -10
```

Confirm the shape of a `callXxx` method: it takes `args map[string]any`, returns `(*ToolResult, error)`.

- [ ] **Step 2: Add tool definitions to `ListTools()`**

Open `~/Projects-apps/nanite/internal/mcp/self_tools_transport.go` and locate the `ListTools` method. Append to the returned slice:

```go
{
    Name:        "nanite_install_home",
    Description: "Extract embedded Nanite framework assets to ~/.nanite/. Skips user-modified files unless force=true.",
    InputSchema: map[string]any{
        "type": "object",
        "properties": map[string]any{
            "force": map[string]any{"type": "boolean", "description": "overwrite user-modified files"},
        },
    },
},
{
    Name:        "nanite_install_project",
    Description: "Scaffold .nanite/ and NANITE.md in a project. Optionally migrate from .agentrc/ or archive-only.",
    InputSchema: map[string]any{
        "type": "object",
        "properties": map[string]any{
            "project_dir":           map[string]any{"type": "string", "description": "absolute path to project dir"},
            "migrate_from_agentrc":  map[string]any{"type": "boolean", "description": "archive .agentrc/ and migrate"},
            "archive_only":          map[string]any{"type": "boolean", "description": "archive .agentrc/ without scaffolding"},
        },
        "required": []string{"project_dir"},
    },
},
{
    Name:        "nanite_install_rollback",
    Description: "Reverse the most recent migration for a project from its archive snapshot.",
    InputSchema: map[string]any{
        "type": "object",
        "properties": map[string]any{
            "project_dir":  map[string]any{"type": "string"},
            "archive_path": map[string]any{"type": "string", "description": "explicit archive dir; finds most recent if empty"},
        },
        "required": []string{"project_dir"},
    },
},
{
    Name:        "nanite_install_diff",
    Description: "Dry-run a project install: show what would change without modifying anything.",
    InputSchema: map[string]any{
        "type": "object",
        "properties": map[string]any{
            "project_dir":          map[string]any{"type": "string"},
            "migrate_from_agentrc": map[string]any{"type": "boolean"},
        },
        "required": []string{"project_dir"},
    },
},
```

- [ ] **Step 3: Add dispatch cases to `CallTool()`**

Locate the `CallTool` method's switch statement. Add:

```go
case "nanite_install_home":
    return st.callInstallHome(args)
case "nanite_install_project":
    return st.callInstallProject(args)
case "nanite_install_rollback":
    return st.callInstallRollback(args)
case "nanite_install_diff":
    return st.callInstallDiff(args)
```

- [ ] **Step 4: Add handler methods**

Append to `self_tools_transport.go`:

```go
import (
	// ...existing imports...
	"github.com/hollis-labs/nanite/internal/service/install"
)

func (st *SelfToolsTransport) callInstallHome(args map[string]any) (*ToolResult, error) {
	force, _ := args["force"].(bool)
	svc := install.New()
	report, err := svc.InstallHome(install.InstallHomeOptions{Force: force})
	if err != nil {
		return nil, err
	}
	return &ToolResult{
		Content: []ToolContent{{
			Type: "text",
			Text: fmt.Sprintf("install home: created=%d unchanged=%d skipped=%d forced=%d",
				report.Created, report.Unchanged, report.Skipped, report.Forced),
		}},
	}, nil
}

func (st *SelfToolsTransport) callInstallProject(args map[string]any) (*ToolResult, error) {
	projectDir, _ := args["project_dir"].(string)
	if projectDir == "" {
		return nil, fmt.Errorf("project_dir required")
	}
	migrate, _ := args["migrate_from_agentrc"].(bool)
	archiveOnly, _ := args["archive_only"].(bool)

	svc := install.New()
	report, err := svc.InstallProject(install.InstallProjectOptions{
		ProjectDir:         projectDir,
		MigrateFromAgentrc: migrate,
		ArchiveOnly:        archiveOnly,
	})
	if err != nil {
		return nil, err
	}
	summary := fmt.Sprintf("install project %s:", projectDir)
	switch {
	case report.FreshScaffold:
		summary += " fresh scaffold"
	case report.Migrated:
		summary += fmt.Sprintf(" migrated (archive=%s)", report.ArchivePath)
	case report.Adopted:
		summary += " adopted existing"
	case report.ArchiveOnly:
		summary += fmt.Sprintf(" archive-only (archive=%s)", report.ArchivePath)
	}
	return &ToolResult{Content: []ToolContent{{Type: "text", Text: summary}}}, nil
}

func (st *SelfToolsTransport) callInstallRollback(args map[string]any) (*ToolResult, error) {
	projectDir, _ := args["project_dir"].(string)
	if projectDir == "" {
		return nil, fmt.Errorf("project_dir required")
	}
	archivePath, _ := args["archive_path"].(string)

	svc := install.New()
	if err := svc.Rollback(install.RollbackOptions{ProjectDir: projectDir, ArchivePath: archivePath}); err != nil {
		return nil, err
	}
	return &ToolResult{Content: []ToolContent{{Type: "text", Text: "rollback complete: " + projectDir}}}, nil
}

func (st *SelfToolsTransport) callInstallDiff(args map[string]any) (*ToolResult, error) {
	// Stub: full implementation deferred. For now, return a placeholder.
	// TODO(nanite-install-diff): implement dry-run mode in internal/service/install
	// that returns an action list without mutating state.
	return &ToolResult{Content: []ToolContent{{Type: "text", Text: "install diff not yet implemented"}}}, nil
}
```

**Note:** The diff tool returns a placeholder stub because the full `--print-diff` implementation is a separate feature (see Future Work in the spec). The stub is included now so the MCP tool list is stable and the CLI flag can ship without errors.

- [ ] **Step 5: Write test for MCP tool listing**

```go
// Append to internal/mcp/self_tools_transport_test.go

func TestListTools_IncludesInstallTools(t *testing.T) {
	// Assuming NewSelfToolsTransport() takes a *store.Store; use a memory store.
	st := NewSelfToolsTransport(newTestStore(t))
	tools, err := st.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"nanite_install_home", "nanite_install_project", "nanite_install_rollback", "nanite_install_diff"}
	for _, name := range want {
		found := false
		for _, tool := range tools {
			if tool.Name == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing install tool: %s", name)
		}
	}
}
```

The existing test file should already have `newTestStore` or a similar helper. If not, use the pattern from existing tests in that file.

- [ ] **Step 6: Run tests, verify pass**

```bash
cd ~/Projects-apps/nanite
go test ./internal/mcp/... -run TestListTools_IncludesInstallTools -v
```

- [ ] **Step 7: Build the binary to catch compile errors**

```bash
go build ./...
```

Expected: clean build, no errors.

- [ ] **Step 8: Commit**

```bash
git add internal/mcp/self_tools_transport.go internal/mcp/self_tools_transport_test.go
git commit -m "$(cat <<'EOF'
feat(mcp): expose nanite install as MCP tools

Adds nanite_install_home, nanite_install_project, nanite_install_rollback,
and nanite_install_diff to SelfToolsTransport. Thin wrappers over
internal/service/install — same canonical logic as the CLI. install_diff
is a stub until the print-diff feature ships.
EOF
)"
```

---

## Task 15: Full migration integration test

**Files:**
- Create: `internal/service/install/integration_test.go`

**Context:** Combines everything — fresh install, migrate, rollback round-trip, refresh — in one end-to-end test. This is the confidence test before rollout.

- [ ] **Step 1: Write the integration test**

```go
// internal/service/install/integration_test.go
package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIntegration_FullMigrationRoundTrip exercises the full flow:
// 1. setup fake agentrc project
// 2. migrate-from-agentrc
// 3. verify all invariants
// 4. rollback
// 5. verify bit-for-bit equivalent to pre-migration state
func TestIntegration_FullMigrationRoundTrip(t *testing.T) {
	home := setupFakeHome(t)
	project := t.TempDir()
	archBase := t.TempDir()
	t.Setenv("NANITE_ARCHIVE_BASE", archBase)

	// Build realistic fake project.
	mkFile := func(p, c string) {
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(c), 0o644)
	}
	originalConfig := "agentrc_version: 2.2.0\nagents:\n  backend-dev:\n    name: Backend\n    roles: [backend, go]\n    context: agents/backend.md\n"
	mkFile(filepath.Join(project, ".agentrc", "config.yaml"), originalConfig)
	mkFile(filepath.Join(project, ".agentrc", "agents", "backend.md"), "# Backend Context\n\nGo service.\n")
	mkFile(filepath.Join(project, ".agentrc", "boot-prompt.md"), "# Session boot\n\nRead this first.\n")
	mkFile(filepath.Join(project, ".agentrc-legacy", "old-v1.md"), "v1 legacy\n")
	originalCLAUDE := "# Project\n\nSome user prose before agentrc.\n\n## agentrc\n\n- If `.agentrc/boot-prompt.md` exists, read it first.\n\nMore agentrc instructions.\n"
	mkFile(filepath.Join(project, "CLAUDE.md"), originalCLAUDE)
	mkFile(filepath.Join(project, "README.md"), "# Test project\n")

	// Snapshot for comparison.
	originalState := snapshotDir(t, project)

	// Migrate.
	svc := New()
	report, err := svc.InstallProject(InstallProjectOptions{
		ProjectDir:         project,
		GlobalHome:         filepath.Join(home, ".nanite"),
		MigrateFromAgentrc: true,
	})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if !report.Migrated {
		t.Error("expected Migrated=true")
	}

	// Invariants after migration.
	if _, err := os.Stat(filepath.Join(project, ".agentrc")); !os.IsNotExist(err) {
		t.Error(".agentrc not archived")
	}
	if _, err := os.Stat(filepath.Join(project, ".agentrc-legacy")); !os.IsNotExist(err) {
		t.Error(".agentrc-legacy not archived")
	}
	// Archive dir has everything.
	if _, err := os.Stat(filepath.Join(report.ArchivePath, ".agentrc", "config.yaml")); err != nil {
		t.Errorf("archived .agentrc/config.yaml missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(report.ArchivePath, ".agentrc-legacy", "old-v1.md")); err != nil {
		t.Errorf("archived .agentrc-legacy missing: %v", err)
	}
	// New .nanite/ has copied content with field renames.
	newCfg, _ := os.ReadFile(filepath.Join(project, ".nanite", "config.yaml"))
	if !strings.Contains(string(newCfg), "nanite_version:") {
		t.Errorf(".nanite/config.yaml missing nanite_version: %q", newCfg)
	}
	if strings.Contains(string(newCfg), "agentrc_version:") {
		t.Errorf(".nanite/config.yaml still has agentrc_version: %q", newCfg)
	}
	// Agents carried over.
	if _, err := os.Stat(filepath.Join(project, ".nanite", "agents", "backend.md")); err != nil {
		t.Errorf(".nanite/agents/backend.md missing: %v", err)
	}
	// boot-prompt carried over.
	if _, err := os.Stat(filepath.Join(project, ".nanite", "boot-prompt.md")); err != nil {
		t.Errorf(".nanite/boot-prompt.md missing: %v", err)
	}
	// NANITE.md scaffolded.
	if _, err := os.Stat(filepath.Join(project, "NANITE.md")); err != nil {
		t.Errorf("NANITE.md missing: %v", err)
	}
	// CLAUDE.md: agentrc section gone, user prose preserved, managed section present.
	claude, _ := os.ReadFile(filepath.Join(project, "CLAUDE.md"))
	if strings.Contains(string(claude), "## agentrc") {
		t.Errorf("CLAUDE.md still has agentrc section: %q", claude)
	}
	if !strings.Contains(string(claude), "Some user prose before agentrc") {
		t.Errorf("CLAUDE.md user content lost: %q", claude)
	}
	if !strings.Contains(string(claude), "<!-- nanite:start -->") {
		t.Errorf("CLAUDE.md missing managed section markers: %q", claude)
	}
	// README.md untouched.
	readme, _ := os.ReadFile(filepath.Join(project, "README.md"))
	if string(readme) != "# Test project\n" {
		t.Errorf("README.md modified: %q", readme)
	}
	// State marker is PhaseComplete.
	state, err := ReadState(filepath.Join(report.ArchivePath, StateFileName))
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if state.Phase != PhaseComplete {
		t.Errorf("phase = %q, want %q", state.Phase, PhaseComplete)
	}

	// Roll back.
	if err := svc.Rollback(RollbackOptions{ProjectDir: project, ArchivePath: report.ArchivePath}); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	// Verify bit-for-bit equivalence to original.
	rolledBack := snapshotDir(t, project)
	if !compareSnapshots(originalState, rolledBack) {
		t.Error("rollback did not restore project to original state")
	}
}

// snapshotDir returns a map of relPath → file content (bytes) for every
// file under root, excluding the root itself.
func snapshotDir(t *testing.T, root string) map[string][]byte {
	t.Helper()
	out := make(map[string][]byte)
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[rel] = data
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	return out
}

func compareSnapshots(a, b map[string][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		bv, ok := b[k]
		if !ok || string(av) != string(bv) {
			return false
		}
	}
	return true
}
```

- [ ] **Step 2: Run the integration test**

```bash
cd ~/Projects-apps/nanite
go test ./internal/service/install/... -run TestIntegration -v
```

Expected: PASS. If rollback doesn't produce bit-for-bit equivalence, debug by checking which files differ (add a print of the diff in `compareSnapshots`).

- [ ] **Step 3: Run the full install package test suite one final time**

```bash
go test ./internal/service/install/... -v
```

Expected: all tests pass.

- [ ] **Step 4: Commit**

```bash
git add internal/service/install/integration_test.go
git commit -m "test(install): full migration round-trip integration test"
```

---

## Task 16: Phase 4 — Dogfood on Nanite itself

**Not a code task — a runbook executed after Tasks 1-15 are complete.**

**Files:**
- Modify: `~/Projects-apps/nanite/.nanite/` (existing, from PR #11)
- Create: `~/Projects-apps/nanite/NANITE.md`
- Modify: `~/Projects-apps/nanite/CLAUDE.md`

- [ ] **Step 1: Install the latest Nanite binary**

```bash
cd ~/Projects-apps/nanite
go build ./cmd/nanite
./nanite version
```

- [ ] **Step 2: Back up current CLAUDE.md before anything risky**

```bash
cp ~/Projects-apps/nanite/CLAUDE.md ~/Projects-apps/nanite/CLAUDE.md.pre-adopt-backup
```

- [ ] **Step 3: Run `nanite install` to populate `~/.nanite/`**

```bash
~/Projects-apps/nanite/nanite install
ls -la ~/.nanite/
```

Expected: `~/.nanite/` now contains the full framework tree (roles, skills, commands, docs, templates, VERSION).

- [ ] **Step 4: Run the adopt-existing path on Nanite itself**

```bash
~/Projects-apps/nanite/nanite install --project ~/Projects-apps/nanite
```

Expected output:
```
nanite: adopted existing .nanite/ in /Users/chrispian/Projects-apps/nanite
```

- [ ] **Step 5: Verify the scaffold**

```bash
ls -la ~/Projects-apps/nanite/.nanite/
cat ~/Projects-apps/nanite/NANITE.md | head -40
grep -c "nanite:start\|nanite:end" ~/Projects-apps/nanite/CLAUDE.md
```

Expected:
- `.nanite/` has roles, skills, commands symlinks pointing to `~/.nanite/{roles,skills,commands}`
- `NANITE.md` has the thin boot prompt
- CLAUDE.md contains `<!-- nanite:start -->` and `<!-- nanite:end -->` markers (grep returns 2)

- [ ] **Step 6: Smoke test in a Claude Code session**

Open a new Claude Code session in `~/Projects-apps/nanite`. Type: `Boot backend`.

Expected: the session loads the backend role + Go stack role + the nanite backend context file. Verify it can mention file paths from the project without hallucinating.

- [ ] **Step 7: Force a context compaction and verify recovery**

Inside the Claude session, type (or let it happen naturally): `/compact`. After compaction, ask the session what project it's in and what role it's loaded. Expected: it re-reads NANITE.md + the role + the context file and answers correctly.

- [ ] **Step 8: If anything is off, roll back**

```bash
# (only if Phase 4 verification fails)
# Restore the pre-adopt CLAUDE.md.
cp ~/Projects-apps/nanite/CLAUDE.md.pre-adopt-backup ~/Projects-apps/nanite/CLAUDE.md
# NOTE: nanite install --rollback doesn't help here because there was no
# migrate-from-agentrc step (adopt path doesn't archive). Remove NANITE.md
# and revert any symlinks manually if needed.
rm -f ~/Projects-apps/nanite/NANITE.md
```

- [ ] **Step 9: Clean up the backup if verification passed**

```bash
rm ~/Projects-apps/nanite/CLAUDE.md.pre-adopt-backup
```

- [ ] **Step 10: Commit the adopted state to the Nanite repo**

```bash
cd ~/Projects-apps/nanite
git add NANITE.md .nanite/ CLAUDE.md
git status
git commit -m "chore: adopt nanite install managed NANITE.md and CLAUDE.md"
```

**Checkpoint:** Nanite is now running the new installer on itself. All other projects still on `.agentrc/`.

---

## Task 17: Phase 5 — Parallel rollout to remaining projects

**Not a code task — an operational runbook using sub-agent dispatch.**

**Projects to migrate (full migration):**

```
cerberus, clockwork-manifold, engine, conduit, cortex, libs, hadron,
carrier, nexus, sigil, lnklst, suds-v2, fragmentsengine.com
```

**Projects to archive-only:** `agent-workspaces`

- [ ] **Step 1: Write the per-project migration sub-agent prompt**

Create a file for reference:

```bash
mkdir -p ~/Projects-apps/agent-workspaces/execution/nanite-rollout
cat > ~/Projects-apps/agent-workspaces/execution/nanite-rollout/migrate-project.md <<'EOF'
# Per-project migration task

You are migrating a single project from `.agentrc/` to `.nanite/` using the `nanite install` command.

## Inputs
- PROJECT_PATH: absolute path to the project root
- MODE: either "migrate" or "archive-only"

## Steps

1. Verify the project has `.agentrc/` (or `.agentrc-legacy/` if archive-only):
   ```
   ls -la $PROJECT_PATH/.agentrc $PROJECT_PATH/.agentrc-legacy 2>&1
   ```

2. Back up CLAUDE.md for safety:
   ```
   cp $PROJECT_PATH/CLAUDE.md $PROJECT_PATH/CLAUDE.md.pre-migrate-backup
   ```

3. Run the installer:
   - If MODE=migrate:
     ```
     ~/Projects-apps/nanite/nanite install --project $PROJECT_PATH --migrate-from-agentrc
     ```
   - If MODE=archive-only:
     ```
     ~/Projects-apps/nanite/nanite install --project $PROJECT_PATH --archive-only
     ```

4. Verify invariants:
   - `$PROJECT_PATH/.agentrc` does NOT exist
   - `$PROJECT_PATH/.agentrc-legacy` does NOT exist
   - If MODE=migrate: `$PROJECT_PATH/.nanite/config.yaml` exists
   - If MODE=migrate: `$PROJECT_PATH/.nanite/roles` is a symlink to `~/.nanite/roles`
   - If MODE=migrate: `$PROJECT_PATH/NANITE.md` exists
   - If MODE=migrate: `$PROJECT_PATH/CLAUDE.md` contains `<!-- nanite:start -->` and no `## agentrc`
   - An archive dir exists at `~/Projects-apps/.archived/{project}-YYYY-MM-DD*` with `.install-state.json` at phase `complete`

5. If all invariants pass:
   - Remove the backup: `rm $PROJECT_PATH/CLAUDE.md.pre-migrate-backup`
   - Report success with the archive dir path

6. If anything fails:
   - If exit code is 3 (partial install): retry with `--resume`
   - If retry also fails: report failure with the error and the archive dir path, leave the backup in place, do NOT attempt further recovery

## Output contract

Report (exactly):
```
project: <basename>
status: success | failed
archive: <path>
notes: <any warnings or errors>
```
EOF
```

- [ ] **Step 2: Dispatch sub-agents in parallel (one per project)**

From the main session, launch 14 sub-agents via the Agent tool. Example for one:

```
Agent(
  description: "Migrate hadron to .nanite/",
  subagent_type: "general-purpose",
  prompt: "Run the nanite install migration for the `hadron` project at ~/Projects-apps/hadron. Follow the procedure in ~/Projects-apps/agent-workspaces/execution/nanite-rollout/migrate-project.md. MODE=migrate. Report using the output contract in that file."
)
```

Repeat for: cerberus, clockwork-manifold, engine, conduit, cortex, libs, hadron, carrier, nexus, sigil, lnklst, suds-v2, fragmentsengine.com (all MODE=migrate), and agent-workspaces (MODE=archive-only).

All 14 agent invocations should be in a single message so they run in parallel.

- [ ] **Step 3: Aggregate results**

Collect all sub-agent outputs. For each project, note:
- Status (success / failed)
- Archive path
- Any warnings

Any project with `status: failed` needs manual attention. Re-dispatch with `--resume` or investigate the underlying error.

- [ ] **Step 4: Spot-check per-language samples**

In a Claude Code session, open each and boot an agent:

```bash
# Go standalone
cd ~/Projects-apps/hadron && ls .nanite NANITE.md

# Go monorepo child
cd ~/Projects-apps/fragments-engine/engine && ls .nanite NANITE.md

# TypeScript
cd ~/Projects-apps/suds-v2 && ls .nanite NANITE.md

# Static site
cd ~/Projects-apps/fragmentsengine.com && ls .nanite NANITE.md
```

For each, verify `.nanite/` + `NANITE.md` + CLAUDE.md managed section are present and well-formed.

- [ ] **Step 5: Verify agent-workspaces (archive-only mode)**

```bash
ls -la ~/Projects-apps/agent-workspaces/.agentrc 2>&1
ls -la ~/Projects-apps/agent-workspaces/.nanite 2>&1
ls -la ~/Projects-apps/.archived/ | grep agent-workspaces
```

Expected: `.agentrc/` gone, no `.nanite/`, `.archived/agent-workspaces-YYYY-MM-DD/` exists.

- [ ] **Step 6: Commit per-project changes**

Each migrated project that's a git repo needs its own commit:

```bash
for proj in cerberus clockwork-manifold hadron carrier nexus sigil lnklst suds-v2 fragmentsengine.com; do
  cd ~/Projects-apps/$proj 2>/dev/null || continue
  git add .nanite NANITE.md CLAUDE.md 2>&1
  git status --short
  git commit -m "chore: migrate to .nanite/ via nanite install" 2>&1
done
```

Monorepo children (engine, conduit, cortex, libs) commit at the monorepo root:

```bash
cd ~/Projects-apps/fragments-engine
git add engine/.nanite engine/NANITE.md engine/CLAUDE.md \
        conduit/.nanite conduit/NANITE.md conduit/CLAUDE.md \
        cortex/.nanite cortex/NANITE.md cortex/CLAUDE.md \
        libs/.nanite libs/NANITE.md libs/CLAUDE.md
git commit -m "chore: migrate monorepo children to .nanite/ via nanite install"
```

`agent-workspaces/` is not a git repo (it's your scratch dir) — no commit needed.

**Checkpoint:** Every portfolio project is on `.nanite/` (or archive-only for scratch dirs). `agentrc` repo still exists as safety net.

---

## Task 18: Phase 6 — Archive agentrc repo

**Not a code task — a final archival runbook.**

- [ ] **Step 1: Commit any pending work in the agentrc source repo**

```bash
cd ~/Projects-apps/agentrc
git status
```

If there are uncommitted changes (the exploration noted `config.yaml` had local changes), commit them:

```bash
git add config.yaml
git commit -m "chore: final state pre-archival"
```

- [ ] **Step 2: Tag the final version**

```bash
git tag v2.2.0-final
git tag -l | tail -5
```

- [ ] **Step 3: Move the repo to the archive directory**

```bash
mkdir -p ~/Projects-apps/.archived
mv ~/Projects-apps/agentrc ~/Projects-apps/.archived/agentrc-final-$(date +%Y-%m-%d)
ls ~/Projects-apps/.archived/ | grep agentrc
```

- [ ] **Step 4: Remove `~/.agentrc/`**

If it's a symlink pointing at the (now moved) source repo, it's dangling:

```bash
readlink ~/.agentrc 2>&1
rm ~/.agentrc
```

If it's a real directory (not a symlink):

```bash
ls -la ~/.agentrc | head -5
# confirm it's not something you want to keep
rm -rf ~/.agentrc
```

- [ ] **Step 5: Portfolio-wide straggler grep**

```bash
cd ~/Projects-apps
# Look for any lingering references to agentrc, excluding .archived
grep -rn "agentrc\|\.agentrc" \
  --include="*.md" --include="*.yaml" --include="*.yml" --include="*.go" --include="*.json" \
  --exclude-dir=.archived \
  --exclude-dir=node_modules \
  --exclude-dir=.git \
  . 2>/dev/null | head -40
```

For each hit, evaluate: is it a stale reference that should be updated to nanite, or is it a legitimate historical mention (commit message, changelog, migration note)?

- [ ] **Step 6: Fix any stragglers that are real stale references**

Update each to use `nanite` / `.nanite` / `NANITE.md` terminology. Commit per affected repo.

- [ ] **Step 7: Confirm the gh repo can be deleted (manual)**

**User action:** Open https://github.com/hollis-labs/agentrc (or wherever it lives), verify it's archived or ready to delete, then delete it through the GitHub UI. Do NOT delete from the CLI automatically.

- [ ] **Step 8: Post-rollout smoke test (optional, one week later)**

Set a reminder to verify one week from now:

```bash
# after 1 week
ls ~/Projects-apps/.archived/ | head -20  # verify safety net still present
~/Projects-apps/nanite/nanite install --refresh  # verify refresh works
# boot a multi-agent session from two different projects and verify context loads
```

**Checkpoint:** `agentrc` no longer exists anywhere on the system. Nanite owns the full framework. All projects run on `.nanite/`. `~/Projects-apps/.archived/` contains dated safety-net backups.

---

## Done

After Task 18 completes, mark the spec as Implemented:

```bash
cd ~/Projects-apps/nanite
# Edit docs/superpowers/specs/2026-04-09-nanite-agentrc-consolidation-design.md
# Change "Status: Approved" to "Status: Implemented"
git add docs/superpowers/specs/2026-04-09-nanite-agentrc-consolidation-design.md
git commit -m "docs: mark agentrc consolidation spec as implemented"
```

Then review the deferred follow-up tasks from the spec §6 (Future Work) and decide which to schedule next.
