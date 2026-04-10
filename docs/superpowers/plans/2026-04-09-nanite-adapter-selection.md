# Nanite Adapter Selection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make CLI adapter selection (CLAUDE.md / AGENTS.md / GEMINI.md / OPENCODE.md) user-controlled and explicit. Remove the legacy direct CLAUDE.md write. Add detection + interactive prompt + persistence + cleanup-on-removal.

**Architecture:** A new `adapters: [...]` key in `.nanite/config.yaml` (tri-state: absent / empty / populated) is the source of truth for which CLI adapters Nanite manages files for. Install resolves the list via priority order: `--no-adapters` flag → `--adapters` flag → config (unless `--reconfigure`) → detect-then-prompt (or detect-only when non-interactive). The four built-in CLI adapters lose their empty-agents short-circuit and write a placeholder section instead. A new cleanup pass strips and (if empty) deletes managed sections from adapters that were removed from the list.

**Tech Stack:** Go 1.22+, `gopkg.in/yaml.v3`, standard library `flag`/`fmt`/`os`/`io`. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-04-09-nanite-adapter-selection-design.md`

---

## Pre-flight

The worktree is already set up at `~/Projects-apps/nanite-adapter-selection` on branch `feature/adapter-selection`. The gitignored `plugins/support-ticket/` has been copied. Verify build:

```bash
cd ~/Projects-apps/nanite-adapter-selection
go build -o /tmp/nanite-feature ./cmd/nanite
go test ./...
```

Both should succeed before starting Task 1.

---

## File Structure

### New files

| Path | Responsibility |
|---|---|
| `internal/service/install/adapter_detect.go` | `DetectAdapters(projectDir) []string` — scan for evidence (root files + `.cli/` dirs) |
| `internal/service/install/adapter_detect_test.go` | Table-driven tests across all evidence combinations |
| `internal/service/install/adapter_select.go` | `ResolveAdapters(cfg, projectDir, opts) (resolved, previous, error)` — resolution priority order |
| `internal/service/install/adapter_select_test.go` | Priority order tests with injected `io.Reader`/`io.Writer` |
| `internal/service/install/adapter_cleanup.go` | `cleanupRemovedAdapters(projectDir, removed) ([]CleanupReport, error)` — strip + delete-if-empty |
| `internal/service/install/adapter_cleanup_test.go` | Edge case tests |
| `internal/service/install/adapter_persist.go` | `persistAdapterList(projectDir, adapters) error` — YAML round-trip the `adapters:` key |
| `internal/service/install/adapter_persist_test.go` | Round-trip tests preserving other config keys |

### Modified files

| Path | Change |
|---|---|
| `internal/service/install/adapters.go` | Add `Adapters *[]string` to `projectConfig` (tri-state). Add `loadProjectConfig()` helper that returns `*projectConfig` distinguishing nil from empty. Change `syncAdaptersForProject` signature to take `allowedAdapters []string` and use the new filtered registry method. |
| `internal/agent/adapter.go` | Add `RemoveManagedSection(path) (removedAny bool, becameEmpty bool, err error)` helper to `managed_section.go`. Add `SyncAllProjectRootsFiltered(projectDir, agents, allowed []string)` method to `AdapterRegistry` (in `adapter.go`). |
| `internal/agent/managed_section.go` | Add `RemoveManagedSection(path)` helper. |
| `internal/service/install/install.go` | Remove `UpdateCLAUDEmd` call from `freshScaffold`. Wire `ResolveAdapters → cleanup → persist → sync` into `freshScaffold`. Extend `InstallProjectReport` and `InstallProjectOptions` for new fields. Plumb new options through `InstallProject`. |
| `internal/service/install/adopt.go` | Same wiring (without snapshot — adopt has no archive). |
| `internal/service/install/migrate.go` | Same wiring (with snapshot via existing archive). Preserve phase markers. |
| `internal/plugin/builtin/adapter-claude/plugin.go` | Remove short-circuit, add `placeholderContent` const, write placeholder when agents empty. |
| `internal/plugin/builtin/adapter-codex/plugin.go` | Same. |
| `internal/plugin/builtin/adapter-gemini/plugin.go` | Same. |
| `internal/plugin/builtin/adapter-opencode/plugin.go` | Same. |
| `cmd/nanite/install_cmd.go` | Add `--adapters`, `--no-adapters`, `--reconfigure` flags. Add `promptAdapterSelection` function. Wire flag values into `InstallProjectOptions`. Add output summary line. |
| `internal/assets/framework/templates/NANITE.md.tmpl` | Add "First-time setup" block. |

### Existing tests to update

| Path | Change |
|---|---|
| `internal/service/install/integration_test.go` | Fresh-install assertions: now expect `adapters:` key in config, no `CLAUDE.md` unless detected. Add migration scenario tests. |
| `internal/service/install/claudemd_test.go` | Remove integration tests of legacy direct path; keep `UpdateCLAUDEmd` helper-level tests. |
| `internal/service/install/adapters_test.go` | Extend with the filter parameter. |
| `internal/plugin/builtin/adapter-claude/plugin_test.go` | Add placeholder content tests. |
| `internal/plugin/builtin/adapter-codex/plugin_test.go` | Same. |
| `internal/plugin/builtin/adapter-gemini/plugin_test.go` | Same. |
| `internal/plugin/builtin/adapter-opencode/plugin_test.go` | Same. |

---

## Task 1: Tri-state config schema

**Files:**
- Modify: `internal/service/install/adapters.go:55-62` (the `projectConfig` struct)
- Create: `internal/service/install/adapter_persist.go`
- Create: `internal/service/install/adapter_persist_test.go`

The current `projectConfig` only knows about `Agents`. We add `Adapters *[]string` so the YAML parser can distinguish "key absent" (nil) from "key present but empty" (`&[]string{}`) from "key present with values".

We also add a `loadProjectConfig(path string) (*projectConfig, error)` helper that reads and parses, distinguishing missing-file from parse-error from empty-file.

- [ ] **Step 1: Write the failing test for tri-state parsing**

Create `internal/service/install/adapter_persist_test.go`:

```go
package install

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadProjectConfig_TriStateAdapters(t *testing.T) {
	cases := []struct {
		name        string
		yaml        string
		wantNil     bool
		wantLen     int
		wantContent []string
	}{
		{
			name:    "key absent",
			yaml:    "nanite_version: 2.3.0\nagents: {}\n",
			wantNil: true,
		},
		{
			name:    "key present empty",
			yaml:    "nanite_version: 2.3.0\nadapters: []\nagents: {}\n",
			wantNil: false,
			wantLen: 0,
		},
		{
			name:        "key present populated",
			yaml:        "nanite_version: 2.3.0\nadapters:\n  - claude\n  - codex\nagents: {}\n",
			wantNil:     false,
			wantLen:     2,
			wantContent: []string{"claude", "codex"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.yaml")
			if err := os.WriteFile(path, []byte(tc.yaml), 0o644); err != nil {
				t.Fatal(err)
			}
			cfg, err := loadProjectConfig(path)
			if err != nil {
				t.Fatalf("loadProjectConfig: %v", err)
			}
			if tc.wantNil {
				if cfg.Adapters != nil {
					t.Fatalf("expected nil Adapters, got %v", *cfg.Adapters)
				}
				return
			}
			if cfg.Adapters == nil {
				t.Fatal("expected non-nil Adapters")
			}
			if len(*cfg.Adapters) != tc.wantLen {
				t.Fatalf("len: got %d, want %d", len(*cfg.Adapters), tc.wantLen)
			}
			for i, want := range tc.wantContent {
				if (*cfg.Adapters)[i] != want {
					t.Errorf("[%d]: got %q, want %q", i, (*cfg.Adapters)[i], want)
				}
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd ~/Projects-apps/nanite-adapter-selection
go test ./internal/service/install/ -run TestLoadProjectConfig_TriStateAdapters
```

Expected: FAIL with "loadProjectConfig undefined" or similar.

- [ ] **Step 3: Modify `projectConfig` struct in `internal/service/install/adapters.go`**

Replace lines 55-62 with:

```go
// projectConfig is the minimal subset of .nanite/config.yaml that the
// install service needs in order to extract an agents list for adapter
// sync and to read/write the adapters selection list. We define our own
// struct (rather than reusing the one in adapter-nanite-native) to avoid
// coupling.
type projectConfig struct {
	Adapters *[]string               `yaml:"adapters,omitempty"`
	Agents   map[string]projectAgent `yaml:"agents"`
}

type projectAgent struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}
```

The pointer-to-slice is the trick that lets YAML distinguish "key absent" (nil pointer) from "key present, empty list" (non-nil pointer to zero-len slice).

- [ ] **Step 4: Create `internal/service/install/adapter_persist.go` with `loadProjectConfig`**

```go
package install

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"gopkg.in/yaml.v3"
)

// loadProjectConfig reads and parses the project's .nanite/config.yaml
// file, returning a *projectConfig that preserves the tri-state nature
// of the Adapters field (nil = absent, empty pointer = explicit none,
// populated = active list).
//
// Returns an empty *projectConfig (no error) if the file doesn't exist
// — the caller treats this the same as a fresh install.
func loadProjectConfig(path string) (*projectConfig, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &projectConfig{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read project config %s: %w", path, err)
	}
	var cfg projectConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse project config %s: %w", path, err)
	}
	return &cfg, nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

```bash
go test ./internal/service/install/ -run TestLoadProjectConfig_TriStateAdapters -v
```

Expected: PASS for all three cases.

Also run the existing install tests to confirm the struct change didn't break anything:

```bash
go test ./internal/service/install/...
```

Expected: PASS (the new `Adapters` field is optional and `extractAgentsFromConfig` doesn't read it).

- [ ] **Step 6: Commit**

```bash
cd ~/Projects-apps/nanite-adapter-selection
git add internal/service/install/adapters.go internal/service/install/adapter_persist.go internal/service/install/adapter_persist_test.go
git commit -m "$(cat <<'EOF'
feat(install): tri-state Adapters field in projectConfig

Add Adapters *[]string to projectConfig and a loadProjectConfig helper
that distinguishes nil (key absent) from empty (key present, no items)
from populated (key present with items). This is the foundation for
the new adapter selection flow.

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: DetectAdapters function

**Files:**
- Create: `internal/service/install/adapter_detect.go`
- Create: `internal/service/install/adapter_detect_test.go`

`DetectAdapters` scans a project directory for evidence that one or more CLI tools are in use, returning a deterministic slug list. Each adapter has two evidence signals: a project-root markdown file or a `.cli/` subdirectory.

- [ ] **Step 1: Write the failing test**

Create `internal/service/install/adapter_detect_test.go`:

```go
package install

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestDetectAdapters(t *testing.T) {
	cases := []struct {
		name  string
		setup func(dir string)
		want  []string
	}{
		{
			name:  "no evidence",
			setup: func(dir string) {},
			want:  nil,
		},
		{
			name: "CLAUDE.md only",
			setup: func(dir string) {
				_ = os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# hi"), 0o644)
			},
			want: []string{"claude"},
		},
		{
			name: ".claude/ dir only",
			setup: func(dir string) {
				_ = os.MkdirAll(filepath.Join(dir, ".claude"), 0o755)
			},
			want: []string{"claude"},
		},
		{
			name: "all four root files",
			setup: func(dir string) {
				for _, f := range []string{"CLAUDE.md", "AGENTS.md", "GEMINI.md", "OPENCODE.md"} {
					_ = os.WriteFile(filepath.Join(dir, f), []byte("# hi"), 0o644)
				}
			},
			want: []string{"claude", "codex", "gemini", "opencode"},
		},
		{
			name: "AGENTS.md and .gemini/ only",
			setup: func(dir string) {
				_ = os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# hi"), 0o644)
				_ = os.MkdirAll(filepath.Join(dir, ".gemini"), 0o755)
			},
			want: []string{"codex", "gemini"},
		},
		{
			name: "both file and dir for one adapter — counts once",
			setup: func(dir string) {
				_ = os.WriteFile(filepath.Join(dir, "OPENCODE.md"), []byte("# hi"), 0o644)
				_ = os.MkdirAll(filepath.Join(dir, ".opencode"), 0o755)
			},
			want: []string{"opencode"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.setup(dir)
			got := DetectAdapters(dir)
			sort.Strings(got)
			sort.Strings(tc.want)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/service/install/ -run TestDetectAdapters
```

Expected: FAIL with "DetectAdapters undefined".

- [ ] **Step 3: Create `internal/service/install/adapter_detect.go`**

```go
package install

import (
	"os"
	"path/filepath"
)

// adapterEvidence maps adapter slugs to the project-root paths whose
// presence indicates that adapter's CLI tool is in use. Both a markdown
// file and a dot-directory are checked; either one triggers detection.
//
// nanite-native is intentionally absent — it's not a CLI integration.
// It manages .nanite/ and NANITE.md, which are always Nanite's own.
var adapterEvidence = map[string]struct {
	rootFile string
	dotDir   string
}{
	"claude":   {"CLAUDE.md", ".claude"},
	"codex":    {"AGENTS.md", ".codex"},
	"gemini":   {"GEMINI.md", ".gemini"},
	"opencode": {"OPENCODE.md", ".opencode"},
}

// DetectAdapters scans projectDir for filesystem evidence of CLI tools
// (project-root markdown files or .cli/ subdirectories) and returns the
// slugs of adapters that should be considered "in use" for this project.
//
// Each adapter is checked once. The order of returned slugs is not
// guaranteed — callers should sort if they need determinism.
func DetectAdapters(projectDir string) []string {
	var found []string
	for slug, evidence := range adapterEvidence {
		if hasFile(filepath.Join(projectDir, evidence.rootFile)) ||
			hasDir(filepath.Join(projectDir, evidence.dotDir)) {
			found = append(found, slug)
		}
	}
	return found
}

func hasFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func hasDir(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/service/install/ -run TestDetectAdapters -v
```

Expected: PASS for all six cases.

- [ ] **Step 5: Commit**

```bash
git add internal/service/install/adapter_detect.go internal/service/install/adapter_detect_test.go
git commit -m "$(cat <<'EOF'
feat(install): DetectAdapters scans project for CLI tool evidence

New helper that walks a project root looking for adapter-specific
markdown files (CLAUDE.md, AGENTS.md, GEMINI.md, OPENCODE.md) and
dot-directories (.claude/, .codex/, .gemini/, .opencode/). Either
signal triggers detection. Used by ResolveAdapters to suggest the
default selection on first install.

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: RemoveManagedSection helper in internal/agent

**Files:**
- Modify: `internal/agent/managed_section.go` (add new function)
- Modify: `internal/agent/managed_section_test.go` (add new test)

The cleanup logic needs to strip a managed section from a file, preserving any user content outside the markers. The existing `WriteManagedSection` writes; we need a complementary `RemoveManagedSection` that removes. It returns whether the section existed and whether the file became empty after removal (so the caller can decide whether to delete it).

- [ ] **Step 1: Write the failing test**

Append to `internal/agent/managed_section_test.go`:

```go
func TestRemoveManagedSection_FileMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing.md")
	removed, becameEmpty, err := RemoveManagedSection(path)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if removed {
		t.Errorf("expected removed=false")
	}
	if becameEmpty {
		t.Errorf("expected becameEmpty=false")
	}
}

func TestRemoveManagedSection_NoMarkers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.md")
	original := "# user content\n\nhello\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	removed, becameEmpty, err := RemoveManagedSection(path)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if removed {
		t.Errorf("expected removed=false (no markers)")
	}
	if becameEmpty {
		t.Errorf("expected becameEmpty=false")
	}
	got, _ := os.ReadFile(path)
	if string(got) != original {
		t.Errorf("file should be untouched, got %q want %q", string(got), original)
	}
}

func TestRemoveManagedSection_PreservesOutsideContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.md")
	if err := WriteManagedSection(path, "managed body"); err != nil {
		t.Fatal(err)
	}
	// Prepend user content
	existing, _ := os.ReadFile(path)
	full := "# user header\n\nuser intro\n\n" + string(existing) + "\n# user footer\n"
	if err := os.WriteFile(path, []byte(full), 0o644); err != nil {
		t.Fatal(err)
	}
	removed, becameEmpty, err := RemoveManagedSection(path)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !removed {
		t.Errorf("expected removed=true")
	}
	if becameEmpty {
		t.Errorf("expected becameEmpty=false (user content remains)")
	}
	got, _ := os.ReadFile(path)
	gotStr := string(got)
	if !strings.Contains(gotStr, "user header") {
		t.Errorf("missing user header in %q", gotStr)
	}
	if !strings.Contains(gotStr, "user intro") {
		t.Errorf("missing user intro in %q", gotStr)
	}
	if !strings.Contains(gotStr, "user footer") {
		t.Errorf("missing user footer in %q", gotStr)
	}
	if strings.Contains(gotStr, managedStart) {
		t.Errorf("managed start marker still present in %q", gotStr)
	}
	if strings.Contains(gotStr, managedEnd) {
		t.Errorf("managed end marker still present in %q", gotStr)
	}
}

func TestRemoveManagedSection_EmptyAfterRemoval(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.md")
	if err := WriteManagedSection(path, "managed body"); err != nil {
		t.Fatal(err)
	}
	removed, becameEmpty, err := RemoveManagedSection(path)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !removed {
		t.Errorf("expected removed=true")
	}
	if !becameEmpty {
		t.Errorf("expected becameEmpty=true (file was managed-section-only)")
	}
}
```

The test uses `strings.Contains` and the existing `managedStart`/`managedEnd` package constants, so add the import if missing:

```go
import "strings"
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/agent/ -run TestRemoveManagedSection
```

Expected: FAIL with "RemoveManagedSection undefined".

- [ ] **Step 3: Add `RemoveManagedSection` to `internal/agent/managed_section.go`**

Append to the file (after `ReadManagedSection`):

```go
// RemoveManagedSection strips the Nanite-managed section (markers and
// content between them) from the file at path, preserving any user
// content outside the markers.
//
// Returns:
//   - removedAny: true if a managed section was found and removed
//   - becameEmpty: true if the file is empty (or whitespace-only) after removal
//   - err: I/O or parse errors
//
// If the file does not exist, returns (false, false, nil) — no-op.
// If the file has no managed-section markers, returns (false, false, nil)
// and leaves the file untouched.
func RemoveManagedSection(path string) (removedAny bool, becameEmpty bool, err error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}

	existing := string(data)

	startIdx := strings.Index(existing, managedStart)
	if startIdx == -1 {
		return false, false, nil
	}

	endIdx := strings.Index(existing[startIdx:], managedEnd)
	if endIdx == -1 {
		// Start marker without end marker — leave the file alone (we don't
		// know where the section ends, so we can't safely strip it).
		return false, false, nil
	}
	endIdx += startIdx + len(managedEnd)

	before := existing[:startIdx]
	after := existing[endIdx:]

	// Trim a single leading newline from `after` so we don't accumulate
	// blank lines, mirroring WriteManagedSection's behavior.
	after = strings.TrimPrefix(after, "\n")

	// Trim trailing whitespace from `before` so we don't leave dangling
	// blank lines either.
	before = strings.TrimRight(before, "\n")
	if before != "" && after != "" {
		before += "\n\n"
	} else if before != "" {
		before += "\n"
	}

	combined := before + after
	trimmed := strings.TrimSpace(combined)
	if trimmed == "" {
		// Whole file is empty after removal.
		if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
			return false, false, fmt.Errorf("truncate %s: %w", path, err)
		}
		return true, true, nil
	}

	if err := os.WriteFile(path, []byte(combined), 0o644); err != nil {
		return false, false, fmt.Errorf("write %s: %w", path, err)
	}
	return true, false, nil
}
```

This requires `fmt` in the import block — add it if not already there.

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/agent/ -run TestRemoveManagedSection -v
```

Expected: PASS for all four cases.

Run the full agent package tests to confirm no regressions:

```bash
go test ./internal/agent/...
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/managed_section.go internal/agent/managed_section_test.go
git commit -m "$(cat <<'EOF'
feat(agent): add RemoveManagedSection helper

Strips the <!-- nanite:start --> ... <!-- nanite:end --> block from a
markdown file, preserving any user content outside the markers. Returns
whether a section was found and whether the file became empty after
removal so the caller can decide whether to delete it.

Used by the install service's adapter cleanup pass to remove managed
sections when an adapter is dropped from a project's adapters: list.

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: cleanupRemovedAdapters function

**Files:**
- Create: `internal/service/install/adapter_cleanup.go`
- Create: `internal/service/install/adapter_cleanup_test.go`

Walks a list of removed adapter slugs, calls `RemoveManagedSection` on each adapter's target file, deletes the file if it became empty, and returns a report describing what happened. Reused by the install paths to handle `--reconfigure` shrinking and migration scenario B.

- [ ] **Step 1: Write the failing tests**

Create `internal/service/install/adapter_cleanup_test.go`:

```go
package install

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/internal/agent"
)

func TestCleanupRemovedAdapters_FileNotPresent(t *testing.T) {
	dir := t.TempDir()
	reports, err := cleanupRemovedAdapters(dir, []string{"claude"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(reports) != 0 {
		t.Errorf("expected 0 reports for missing file, got %d", len(reports))
	}
}

func TestCleanupRemovedAdapters_StripsAndPreservesContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")
	if err := agent.WriteManagedSection(path, "managed body"); err != nil {
		t.Fatal(err)
	}
	// Add user content outside the markers
	existing, _ := os.ReadFile(path)
	combined := "# user content\n\n" + string(existing)
	if err := os.WriteFile(path, []byte(combined), 0o644); err != nil {
		t.Fatal(err)
	}

	reports, err := cleanupRemovedAdapters(dir, []string{"codex"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(reports) != 1 {
		t.Fatalf("expected 1 report, got %d", len(reports))
	}
	if reports[0].Adapter != "codex" {
		t.Errorf("Adapter: got %q, want codex", reports[0].Adapter)
	}
	if reports[0].Action != "stripped" {
		t.Errorf("Action: got %q, want stripped", reports[0].Action)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("AGENTS.md should still exist: %v", err)
	}
	got, _ := os.ReadFile(path)
	if !contains(string(got), "user content") {
		t.Errorf("user content missing from %q", string(got))
	}
}

func TestCleanupRemovedAdapters_DeletesEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "GEMINI.md")
	if err := agent.WriteManagedSection(path, "managed body"); err != nil {
		t.Fatal(err)
	}

	reports, err := cleanupRemovedAdapters(dir, []string{"gemini"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(reports) != 1 {
		t.Fatalf("expected 1 report, got %d", len(reports))
	}
	if reports[0].Action != "deleted" {
		t.Errorf("Action: got %q, want deleted", reports[0].Action)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected GEMINI.md to be deleted, stat err: %v", err)
	}
}

func TestCleanupRemovedAdapters_MultipleAdapters(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"CLAUDE.md", "AGENTS.md", "OPENCODE.md"} {
		if err := agent.WriteManagedSection(filepath.Join(dir, f), "body"); err != nil {
			t.Fatal(err)
		}
	}

	reports, err := cleanupRemovedAdapters(dir, []string{"claude", "codex", "opencode"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(reports) != 3 {
		t.Fatalf("expected 3 reports, got %d", len(reports))
	}
	for _, r := range reports {
		if r.Action != "deleted" {
			t.Errorf("expected deleted, got %q for %q", r.Action, r.Adapter)
		}
	}
}

func TestCleanupRemovedAdapters_UnknownAdapterIgnored(t *testing.T) {
	dir := t.TempDir()
	reports, err := cleanupRemovedAdapters(dir, []string{"frobnicate"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(reports) != 0 {
		t.Errorf("expected 0 reports for unknown adapter, got %d", len(reports))
	}
}

// contains is a tiny strings.Contains helper to avoid pulling in the
// strings package at the top of every test file.
func contains(s, substr string) bool {
	return len(substr) == 0 || (len(s) >= len(substr) && indexOf(s, substr) >= 0)
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
```

(Use `strings.Contains` directly instead of the `contains`/`indexOf` helpers if `strings` is already imported in the file — either way works. The plan keeps them inline to make the test file self-contained.)

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/service/install/ -run TestCleanupRemovedAdapters
```

Expected: FAIL with "cleanupRemovedAdapters undefined".

- [ ] **Step 3: Create `internal/service/install/adapter_cleanup.go`**

```go
package install

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hollis-labs/nanite/internal/agent"
)

// CleanupReport describes what happened to one adapter's target file
// during cleanupRemovedAdapters. Returned to the caller (install command)
// so it can print user-facing notices.
type CleanupReport struct {
	Adapter  string // adapter slug, e.g., "claude"
	FilePath string // absolute path of the file that was modified
	Action   string // "stripped" (managed section removed, file kept) or "deleted" (file removed entirely)
}

// cleanupRemovedAdapters strips the Nanite-managed section from each
// removed adapter's target file in projectDir. If a file is empty after
// the strip (was managed-section-only), the file is deleted.
//
// Adapters not in the evidence map (e.g., "frobnicate") are silently
// ignored — slug validation happens upstream in ResolveAdapters.
//
// Returns one CleanupReport per adapter that had visible work done.
// Adapters whose target file was already missing are skipped without a
// report.
//
// All errors are I/O errors and propagate immediately. Snapshot for
// rollback is handled by the existing snapshotAdapterTargets() pass that
// runs before cleanup.
func cleanupRemovedAdapters(projectDir string, removed []string) ([]CleanupReport, error) {
	var reports []CleanupReport
	for _, slug := range removed {
		evidence, ok := adapterEvidence[slug]
		if !ok {
			continue
		}
		path := filepath.Join(projectDir, evidence.rootFile)
		stripped, becameEmpty, err := agent.RemoveManagedSection(path)
		if err != nil {
			return reports, fmt.Errorf("cleanup %s: %w", slug, err)
		}
		if !stripped {
			continue
		}
		report := CleanupReport{Adapter: slug, FilePath: path, Action: "stripped"}
		if becameEmpty {
			if err := os.Remove(path); err != nil {
				return reports, fmt.Errorf("delete empty %s: %w", path, err)
			}
			report.Action = "deleted"
		}
		reports = append(reports, report)
	}
	return reports, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/service/install/ -run TestCleanupRemovedAdapters -v
```

Expected: PASS for all five cases.

- [ ] **Step 5: Commit**

```bash
git add internal/service/install/adapter_cleanup.go internal/service/install/adapter_cleanup_test.go
git commit -m "$(cat <<'EOF'
feat(install): cleanupRemovedAdapters strips managed sections

When an adapter is dropped from a project's adapters: list (via
--reconfigure or migration decline), strip its managed section from the
corresponding root file (CLAUDE.md / AGENTS.md / GEMINI.md / OPENCODE.md).
If the file is empty after the strip, delete it. Returns CleanupReport
slice for the install command to print user-facing notices.

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: SyncAllProjectRootsFiltered registry method

**Files:**
- Modify: `internal/agent/adapter.go` (add new method after `SyncAllProjectRoots`)
- Modify: `internal/agent/adapter_test.go` (add new tests; create the file if missing)

The current `SyncAllProjectRoots` runs every registered adapter unconditionally. We add a filtered variant that skips adapters whose `Name()` is not in the allowed list. The `nanite-native` adapter is **always** included regardless of the filter — it manages `.nanite/` and NANITE.md, which are not user-selectable.

- [ ] **Step 1: Write the failing test**

Add to (or create) `internal/agent/adapter_test.go`:

```go
package agent

import (
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// fakeAdapter is a minimal CLIAgentAdapter for testing the registry.
type fakeAdapter struct {
	name     string
	priority int
	synced   *bool
}

func (f *fakeAdapter) Name() string  { return f.name }
func (f *fakeAdapter) Priority() int { return f.priority }
func (f *fakeAdapter) Discover(_ string) ([]Definition, error) { return nil, nil }
func (f *fakeAdapter) PopulateSandbox(_ string, _ store.AgentProfile, _ SandboxContext) error {
	return nil
}
func (f *fakeAdapter) SyncProjectRoot(_ string, _ []store.AgentProfile) error {
	if f.synced != nil {
		*f.synced = true
	}
	return nil
}

func TestSyncAllProjectRootsFiltered_OnlyAllowedRun(t *testing.T) {
	var claudeRan, codexRan, geminiRan, nativeRan bool
	reg := NewAdapterRegistry()
	reg.Register(&fakeAdapter{name: "claude", priority: 60, synced: &claudeRan})
	reg.Register(&fakeAdapter{name: "codex", priority: 70, synced: &codexRan})
	reg.Register(&fakeAdapter{name: "gemini", priority: 80, synced: &geminiRan})
	reg.Register(&fakeAdapter{name: "nanite-native", priority: 10, synced: &nativeRan})

	if err := reg.SyncAllProjectRootsFiltered("/tmp/x", nil, []string{"claude"}); err != nil {
		t.Fatalf("err: %v", err)
	}
	if !claudeRan {
		t.Error("claude should have run")
	}
	if codexRan {
		t.Error("codex should NOT have run")
	}
	if geminiRan {
		t.Error("gemini should NOT have run")
	}
	if !nativeRan {
		t.Error("nanite-native should always run")
	}
}

func TestSyncAllProjectRootsFiltered_EmptyAllowedListStillRunsNative(t *testing.T) {
	var claudeRan, nativeRan bool
	reg := NewAdapterRegistry()
	reg.Register(&fakeAdapter{name: "claude", priority: 60, synced: &claudeRan})
	reg.Register(&fakeAdapter{name: "nanite-native", priority: 10, synced: &nativeRan})

	if err := reg.SyncAllProjectRootsFiltered("/tmp/x", nil, []string{}); err != nil {
		t.Fatalf("err: %v", err)
	}
	if claudeRan {
		t.Error("claude should NOT have run with empty allowed list")
	}
	if !nativeRan {
		t.Error("nanite-native should always run, even with empty allowed list")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/agent/ -run TestSyncAllProjectRootsFiltered
```

Expected: FAIL with "SyncAllProjectRootsFiltered undefined".

- [ ] **Step 3: Add `SyncAllProjectRootsFiltered` method to `internal/agent/adapter.go`**

Insert after the existing `SyncAllProjectRoots` method (line 166):

```go
// SyncAllProjectRootsFiltered is the same as SyncAllProjectRoots but
// only runs the adapters whose Name() is present in the allowed slice.
// The "nanite-native" adapter is always run regardless of the allowed
// list — it manages .nanite/ and NANITE.md, which are not user-selectable.
//
// The adapter slice is copied before iteration to avoid holding the lock
// during I/O.
func (r *AdapterRegistry) SyncAllProjectRootsFiltered(projectDir string, agents []store.AgentProfile, allowed []string) error {
	allowSet := make(map[string]bool, len(allowed)+1)
	for _, name := range allowed {
		allowSet[name] = true
	}
	allowSet["nanite-native"] = true

	adapters := r.Adapters()

	for _, a := range adapters {
		if !allowSet[a.Name()] {
			continue
		}
		if err := a.SyncProjectRoot(projectDir, agents); err != nil {
			return fmt.Errorf("adapter %s: sync project root: %w", a.Name(), err)
		}
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/agent/ -run TestSyncAllProjectRootsFiltered -v
```

Expected: PASS for both cases.

Run the full agent package tests:

```bash
go test ./internal/agent/...
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/adapter.go internal/agent/adapter_test.go
git commit -m "$(cat <<'EOF'
feat(agent): SyncAllProjectRootsFiltered for adapter selection

New AdapterRegistry method that only runs adapters whose Name() is in
an allowed list. nanite-native is always included regardless of the
filter — it's not a user-selectable CLI integration. Used by the
install service to honor the project's adapters: selection list.

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: persistAdapterList helper

**Files:**
- Modify: `internal/service/install/adapter_persist.go` (add new function)
- Modify: `internal/service/install/adapter_persist_test.go` (add new tests)

`persistAdapterList` writes the resolved adapter list back to `.nanite/config.yaml` under the `adapters:` key, preserving all other keys (`nanite_version`, `agents`, comments where possible). YAML round-trip with comment preservation is non-trivial; we use `yaml.v3`'s `Node` API to keep formatting reasonable.

For simplicity and predictability, we parse with `yaml.Node`, find or insert the `adapters` key at the top of the mapping, and re-marshal. Comments are preserved by `yaml.v3` for keys that already exist; for newly-inserted `adapters:` keys, surrounding comments may shift slightly (acceptable for an installer-managed file).

- [ ] **Step 1: Write the failing tests**

Append to `internal/service/install/adapter_persist_test.go`:

```go
import (
	"strings"
)

func TestPersistAdapterList_PreservesOtherKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	original := `nanite_version: 2.3.0

agents:
  frontend:
    name: Frontend Developer
    description: React/Tailwind frontend
`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := persistAdapterList(path, []string{"claude", "codex"}); err != nil {
		t.Fatalf("persistAdapterList: %v", err)
	}

	got, _ := os.ReadFile(path)
	gotStr := string(got)

	if !strings.Contains(gotStr, "nanite_version: 2.3.0") {
		t.Errorf("nanite_version missing: %q", gotStr)
	}
	if !strings.Contains(gotStr, "frontend:") {
		t.Errorf("agent definition missing: %q", gotStr)
	}
	if !strings.Contains(gotStr, "Frontend Developer") {
		t.Errorf("agent name missing: %q", gotStr)
	}
	if !strings.Contains(gotStr, "adapters:") {
		t.Errorf("adapters key missing: %q", gotStr)
	}
	if !strings.Contains(gotStr, "claude") {
		t.Errorf("claude entry missing: %q", gotStr)
	}
	if !strings.Contains(gotStr, "codex") {
		t.Errorf("codex entry missing: %q", gotStr)
	}

	// Round-trip via loadProjectConfig to verify it parses cleanly.
	cfg, err := loadProjectConfig(path)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if cfg.Adapters == nil || len(*cfg.Adapters) != 2 {
		t.Errorf("re-parsed adapters: %v", cfg.Adapters)
	}
	if (*cfg.Adapters)[0] != "claude" || (*cfg.Adapters)[1] != "codex" {
		t.Errorf("re-parsed order wrong: %v", *cfg.Adapters)
	}
}

func TestPersistAdapterList_EmptyList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	original := "nanite_version: 2.3.0\nagents: {}\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := persistAdapterList(path, []string{}); err != nil {
		t.Fatalf("persistAdapterList: %v", err)
	}

	cfg, err := loadProjectConfig(path)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if cfg.Adapters == nil {
		t.Fatal("expected non-nil Adapters (empty != absent)")
	}
	if len(*cfg.Adapters) != 0 {
		t.Errorf("expected empty list, got %v", *cfg.Adapters)
	}
}

func TestPersistAdapterList_OverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	original := `nanite_version: 2.3.0
adapters:
  - claude
  - codex
agents: {}
`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := persistAdapterList(path, []string{"gemini"}); err != nil {
		t.Fatalf("persistAdapterList: %v", err)
	}

	cfg, err := loadProjectConfig(path)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if cfg.Adapters == nil || len(*cfg.Adapters) != 1 || (*cfg.Adapters)[0] != "gemini" {
		t.Errorf("expected [gemini], got %v", cfg.Adapters)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/service/install/ -run TestPersistAdapterList
```

Expected: FAIL with "persistAdapterList undefined".

- [ ] **Step 3: Implement `persistAdapterList` in `adapter_persist.go`**

Append to `internal/service/install/adapter_persist.go`:

```go
// persistAdapterList writes the given adapters slice into the
// .nanite/config.yaml file at path, under the `adapters:` key. All other
// top-level keys (nanite_version, agents, etc.) are preserved.
//
// An empty adapters slice (length 0, non-nil) is written as `adapters: []`
// to preserve the tri-state semantic — distinguishable from a missing key.
//
// The implementation uses yaml.Node to preserve key ordering and most
// formatting. Comments may shift slightly when a new adapters key is
// inserted into a mapping that didn't have one.
func persistAdapterList(path string, adapters []string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config %s: %w", path, err)
	}

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("parse config %s: %w", path, err)
	}

	// Root is a Document node; its first content is the mapping.
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return fmt.Errorf("config %s is not a yaml document", path)
	}
	mapping := root.Content[0]
	if mapping.Kind != yaml.MappingNode {
		return fmt.Errorf("config %s top-level is not a mapping", path)
	}

	// Build the new adapters value node.
	adaptersValue := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	if len(adapters) == 0 {
		adaptersValue.Style = yaml.FlowStyle // renders as []
	}
	for _, slug := range adapters {
		adaptersValue.Content = append(adaptersValue.Content, &yaml.Node{
			Kind:  yaml.ScalarNode,
			Tag:   "!!str",
			Value: slug,
		})
	}

	// Find the existing adapters key, replace its value.
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		key := mapping.Content[i]
		if key.Kind == yaml.ScalarNode && key.Value == "adapters" {
			mapping.Content[i+1] = adaptersValue
			return writeYAMLBack(path, &root)
		}
	}

	// Adapters key not present — insert it as the first key (for visibility).
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "adapters"}
	newContent := make([]*yaml.Node, 0, len(mapping.Content)+2)
	newContent = append(newContent, keyNode, adaptersValue)
	newContent = append(newContent, mapping.Content...)
	mapping.Content = newContent

	return writeYAMLBack(path, &root)
}

func writeYAMLBack(path string, root *yaml.Node) error {
	out, err := yaml.Marshal(root)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/service/install/ -run TestPersistAdapterList -v
```

Expected: PASS for all three cases.

- [ ] **Step 5: Commit**

```bash
git add internal/service/install/adapter_persist.go internal/service/install/adapter_persist_test.go
git commit -m "$(cat <<'EOF'
feat(install): persistAdapterList writes adapters: key to config.yaml

Round-trips .nanite/config.yaml via yaml.Node, replacing or inserting
the adapters: key while preserving other top-level keys (nanite_version,
agents). Empty list is written as `adapters: []` to preserve tri-state
semantics — distinguishable from a missing key.

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 7: Adapter placeholder content (all four CLI adapters)

**Files:**
- Modify: `internal/plugin/builtin/adapter-claude/plugin.go:159-178`
- Modify: `internal/plugin/builtin/adapter-codex/plugin.go:137-147`
- Modify: `internal/plugin/builtin/adapter-gemini/plugin.go` (similar lines)
- Modify: `internal/plugin/builtin/adapter-opencode/plugin.go` (similar lines)
- Modify: `internal/plugin/builtin/adapter-claude/plugin_test.go`
- Modify: `internal/plugin/builtin/adapter-codex/plugin_test.go`
- Modify: `internal/plugin/builtin/adapter-gemini/plugin_test.go`
- Modify: `internal/plugin/builtin/adapter-opencode/plugin_test.go`

Each of the four CLI adapters' `SyncProjectRoot` currently short-circuits on `len(agents) == 0`. We remove the short-circuit and have them write a placeholder section instead. The placeholder content is identical across all four for the initial implementation (per spec — tune later if any CLI's parser needs different formatting).

- [ ] **Step 1: Write the failing test for adapter-claude**

Append to `internal/plugin/builtin/adapter-claude/plugin_test.go`:

```go
func TestSyncProjectRoot_EmptyAgentsWritesPlaceholder(t *testing.T) {
	dir := t.TempDir()
	a := New().Adapter()
	if err := a.SyncProjectRoot(dir, nil); err != nil {
		t.Fatalf("SyncProjectRoot: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("CLAUDE.md should exist: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "<!-- nanite:start -->") {
		t.Errorf("missing start marker: %q", got)
	}
	if !strings.Contains(got, "<!-- nanite:end -->") {
		t.Errorf("missing end marker: %q", got)
	}
	if !strings.Contains(got, "No agents configured") {
		t.Errorf("missing placeholder text: %q", got)
	}
	if !strings.Contains(got, "NANITE.md") {
		t.Errorf("missing NANITE.md pointer: %q", got)
	}
}
```

If the file doesn't import `os`, `strings`, `filepath`, `testing` — add them to the import block.

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/plugin/builtin/adapter-claude/ -run TestSyncProjectRoot_EmptyAgentsWritesPlaceholder
```

Expected: FAIL — current code returns nil on empty agents and writes nothing.

- [ ] **Step 3: Update `internal/plugin/builtin/adapter-claude/plugin.go` SyncProjectRoot**

Replace lines 159-178 with:

```go
// SyncProjectRoot writes a managed section into {projectDir}/CLAUDE.md
// listing available Nanite agents. If no agents are configured, writes
// a placeholder section pointing to NANITE.md for setup help.
func (a *Adapter) SyncProjectRoot(projectDir string, agents []store.AgentProfile) error {
	var content string
	if len(agents) == 0 {
		content = placeholderContent
	} else {
		var b strings.Builder
		b.WriteString("Available Nanite agents:\n\n")
		for _, ap := range agents {
			if ap.Description != "" {
				fmt.Fprintf(&b, "- **%s** — %s\n", ap.Name, ap.Description)
			} else {
				fmt.Fprintf(&b, "- **%s**\n", ap.Name)
			}
		}
		content = b.String()
	}

	claudePath := filepath.Join(projectDir, "CLAUDE.md")
	return agent.WriteManagedSection(claudePath, content)
}

const placeholderContent = `## Nanite Agents

No agents configured for this project yet. See NANITE.md for setup help, or add an agent definition to ` + "`.nanite/config.yaml`" + ` and re-run ` + "`nanite install --project .`" + `.`
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/plugin/builtin/adapter-claude/ -run TestSyncProjectRoot_EmptyAgentsWritesPlaceholder -v
```

Expected: PASS.

Run the full claude adapter tests:

```bash
go test ./internal/plugin/builtin/adapter-claude/...
```

Expected: PASS (existing tests aren't affected — they test the populated path).

- [ ] **Step 5: Repeat for adapter-codex**

In `internal/plugin/builtin/adapter-codex/plugin_test.go`, add the same test (replace `CLAUDE.md` with `AGENTS.md`).

In `internal/plugin/builtin/adapter-codex/plugin.go`, replace `SyncProjectRoot` (lines 137-147) with:

```go
func (a *Adapter) SyncProjectRoot(projectDir string, agents []store.AgentProfile) error {
	var content string
	if len(agents) == 0 {
		content = placeholderContent
	} else {
		content = buildNaniteAgentsSection(agents)
	}
	agentsPath := filepath.Join(projectDir, "AGENTS.md")
	return agent.WriteManagedSection(agentsPath, content)
}

const placeholderContent = `## Nanite Agents

No agents configured for this project yet. See NANITE.md for setup help, or add an agent definition to ` + "`.nanite/config.yaml`" + ` and re-run ` + "`nanite install --project .`" + `.`
```

Run the test, expect PASS:

```bash
go test ./internal/plugin/builtin/adapter-codex/ -v
```

- [ ] **Step 6: Repeat for adapter-gemini**

Same test pattern (`GEMINI.md`). Same `SyncProjectRoot` change pattern. Add the same `placeholderContent` const.

Run the test, expect PASS:

```bash
go test ./internal/plugin/builtin/adapter-gemini/ -v
```

- [ ] **Step 7: Repeat for adapter-opencode**

Same test pattern (`OPENCODE.md`). Same `SyncProjectRoot` change pattern. Add the same `placeholderContent` const.

Run the test, expect PASS:

```bash
go test ./internal/plugin/builtin/adapter-opencode/ -v
```

- [ ] **Step 8: Run all builtin adapter tests as a sanity check**

```bash
go test ./internal/plugin/builtin/...
```

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/plugin/builtin/adapter-claude/plugin.go \
        internal/plugin/builtin/adapter-claude/plugin_test.go \
        internal/plugin/builtin/adapter-codex/plugin.go \
        internal/plugin/builtin/adapter-codex/plugin_test.go \
        internal/plugin/builtin/adapter-gemini/plugin.go \
        internal/plugin/builtin/adapter-gemini/plugin_test.go \
        internal/plugin/builtin/adapter-opencode/plugin.go \
        internal/plugin/builtin/adapter-opencode/plugin_test.go
git commit -m "$(cat <<'EOF'
feat(adapters): write placeholder section when no agents configured

Remove the len(agents)==0 short-circuit from all four CLI adapters
(claude, codex, gemini, opencode) and write a placeholder managed
section pointing the user (and the agent) to NANITE.md for setup help.

This is part of the adapter selection refactor: once an adapter is
in the project's adapters: list, its target file always exists with
markers, and re-running install picks up agents as they're defined.

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 8: ResolveAdapters non-interactive paths

**Files:**
- Create: `internal/service/install/adapter_select.go`
- Create: `internal/service/install/adapter_select_test.go`

`ResolveAdapters` is the resolution function with the priority order from spec section 4. We implement the non-interactive paths first (flag wins, config wins, detection fallback) and defer the prompt integration to Task 10.

The function is read-only — it returns `(resolved, previous, error)` and the caller persists.

- [ ] **Step 1: Write the failing tests**

Create `internal/service/install/adapter_select_test.go`:

```go
package install

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func ptr(s []string) *[]string { return &s }

func TestResolveAdapters_NoAdaptersFlagWins(t *testing.T) {
	dir := t.TempDir()
	cfg := &projectConfig{Adapters: ptr([]string{"claude", "codex"})}
	resolved, previous, err := ResolveAdapters(cfg, dir, ResolveOpts{NoAdapters: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved) != 0 {
		t.Errorf("resolved: got %v, want []", resolved)
	}
	if !reflect.DeepEqual(previous, []string{"claude", "codex"}) {
		t.Errorf("previous: got %v, want [claude codex]", previous)
	}
}

func TestResolveAdapters_FlagWinsOverConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := &projectConfig{Adapters: ptr([]string{"gemini"})}
	resolved, previous, err := ResolveAdapters(cfg, dir, ResolveOpts{Flag: "claude,codex"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(resolved, []string{"claude", "codex"}) {
		t.Errorf("resolved: got %v, want [claude codex]", resolved)
	}
	if !reflect.DeepEqual(previous, []string{"gemini"}) {
		t.Errorf("previous: got %v, want [gemini]", previous)
	}
}

func TestResolveAdapters_FlagRejectsUnknownSlug(t *testing.T) {
	dir := t.TempDir()
	cfg := &projectConfig{}
	_, _, err := ResolveAdapters(cfg, dir, ResolveOpts{Flag: "frobnicate"})
	if err == nil {
		t.Fatal("expected error for unknown slug, got nil")
	}
}

func TestResolveAdapters_FlagRejectsNaniteNative(t *testing.T) {
	dir := t.TempDir()
	cfg := &projectConfig{}
	_, _, err := ResolveAdapters(cfg, dir, ResolveOpts{Flag: "nanite-native"})
	if err == nil {
		t.Fatal("expected error for nanite-native (not user-selectable)")
	}
}

func TestResolveAdapters_ConfigWinsWhenNotReconfigure(t *testing.T) {
	dir := t.TempDir()
	// Plant CLAUDE.md so detection would find it.
	_ = os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# x"), 0o644)
	cfg := &projectConfig{Adapters: ptr([]string{"gemini"})}
	resolved, previous, err := ResolveAdapters(cfg, dir, ResolveOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(resolved, []string{"gemini"}) {
		t.Errorf("resolved: got %v, want [gemini]", resolved)
	}
	if !reflect.DeepEqual(previous, []string{"gemini"}) {
		t.Errorf("previous: got %v, want [gemini]", previous)
	}
}

func TestResolveAdapters_DetectionFallback_NonInteractive(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# x"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# x"), 0o644)
	cfg := &projectConfig{} // Adapters nil, key absent
	resolved, _, err := ResolveAdapters(cfg, dir, ResolveOpts{Interactive: false})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(resolved)
	if !reflect.DeepEqual(resolved, []string{"claude", "codex"}) {
		t.Errorf("resolved: got %v, want [claude codex]", resolved)
	}
}

func TestResolveAdapters_DetectionFallback_NonInteractive_Empty(t *testing.T) {
	dir := t.TempDir()
	cfg := &projectConfig{}
	resolved, _, err := ResolveAdapters(cfg, dir, ResolveOpts{Interactive: false})
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved) != 0 {
		t.Errorf("resolved: got %v, want []", resolved)
	}
}

func TestResolveAdapters_ReconfigureBypassesConfig_NonInteractive(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "GEMINI.md"), []byte("# x"), 0o644)
	cfg := &projectConfig{Adapters: ptr([]string{"claude"})}
	resolved, _, err := ResolveAdapters(cfg, dir, ResolveOpts{Reconfigure: true, Interactive: false})
	if err != nil {
		t.Fatal(err)
	}
	// Non-interactive --reconfigure: keep current per spec section 4 step 8
	// (interpreted as: with no prompt, current = cfg.Adapters; resolved = current)
	if !reflect.DeepEqual(resolved, []string{"claude"}) {
		t.Errorf("resolved: got %v, want [claude] (non-interactive --reconfigure is no-op)", resolved)
	}
}

// Sentinel writers for the prompt integration tests in Task 10.
var _ = bytes.NewBuffer
```

(The trailing `var _ = bytes.NewBuffer` keeps the `bytes` import alive even before Task 10 wires the prompt — it's just a placeholder to avoid an unused-import error in this intermediate state. Remove it after Task 10.)

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/service/install/ -run TestResolveAdapters
```

Expected: FAIL with "ResolveAdapters undefined" / "ResolveOpts undefined".

- [ ] **Step 3: Create `internal/service/install/adapter_select.go`**

```go
package install

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// ResolveOpts controls how ResolveAdapters picks the active adapter list.
// Stdin/Stdout are injection points for the interactive prompt (wired in
// Task 10) and are unused in the non-interactive paths.
type ResolveOpts struct {
	Flag        string    // value of --adapters, empty = unset
	NoAdapters  bool      // --no-adapters
	Reconfigure bool      // --reconfigure
	Interactive bool      // isStdinTTY() result, set by the caller
	Stdin       io.Reader // for prompt injection in tests
	Stdout      io.Writer // for prompt output in tests
}

// userSelectableAdapters is the set of adapter slugs that ResolveAdapters
// will accept from user input (CLI flag or prompt). nanite-native is
// excluded — it's loaded internally by the install service and is not a
// CLI integration.
var userSelectableAdapters = []string{"claude", "codex", "gemini", "opencode"}

// ResolveAdapters computes the active adapter list for an install run
// using the priority order:
//
//  1. NoAdapters flag → []
//  2. Flag (--adapters) → parsed list (validated against registry)
//  3. cfg.Adapters present and !Reconfigure → cfg.Adapters as-is
//  4. Detection-then-prompt (interactive) or detection-only (non-interactive)
//
// Returns (resolved, previous, error). The caller is responsible for
// cleanup (using `previous - resolved`) and persistence. ResolveAdapters
// is read-only — it does not write to disk.
func ResolveAdapters(cfg *projectConfig, projectDir string, opts ResolveOpts) (resolved []string, previous []string, err error) {
	if cfg.Adapters != nil {
		previous = append(previous, (*cfg.Adapters)...)
	}

	// 1. --no-adapters flag wins.
	if opts.NoAdapters {
		return []string{}, previous, nil
	}

	// 2. --adapters flag wins.
	if opts.Flag != "" {
		parsed, perr := parseAdapterFlag(opts.Flag)
		if perr != nil {
			return nil, previous, perr
		}
		return parsed, previous, nil
	}

	// 3. Config wins unless --reconfigure.
	if !opts.Reconfigure && cfg.Adapters != nil {
		return previous, previous, nil // resolved == previous (no-op case)
	}

	// 4. Detection (with prompt in interactive mode).
	detected := DetectAdapters(projectDir)
	sort.Strings(detected)

	if opts.Interactive {
		// Prompt integration is wired in Task 10. For now, fall through
		// to detection-only behavior — Task 10 will replace this branch
		// with promptAdapterSelection().
		return detected, previous, nil
	}

	// Non-interactive --reconfigure with existing config: keep current.
	if opts.Reconfigure && cfg.Adapters != nil {
		return previous, previous, nil
	}

	// Non-interactive fresh: use detection result.
	return detected, previous, nil
}

// parseAdapterFlag parses a comma-separated --adapters value, validates
// each slug against the user-selectable adapter list, and returns the
// resulting slice. Returns an error if any slug is unknown or if
// nanite-native is requested (it's not user-selectable).
func parseAdapterFlag(value string) ([]string, error) {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, raw := range parts {
		slug := strings.TrimSpace(raw)
		if slug == "" {
			continue
		}
		if !isUserSelectable(slug) {
			return nil, fmt.Errorf("unknown adapter %q (available: %s)", slug, strings.Join(userSelectableAdapters, ", "))
		}
		out = append(out, slug)
	}
	return out, nil
}

func isUserSelectable(slug string) bool {
	for _, s := range userSelectableAdapters {
		if s == slug {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/service/install/ -run TestResolveAdapters -v
```

Expected: PASS for all eight cases.

- [ ] **Step 5: Commit**

```bash
git add internal/service/install/adapter_select.go internal/service/install/adapter_select_test.go
git commit -m "$(cat <<'EOF'
feat(install): ResolveAdapters non-interactive priority order

Implement the non-interactive paths of the adapter selection resolver:
--no-adapters flag → --adapters flag → config (unless --reconfigure)
→ detection. Read-only — returns (resolved, previous, error) and the
caller handles cleanup-then-persist ordering. Interactive prompt path
is stubbed and will be wired in the next task.

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 9: promptAdapterSelection function

**Files:**
- Create: `internal/service/install/adapter_prompt.go`
- Create: `internal/service/install/adapter_prompt_test.go`

The interactive prompt lives in the `install` package (not `cmd/nanite`) so it's testable with injected `io.Reader`/`io.Writer` and so `ResolveAdapters` can call it directly. Per the spec, three flows:

1. **Fresh install, detection found something** — Y/n/e prompt
2. **Fresh install, detection found nothing (or `e`)** — numbered list, empty = `[]`
3. **`--reconfigure`** — numbered list with `*` for current, empty = keep current

- [ ] **Step 1: Write the failing tests**

Create `internal/service/install/adapter_prompt_test.go`:

```go
package install

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestPromptAdapterSelection_DetectedAccept(t *testing.T) {
	in := strings.NewReader("y\n")
	var out bytes.Buffer
	got, err := promptAdapterSelection(promptInput{
		Detected: []string{"claude", "codex"},
		Current:  []string{"claude", "codex"},
		IsReconfigure: false,
		Stdin:    in,
		Stdout:   &out,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"claude", "codex"}) {
		t.Errorf("got %v, want [claude codex]", got)
	}
	if !strings.Contains(out.String(), "Detected") {
		t.Errorf("expected detected message in output: %q", out.String())
	}
}

func TestPromptAdapterSelection_DetectedDecline(t *testing.T) {
	in := strings.NewReader("n\n")
	var out bytes.Buffer
	got, err := promptAdapterSelection(promptInput{
		Detected: []string{"claude"},
		Current:  []string{"claude"},
		Stdin:    in,
		Stdout:   &out,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want []", got)
	}
}

func TestPromptAdapterSelection_DetectedEditFallsThroughToList(t *testing.T) {
	// "e" → fall through to numbered list → "1 3" picks claude and gemini
	in := strings.NewReader("e\n1 3\n")
	var out bytes.Buffer
	got, err := promptAdapterSelection(promptInput{
		Detected: []string{"claude", "codex"},
		Current:  []string{"claude", "codex"},
		Stdin:    in,
		Stdout:   &out,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Numbered list is the canonical userSelectableAdapters order.
	if !reflect.DeepEqual(got, []string{"claude", "gemini"}) {
		t.Errorf("got %v, want [claude gemini]", got)
	}
}

func TestPromptAdapterSelection_FreshNoDetection_Empty(t *testing.T) {
	// Detection found nothing, user enters empty → []
	in := strings.NewReader("\n")
	var out bytes.Buffer
	got, err := promptAdapterSelection(promptInput{
		Detected: nil,
		Current:  nil,
		Stdin:    in,
		Stdout:   &out,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want []", got)
	}
}

func TestPromptAdapterSelection_FreshNoDetection_PicksTwo(t *testing.T) {
	in := strings.NewReader("2 4\n")
	var out bytes.Buffer
	got, err := promptAdapterSelection(promptInput{
		Detected: nil,
		Current:  nil,
		Stdin:    in,
		Stdout:   &out,
	})
	if err != nil {
		t.Fatal(err)
	}
	// userSelectableAdapters order: claude(1), codex(2), gemini(3), opencode(4)
	if !reflect.DeepEqual(got, []string{"codex", "opencode"}) {
		t.Errorf("got %v, want [codex opencode]", got)
	}
}

func TestPromptAdapterSelection_Reconfigure_EmptyKeepsCurrent(t *testing.T) {
	in := strings.NewReader("\n")
	var out bytes.Buffer
	got, err := promptAdapterSelection(promptInput{
		Detected:      []string{"claude", "codex"},
		Current:       []string{"claude", "gemini"},
		IsReconfigure: true,
		Stdin:         in,
		Stdout:        &out,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"claude", "gemini"}) {
		t.Errorf("got %v, want current [claude gemini]", got)
	}
	if !strings.Contains(out.String(), "*") {
		t.Errorf("expected * markers in output: %q", out.String())
	}
}

func TestPromptAdapterSelection_Reconfigure_ReplacesList(t *testing.T) {
	in := strings.NewReader("3\n")
	var out bytes.Buffer
	got, err := promptAdapterSelection(promptInput{
		Detected:      []string{"claude"},
		Current:       []string{"claude", "codex"},
		IsReconfigure: true,
		Stdin:         in,
		Stdout:        &out,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"gemini"}) {
		t.Errorf("got %v, want [gemini]", got)
	}
}

func TestPromptAdapterSelection_InvalidThenValid(t *testing.T) {
	// "abc" is invalid → re-prompt → "1" picks claude
	in := strings.NewReader("abc\n1\n")
	var out bytes.Buffer
	got, err := promptAdapterSelection(promptInput{
		Detected: nil,
		Current:  nil,
		Stdin:    in,
		Stdout:   &out,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"claude"}) {
		t.Errorf("got %v, want [claude]", got)
	}
	if !strings.Contains(out.String(), "invalid") {
		t.Errorf("expected 'invalid' warning: %q", out.String())
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/service/install/ -run TestPromptAdapterSelection
```

Expected: FAIL with "promptAdapterSelection undefined" / "promptInput undefined".

- [ ] **Step 3: Create `internal/service/install/adapter_prompt.go`**

```go
package install

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// promptInput is the parameter bag for promptAdapterSelection. Bundling
// the inputs into a struct makes the call sites and tests less noisy.
type promptInput struct {
	Detected      []string
	Current       []string
	IsReconfigure bool
	Stdin         io.Reader
	Stdout        io.Writer
}

// promptAdapterSelection runs the interactive adapter-selection prompt
// and returns the chosen list. Re-prompts on invalid input up to 3 times
// before falling back (fresh install: empty list; reconfigure: current).
//
// Behavior matrix:
//
//   - Fresh install, detection found something: prompt Y/n/e
//   - Fresh install, detection found nothing (or e from above): numbered list,
//     empty input → empty list
//   - --reconfigure: numbered list with * marking current, empty input → current
func promptAdapterSelection(in promptInput) ([]string, error) {
	r := bufio.NewReader(in.Stdin)

	// Sort detected for stable display.
	detected := append([]string(nil), in.Detected...)
	sort.Strings(detected)

	// --reconfigure path: skip the Y/n/e shortcut, go straight to the list.
	if in.IsReconfigure {
		return promptNumberedList(in.Stdout, r, in.Current, true)
	}

	// Fresh install with detection: offer the Y/n/e shortcut.
	if len(detected) > 0 {
		fmt.Fprintf(in.Stdout, "Detected CLI tools in this project: %s\n", strings.Join(detected, ", "))
		fmt.Fprintln(in.Stdout, "Manage these with Nanite? [Y]es / [n]o / [e]dit list")
		fmt.Fprint(in.Stdout, "> ")
		line, err := r.ReadString('\n')
		if err != nil && line == "" {
			return nil, fmt.Errorf("read prompt: %w", err)
		}
		choice := strings.ToLower(strings.TrimSpace(line))
		switch choice {
		case "", "y", "yes":
			return detected, nil
		case "n", "no":
			return []string{}, nil
		case "e", "edit":
			// Fall through to the numbered list.
		default:
			fmt.Fprintf(in.Stdout, "invalid choice %q, falling through to edit list\n", choice)
		}
	}

	// Fresh install with no detection (or fell through from "e"): numbered list.
	return promptNumberedList(in.Stdout, r, in.Current, false)
}

// promptNumberedList renders the numbered adapter list and reads a
// space-separated index selection. Up to 3 invalid attempts before
// falling back.
//
// If isReconfigure is true:
//   - current adapters are marked with " *"
//   - empty input keeps current
//
// If isReconfigure is false:
//   - no markers
//   - empty input returns []
func promptNumberedList(w io.Writer, r *bufio.Reader, current []string, isReconfigure bool) ([]string, error) {
	currentSet := make(map[string]bool, len(current))
	for _, c := range current {
		currentSet[c] = true
	}

	var marker string
	var emptyHint string
	if isReconfigure {
		fmt.Fprintln(w, "Select CLI tools to manage with Nanite (* = currently enabled):")
		emptyHint = "empty to keep current"
	} else {
		fmt.Fprintln(w, "Select CLI tools to manage with Nanite:")
		emptyHint = "empty for none"
	}
	for i, slug := range userSelectableAdapters {
		marker = ""
		if isReconfigure && currentSet[slug] {
			marker = " *"
		}
		fmt.Fprintf(w, "  %d) %s%s\n", i+1, slug, marker)
	}
	fmt.Fprintf(w, "Enter numbers separated by spaces (%s):\n", emptyHint)

	for attempt := 0; attempt < 3; attempt++ {
		fmt.Fprint(w, "> ")
		line, err := r.ReadString('\n')
		if err != nil && line == "" {
			return nil, fmt.Errorf("read prompt: %w", err)
		}
		raw := strings.TrimSpace(line)
		if raw == "" {
			if isReconfigure {
				return current, nil
			}
			return []string{}, nil
		}
		picked, perr := parseNumberSelection(raw)
		if perr != nil {
			fmt.Fprintf(w, "invalid input: %v\n", perr)
			continue
		}
		return picked, nil
	}

	// Fallback after 3 invalid attempts.
	fmt.Fprintln(w, "too many invalid attempts; using fallback")
	if isReconfigure {
		return current, nil
	}
	return []string{}, nil
}

// parseNumberSelection parses a space-separated list of 1-indexed numbers
// referencing userSelectableAdapters and returns the corresponding slugs.
// Returns an error on any unparseable token or out-of-range index.
func parseNumberSelection(raw string) ([]string, error) {
	tokens := strings.Fields(raw)
	out := make([]string, 0, len(tokens))
	seen := make(map[int]bool, len(tokens))
	for _, tok := range tokens {
		n, err := strconv.Atoi(tok)
		if err != nil {
			return nil, fmt.Errorf("not a number: %q", tok)
		}
		if n < 1 || n > len(userSelectableAdapters) {
			return nil, fmt.Errorf("out of range: %d (valid 1..%d)", n, len(userSelectableAdapters))
		}
		if seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, userSelectableAdapters[n-1])
	}
	return out, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/service/install/ -run TestPromptAdapterSelection -v
```

Expected: PASS for all eight cases.

- [ ] **Step 5: Commit**

```bash
git add internal/service/install/adapter_prompt.go internal/service/install/adapter_prompt_test.go
git commit -m "$(cat <<'EOF'
feat(install): promptAdapterSelection interactive picker

Three modes per spec section 5:
- Detected, fresh install: Y/n/e shortcut prompt
- No detection (or "e"): numbered list, empty → []
- --reconfigure: numbered list with * marking current, empty → keep current

Re-prompts up to 3 times on invalid input, then falls back to a safe
default (empty for fresh, current for reconfigure). Stdin/Stdout are
injected so the function is fully unit-testable.

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 10: Wire prompt into ResolveAdapters

**Files:**
- Modify: `internal/service/install/adapter_select.go`
- Modify: `internal/service/install/adapter_select_test.go`

Replace the stubbed interactive branch in `ResolveAdapters` with a call to `promptAdapterSelection`. Add tests that exercise the interactive paths via injected I/O.

- [ ] **Step 1: Write the failing tests**

Append to `internal/service/install/adapter_select_test.go` (and remove the placeholder `var _ = bytes.NewBuffer` line):

```go
func TestResolveAdapters_InteractivePrompt_Accept(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# x"), 0o644)
	cfg := &projectConfig{}
	in := bytes.NewBufferString("y\n")
	var out bytes.Buffer
	resolved, _, err := ResolveAdapters(cfg, dir, ResolveOpts{
		Interactive: true,
		Stdin:       in,
		Stdout:      &out,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(resolved, []string{"claude"}) {
		t.Errorf("resolved: got %v, want [claude]", resolved)
	}
}

func TestResolveAdapters_InteractivePrompt_Reconfigure_KeepsCurrent(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "GEMINI.md"), []byte("# x"), 0o644)
	cfg := &projectConfig{Adapters: ptr([]string{"claude"})}
	in := bytes.NewBufferString("\n")
	var out bytes.Buffer
	resolved, previous, err := ResolveAdapters(cfg, dir, ResolveOpts{
		Reconfigure: true,
		Interactive: true,
		Stdin:       in,
		Stdout:      &out,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(resolved, []string{"claude"}) {
		t.Errorf("resolved: got %v, want current [claude]", resolved)
	}
	if !reflect.DeepEqual(previous, []string{"claude"}) {
		t.Errorf("previous: got %v, want [claude]", previous)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/service/install/ -run TestResolveAdapters_InteractivePrompt
```

Expected: FAIL — current `ResolveAdapters` interactive branch returns `detected` directly without calling the prompt.

- [ ] **Step 3: Update `ResolveAdapters` interactive branch in `adapter_select.go`**

Replace the interactive branch (the `if opts.Interactive` block) with:

```go
	if opts.Interactive {
		picked, perr := promptAdapterSelection(promptInput{
			Detected:      detected,
			Current:       previous, // nil if cfg.Adapters was nil
			IsReconfigure: opts.Reconfigure,
			Stdin:         opts.Stdin,
			Stdout:        opts.Stdout,
		})
		if perr != nil {
			return nil, previous, perr
		}
		return picked, previous, nil
	}
```

Note: `previous` is `nil` when `cfg.Adapters == nil` (first install) and the populated slice otherwise. The prompt's `IsReconfigure` flag tells it which UX to use.

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/service/install/ -run TestResolveAdapters -v
```

Expected: PASS for all ResolveAdapters tests, including the two new interactive ones.

- [ ] **Step 5: Commit**

```bash
git add internal/service/install/adapter_select.go internal/service/install/adapter_select_test.go
git commit -m "$(cat <<'EOF'
feat(install): wire promptAdapterSelection into ResolveAdapters

Replaces the stubbed interactive branch with a real call to the prompt.
Detection is still computed first and passed to the prompt as the
suggested set; the prompt handles the Y/n/e shortcut, the numbered
list, and the --reconfigure-with-current variant.

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 11: syncAdaptersForProject signature change

**Files:**
- Modify: `internal/service/install/adapters.go:136-147`
- Create: `internal/service/install/adapters_test.go` (or extend if it exists)

The current `syncAdaptersForProject(projectDir string)` is a thin wrapper that builds the registry, reads agents, and calls `SyncAllProjectRoots`. We change it to accept an `allowedAdapters []string` and use `SyncAllProjectRootsFiltered` from Task 5.

- [ ] **Step 1: Check if `adapters_test.go` exists**

```bash
ls internal/service/install/adapters_test.go 2>&1
```

If it doesn't exist, the test below creates it. If it does, append the new test to the existing file.

- [ ] **Step 2: Write the failing test**

Create or append to `internal/service/install/adapters_test.go`:

```go
package install

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSyncAdaptersForProject_FilterAllowed(t *testing.T) {
	dir := t.TempDir()
	naniteDir := filepath.Join(dir, ".nanite")
	if err := os.MkdirAll(naniteDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Write a config with one agent so adapters produce non-placeholder content.
	cfg := `nanite_version: 2.3.0
agents:
  frontend:
    name: Frontend Developer
    description: React work
`
	if err := os.WriteFile(filepath.Join(naniteDir, "config.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	// Run with only claude allowed.
	if err := syncAdaptersForProject(dir, []string{"claude"}); err != nil {
		t.Fatalf("syncAdaptersForProject: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "CLAUDE.md")); err != nil {
		t.Errorf("CLAUDE.md should exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); !os.IsNotExist(err) {
		t.Errorf("AGENTS.md should NOT exist (codex not allowed), stat: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "GEMINI.md")); !os.IsNotExist(err) {
		t.Errorf("GEMINI.md should NOT exist (gemini not allowed), stat: %v", err)
	}
}

func TestSyncAdaptersForProject_EmptyAllowedListWritesNoFiles(t *testing.T) {
	dir := t.TempDir()
	naniteDir := filepath.Join(dir, ".nanite")
	if err := os.MkdirAll(naniteDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(naniteDir, "config.yaml"), []byte("agents: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := syncAdaptersForProject(dir, []string{}); err != nil {
		t.Fatalf("syncAdaptersForProject: %v", err)
	}
	for _, name := range adapterTargetFiles {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("%s should NOT exist with empty allowed list", name)
		}
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

```bash
go test ./internal/service/install/ -run TestSyncAdaptersForProject
```

Expected: FAIL with "too many arguments" (current signature takes one arg) or compile error.

- [ ] **Step 4: Update `syncAdaptersForProject` in `internal/service/install/adapters.go`**

Replace lines 129-147 with:

```go
// syncAdaptersForProject runs SyncAllProjectRootsFiltered against the
// built-in adapter registry, parsing the agents list from
// `<projectDir>/.nanite/config.yaml`. Only the adapters whose Name() is
// in allowedAdapters are run; nanite-native is always included.
//
// If the config file is missing or empty, the agents list is empty and
// each (allowed) adapter writes a placeholder section.
func syncAdaptersForProject(projectDir string, allowedAdapters []string) error {
	cfgPath := filepath.Join(projectDir, ".nanite", "config.yaml")
	agents, err := extractAgentsFromConfig(cfgPath)
	if err != nil {
		return fmt.Errorf("extract agents: %w", err)
	}
	reg := newBuiltinAdapterRegistry()
	if err := reg.SyncAllProjectRootsFiltered(projectDir, agents, allowedAdapters); err != nil {
		return fmt.Errorf("sync adapters: %w", err)
	}
	return nil
}
```

Compile errors will surface in `install.go`, `adopt.go`, and `migrate.go` — they still call the old signature. Tasks 12-14 fix those. For now, comment out the calls in those files temporarily to get the tests to pass, OR just expect the build to fail in the install package overall and rely on this task's tests building cleanly.

Actually, the cleanest path: stub the callers temporarily so the package compiles. Edit `install.go:165`, `adopt.go:59`, and `migrate.go:109` to pass an empty allowed list:

```go
if err := syncAdaptersForProject(projectDir, nil); err != nil {
```

This is a temporary stub — Tasks 12-14 will replace it with the real resolved list.

- [ ] **Step 5: Run tests to verify they pass**

```bash
go test ./internal/service/install/ -run TestSyncAdaptersForProject -v
```

Expected: PASS for both new cases. Existing install tests may fail because the install paths are stubbed; that's expected.

Run the package build to make sure everything compiles:

```bash
go build ./internal/service/install/...
```

Expected: success.

- [ ] **Step 6: Commit**

```bash
git add internal/service/install/adapters.go internal/service/install/adapters_test.go internal/service/install/install.go internal/service/install/adopt.go internal/service/install/migrate.go
git commit -m "$(cat <<'EOF'
refactor(install): syncAdaptersForProject takes allowedAdapters

Change the signature to accept a list of adapter slugs and use the new
SyncAllProjectRootsFiltered registry method. Callers are temporarily
stubbed with nil — Tasks 12-14 will wire them to the resolved list.

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 12: Update freshScaffold to use the new flow

**Files:**
- Modify: `internal/service/install/install.go:54-70` (`InstallProjectOptions` and `InstallProjectReport`)
- Modify: `internal/service/install/install.go:145-172` (`freshScaffold`)
- Modify: `internal/service/install/integration_test.go` (existing fresh install test)

`freshScaffold` is the most-changed install path. We:
1. Remove the legacy direct `UpdateCLAUDEmd` call
2. Load the existing config (via `loadProjectConfig`)
3. Call `ResolveAdapters` to get resolved + previous
4. Call `cleanupRemovedAdapters` for `previous - resolved`
5. Call `persistAdapterList` to write the new list
6. Call `syncAdaptersForProject` with the resolved list
7. Return the report including the resolved list and any cleanup reports

We also extend `InstallProjectOptions` with the new flag fields and `InstallProjectReport` with `Adapters` and `AdapterCleanups`.

- [ ] **Step 1: Write the failing integration test**

Read the existing `internal/service/install/integration_test.go` to find the fresh-install test (likely `TestInstallProject_Fresh` or similar).

Add (or update) a test that asserts the new behavior:

```go
func TestInstallProject_Fresh_NoAdapters_NoCLIFiles(t *testing.T) {
	dir := t.TempDir()
	globalHome := setupTestGlobalHome(t) // existing helper

	svc := New()
	report, err := svc.InstallProject(InstallProjectOptions{
		ProjectDir: dir,
		GlobalHome: globalHome,
		NoAdapters: true, // new field
	})
	if err != nil {
		t.Fatalf("InstallProject: %v", err)
	}
	if !report.FreshScaffold {
		t.Error("expected FreshScaffold=true")
	}
	if len(report.Adapters) != 0 {
		t.Errorf("Adapters: got %v, want []", report.Adapters)
	}
	for _, name := range []string{"CLAUDE.md", "AGENTS.md", "GEMINI.md", "OPENCODE.md"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("%s should NOT exist with --no-adapters: %v", name, err)
		}
	}

	// Config should have adapters: [] persisted.
	cfg, err := loadProjectConfig(filepath.Join(dir, ".nanite", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Adapters == nil {
		t.Error("expected non-nil Adapters in persisted config")
	}
	if len(*cfg.Adapters) != 0 {
		t.Errorf("expected empty Adapters in persisted config, got %v", *cfg.Adapters)
	}
}

func TestInstallProject_Fresh_AdaptersFlag_WritesFiles(t *testing.T) {
	dir := t.TempDir()
	globalHome := setupTestGlobalHome(t)

	svc := New()
	report, err := svc.InstallProject(InstallProjectOptions{
		ProjectDir: dir,
		GlobalHome: globalHome,
		Adapters:   "claude,codex", // new field
	})
	if err != nil {
		t.Fatalf("InstallProject: %v", err)
	}
	if !report.FreshScaffold {
		t.Error("expected FreshScaffold=true")
	}
	if !reflect.DeepEqual(report.Adapters, []string{"claude", "codex"}) {
		t.Errorf("Adapters: got %v, want [claude codex]", report.Adapters)
	}
	if _, err := os.Stat(filepath.Join(dir, "CLAUDE.md")); err != nil {
		t.Errorf("CLAUDE.md should exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); err != nil {
		t.Errorf("AGENTS.md should exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "GEMINI.md")); !os.IsNotExist(err) {
		t.Errorf("GEMINI.md should NOT exist: %v", err)
	}
}
```

If `setupTestGlobalHome` doesn't exist, look at how the existing fresh-install integration test sets up `globalHome` — likely `t.TempDir()` plus an `assets.ExtractTo` call. Use the same pattern.

Add `reflect` to the imports if needed.

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/service/install/ -run TestInstallProject_Fresh_NoAdapters
```

Expected: FAIL — `InstallProjectOptions` doesn't have a `NoAdapters` field, `InstallProjectReport` doesn't have an `Adapters` field.

- [ ] **Step 3: Extend `InstallProjectOptions` and `InstallProjectReport`**

In `internal/service/install/install.go`, replace lines 54-70 with:

```go
// InstallProjectOptions controls InstallProject.
type InstallProjectOptions struct {
	ProjectDir         string
	GlobalHome         string // defaults to ~/.nanite
	MigrateFromAgentrc bool
	ArchiveOnly        bool

	// Adapter selection options (new in 2026-04-09 design).
	Adapters    string // value of --adapters flag, comma-separated, "" = unset
	NoAdapters  bool   // --no-adapters flag
	Reconfigure bool   // --reconfigure flag

	// Interactive controls whether to prompt the user for adapter
	// selection. Set by the CLI based on isStdinTTY().
	Interactive bool
	Stdin       io.Reader // injection for prompts (defaults to os.Stdin)
	Stdout      io.Writer // injection for prompts (defaults to os.Stdout)
}

// InstallProjectReport summarizes what InstallProject did.
type InstallProjectReport struct {
	FreshScaffold      bool
	Migrated           bool
	Adopted            bool
	ArchiveOnly        bool
	ArchivePath        string
	CLAUDEUpdateReport *CLAUDEUpdateReport // legacy, retained for migrate path's snapshot
	Warnings           []string

	// Adapter selection results (new in 2026-04-09 design).
	Adapters        []string        // resolved adapter list (may be empty)
	AdapterCleanups []CleanupReport // sections that were stripped/deleted
}
```

Add `"io"` to the import block.

- [ ] **Step 4: Update `freshScaffold` to use the new flow**

Replace lines 145-172 of `internal/service/install/install.go` with:

```go
func (s *Service) freshScaffold(projectDir, globalHome string, opts InstallProjectOptions) (*InstallProjectReport, error) {
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

	cfgPath := filepath.Join(projectDir, ".nanite", "config.yaml")
	cfg, err := loadProjectConfig(cfgPath)
	if err != nil {
		return nil, err
	}

	resolved, previous, err := ResolveAdapters(cfg, projectDir, ResolveOpts{
		Flag:        opts.Adapters,
		NoAdapters:  opts.NoAdapters,
		Reconfigure: opts.Reconfigure,
		Interactive: opts.Interactive,
		Stdin:       opts.Stdin,
		Stdout:      opts.Stdout,
	})
	if err != nil {
		return nil, fmt.Errorf("resolve adapters: %w", err)
	}

	removed := setDifference(previous, resolved)
	cleanupReports, err := cleanupRemovedAdapters(projectDir, removed)
	if err != nil {
		return nil, fmt.Errorf("cleanup removed adapters: %w", err)
	}

	if err := persistAdapterList(cfgPath, resolved); err != nil {
		return nil, fmt.Errorf("persist adapter list: %w", err)
	}

	if err := syncAdaptersForProject(projectDir, resolved); err != nil {
		return nil, fmt.Errorf("adapter sync: %w", err)
	}

	return &InstallProjectReport{
		FreshScaffold:   true,
		Adapters:        resolved,
		AdapterCleanups: cleanupReports,
	}, nil
}

// setDifference returns elements present in `a` but not in `b`.
func setDifference(a, b []string) []string {
	bSet := make(map[string]bool, len(b))
	for _, x := range b {
		bSet[x] = true
	}
	var out []string
	for _, x := range a {
		if !bSet[x] {
			out = append(out, x)
		}
	}
	return out
}
```

The signature change (now takes `opts InstallProjectOptions`) requires updating the caller in `InstallProject` (around line 142):

```go
return s.freshScaffold(projectDir, globalHome, opts)
```

- [ ] **Step 5: Run tests to verify they pass**

```bash
go test ./internal/service/install/ -run TestInstallProject_Fresh -v
```

Expected: PASS for the new tests.

The existing fresh-install integration tests may now fail because they assert legacy behavior. Identify and update them: they should now expect the new `Adapters` field in the report and the absence of CLAUDE.md when `--no-adapters` would have been the default. (The default for non-interactive fresh install is detection, which on a `t.TempDir()` returns nil, so existing tests that didn't plant any CLI files should now expect no CLI files.)

If existing tests assert that CLAUDE.md exists after a fresh install with no other setup — those assertions are now wrong. Update them to assert that CLAUDE.md does NOT exist (or to set up detection evidence first if they want it to exist).

Run the full install package tests:

```bash
go test ./internal/service/install/...
```

Expected: PASS (after updating existing tests).

- [ ] **Step 6: Commit**

```bash
git add internal/service/install/install.go internal/service/install/integration_test.go
git commit -m "$(cat <<'EOF'
feat(install): freshScaffold uses ResolveAdapters/cleanup/persist flow

Replace the legacy direct UpdateCLAUDEmd call in freshScaffold with the
new flow: load config, resolve adapter list (flag/config/detect/prompt),
clean up removed adapters, persist resolved list, sync remaining
adapters. Extend InstallProjectOptions and InstallProjectReport with
the new fields. Wire opts through from InstallProject.

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 13: Update adoptExisting to use the new flow

**Files:**
- Modify: `internal/service/install/adopt.go`
- Modify: `internal/service/install/install.go` (caller)
- Modify: `internal/service/install/integration_test.go` (add adopt test)

Same pattern as Task 12, applied to the adopt path. Adopt is the most common path for `--reconfigure` since the project already exists and has a `.nanite/` dir.

- [ ] **Step 1: Write the failing test**

Append to `integration_test.go`:

```go
func TestInstallProject_Adopt_Reconfigure_RemovesDroppedAdapter(t *testing.T) {
	dir := t.TempDir()
	globalHome := setupTestGlobalHome(t)

	// First install with claude + codex.
	if _, err := New().InstallProject(InstallProjectOptions{
		ProjectDir: dir,
		GlobalHome: globalHome,
		Adapters:   "claude,codex",
	}); err != nil {
		t.Fatalf("first install: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); err != nil {
		t.Fatalf("AGENTS.md should exist after first install: %v", err)
	}

	// Re-run with --reconfigure and only claude → AGENTS.md should be cleaned up.
	report, err := New().InstallProject(InstallProjectOptions{
		ProjectDir:  dir,
		GlobalHome:  globalHome,
		Adapters:    "claude",
		Reconfigure: true,
	})
	if err != nil {
		t.Fatalf("reconfigure: %v", err)
	}
	if !report.Adopted {
		t.Errorf("expected Adopted=true, got %+v", report)
	}
	if !reflect.DeepEqual(report.Adapters, []string{"claude"}) {
		t.Errorf("Adapters: got %v, want [claude]", report.Adapters)
	}
	if len(report.AdapterCleanups) != 1 {
		t.Errorf("expected 1 cleanup report, got %d", len(report.AdapterCleanups))
	}
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); !os.IsNotExist(err) {
		t.Errorf("AGENTS.md should be deleted, stat: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "CLAUDE.md")); err != nil {
		t.Errorf("CLAUDE.md should still exist: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/service/install/ -run TestInstallProject_Adopt_Reconfigure -v
```

Expected: FAIL — adopt path still uses legacy flow.

- [ ] **Step 3: Update `adoptExisting` in `internal/service/install/adopt.go`**

Replace the entire function body with:

```go
func (s *Service) adoptExisting(projectDir, globalHome string, opts InstallProjectOptions) (*InstallProjectReport, error) {
	src := ScaffoldSource{
		FrameworkVersion: assets.Version(),
		ProjectName:      filepath.Base(projectDir),
	}

	// ScaffoldNaniteDir is safe to call against an existing .nanite/:
	//   - config.yaml is only written if missing
	//   - agents/ is created via MkdirAll (no-op if present)
	//   - symlinks are only created if the link path doesn't exist
	if err := ScaffoldNaniteDir(projectDir, globalHome, src); err != nil {
		return nil, err
	}

	// ScaffoldNaniteMD preserves existing NANITE.md.
	if err := ScaffoldNaniteMD(projectDir, src); err != nil {
		return nil, err
	}

	cfgPath := filepath.Join(projectDir, ".nanite", "config.yaml")
	cfg, err := loadProjectConfig(cfgPath)
	if err != nil {
		return nil, err
	}

	resolved, previous, err := ResolveAdapters(cfg, projectDir, ResolveOpts{
		Flag:        opts.Adapters,
		NoAdapters:  opts.NoAdapters,
		Reconfigure: opts.Reconfigure,
		Interactive: opts.Interactive,
		Stdin:       opts.Stdin,
		Stdout:      opts.Stdout,
	})
	if err != nil {
		return nil, fmt.Errorf("resolve adapters: %w", err)
	}

	removed := setDifference(previous, resolved)
	cleanupReports, err := cleanupRemovedAdapters(projectDir, removed)
	if err != nil {
		return nil, fmt.Errorf("cleanup removed adapters: %w", err)
	}

	if err := persistAdapterList(cfgPath, resolved); err != nil {
		return nil, fmt.Errorf("persist adapter list: %w", err)
	}

	if err := syncAdaptersForProject(projectDir, resolved); err != nil {
		return nil, fmt.Errorf("adapter sync: %w", err)
	}

	return &InstallProjectReport{
		Adopted:         true,
		Adapters:        resolved,
		AdapterCleanups: cleanupReports,
	}, nil
}
```

Add `"fmt"` to imports if missing.

Update the caller in `install.go` (around line 121):

```go
if hasNaniteDir {
	return s.adoptExisting(projectDir, globalHome, opts)
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/service/install/ -run TestInstallProject_Adopt -v
```

Expected: PASS.

```bash
go test ./internal/service/install/...
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/service/install/adopt.go internal/service/install/install.go internal/service/install/integration_test.go
git commit -m "$(cat <<'EOF'
feat(install): adoptExisting uses ResolveAdapters flow

Same pattern as freshScaffold: load config, resolve, cleanup removed,
persist, sync. Adopt is the path that handles --reconfigure since the
project already exists with a .nanite/ dir.

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 14: Update migrateFromAgentrc to use the new flow

**Files:**
- Modify: `internal/service/install/migrate.go`
- Modify: `internal/service/install/install.go` (caller)

Same pattern as Tasks 12 and 13. The migrate path is more complex because it has phase markers and runs after the agentrc archive + carry-over completes. We add the resolve/cleanup/persist/sync flow after `ScaffoldNaniteMD` and before the existing `PhaseClaudeSync` marker. We keep the phase marker name to avoid breaking in-flight installs that have already written this state to disk.

- [ ] **Step 1: Update `migrateFromAgentrc` in `internal/service/install/migrate.go`**

Replace the section from `// CLAUDE.md surgery.` (around line 89) through `state.MarkPhaseComplete(PhaseAdapterSync)` (around line 112) with:

```go
	// Adapter selection + sync. Replaces the legacy CLAUDE.md surgery
	// path. The migrate path is always non-interactive in practice
	// (sub-agents during portfolio rollout) — the caller can pass
	// --no-adapters or --adapters explicitly to control behavior.
	cfgPath := filepath.Join(projectDir, ".nanite", "config.yaml")
	cfg, err := loadProjectConfig(cfgPath)
	if err != nil {
		return nil, err
	}

	resolved, previous, err := ResolveAdapters(cfg, projectDir, ResolveOpts{
		Flag:        opts.Adapters,
		NoAdapters:  opts.NoAdapters,
		Reconfigure: opts.Reconfigure,
		Interactive: opts.Interactive,
		Stdin:       opts.Stdin,
		Stdout:      opts.Stdout,
	})
	if err != nil {
		return nil, fmt.Errorf("resolve adapters: %w", err)
	}

	removed := setDifference(previous, resolved)
	cleanupReports, err := cleanupRemovedAdapters(projectDir, removed)
	if err != nil {
		return nil, fmt.Errorf("cleanup removed adapters: %w", err)
	}

	if err := persistAdapterList(cfgPath, resolved); err != nil {
		return nil, fmt.Errorf("persist adapter list: %w", err)
	}

	state.MarkPhaseComplete(PhaseClaudeSync) // legacy phase name retained
	if err := WriteState(statePath, state); err != nil {
		return nil, fmt.Errorf("write state after claude-sync: %w", err)
	}

	if err := syncAdaptersForProject(projectDir, resolved); err != nil {
		return nil, fmt.Errorf("adapter sync: %w", err)
	}
	state.MarkPhaseComplete(PhaseAdapterSync)
```

Update the function signature to take `opts InstallProjectOptions`:

```go
func (s *Service) migrateFromAgentrc(projectDir, globalHome string, opts InstallProjectOptions) (*InstallProjectReport, error) {
```

Update the return value to include the new fields:

```go
	return &InstallProjectReport{
		Migrated:        true,
		ArchivePath:     archiveDir,
		Adapters:        resolved,
		AdapterCleanups: cleanupReports,
	}, nil
```

Update the caller in `install.go` (around line 113):

```go
if opts.MigrateFromAgentrc {
	if !hasAgentrc {
		return nil, fmt.Errorf("--migrate-from-agentrc requires .agentrc/ in %s", projectDir)
	}
	return s.migrateFromAgentrc(projectDir, globalHome, opts)
}
```

- [ ] **Step 2: Update existing migrate test**

Find the existing migrate test (likely `TestInstallProject_Migrate` or similar) and update its assertions to expect the new `Adapters` field. If the test was relying on legacy CLAUDE.md content, update it to match the new behavior.

If the test uses a fixture project with no CLI files, the resolved list should be `[]` (non-interactive detection found nothing). Add to the test assertion:

```go
if len(report.Adapters) != 0 {
	t.Errorf("Adapters: got %v, want [] (no CLI files in fixture)", report.Adapters)
}
```

If the test uses a fixture that includes a `CLAUDE.md`, update the assertion to expect `[]string{"claude"}`.

- [ ] **Step 3: Run tests to verify they pass**

```bash
go test ./internal/service/install/...
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/service/install/migrate.go internal/service/install/install.go internal/service/install/integration_test.go
git commit -m "$(cat <<'EOF'
feat(install): migrateFromAgentrc uses ResolveAdapters flow

Replace legacy CLAUDE.md surgery in the migrate path with the same
ResolveAdapters/cleanup/persist/sync flow used by freshScaffold and
adoptExisting. Phase markers (PhaseClaudeSync, PhaseAdapterSync) are
retained to avoid breaking in-flight installs.

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 15: CLI flags + Service plumbing in install_cmd.go

**Files:**
- Modify: `cmd/nanite/install_cmd.go`

Add the three new flags (`--adapters`, `--no-adapters`, `--reconfigure`), wire them into `InstallProjectOptions`, populate `Interactive`/`Stdin`/`Stdout` from the CLI environment, and add the post-install summary line.

- [ ] **Step 1: Add new flags to `cmdInstall` flag parsing**

In `cmd/nanite/install_cmd.go`, after the existing flag declarations (after `printDiff`, around line 29), add:

```go
adapters := fs.String("adapters", "", "comma-separated list of CLI adapters to manage (claude, codex, gemini, opencode)")
noAdapters := fs.Bool("no-adapters", false, "disable all CLI adapter management for this project")
reconfigure := fs.Bool("reconfigure", false, "re-prompt for adapter selection even if config has adapters: set")
```

- [ ] **Step 2: Add mutual-exclusion validation**

After `fs.Parse(args)` (around line 30), add:

```go
if *adapters != "" && *noAdapters {
	installDie("flag conflict", fmt.Errorf("--adapters and --no-adapters are mutually exclusive"))
}
```

- [ ] **Step 3: Plumb flags into `InstallProjectOptions`**

Update the `opts := install.InstallProjectOptions{...}` block (around line 129):

```go
opts := install.InstallProjectOptions{
	ProjectDir:         projectDir,
	GlobalHome:         defaultGlobalHome(),
	MigrateFromAgentrc: *migrate,
	ArchiveOnly:        *archiveOnly,
	Adapters:           *adapters,
	NoAdapters:         *noAdapters,
	Reconfigure:        *reconfigure,
	Interactive:        isStdinTTY(),
	Stdin:              os.Stdin,
	Stdout:             os.Stdout,
}
```

- [ ] **Step 4: Add the post-install summary line**

After the existing `switch { case report.FreshScaffold: ... }` block (around line 159), add:

```go
// Post-install summary: show resolved adapters and any cleanup notices.
if len(report.Adapters) > 0 {
	fmt.Printf("%s install: adapters [%s] (%d enabled, %d disabled)\n",
		brand.BinaryName,
		strings.Join(report.Adapters, ", "),
		len(report.Adapters),
		4-len(report.Adapters), // 4 user-selectable adapters total
	)
} else if !report.ArchiveOnly {
	fmt.Printf("%s install: no CLI adapters enabled (run with --reconfigure to add them later)\n",
		brand.BinaryName)
}
for _, cleanup := range report.AdapterCleanups {
	switch cleanup.Action {
	case "stripped":
		fmt.Printf("%s install: removed managed section from %s (re-run with --reconfigure to add it back)\n",
			brand.BinaryName, filepath.Base(cleanup.FilePath))
	case "deleted":
		fmt.Printf("%s install: deleted %s (was managed-section-only)\n",
			brand.BinaryName, filepath.Base(cleanup.FilePath))
	}
}
```

Add `"strings"` to the import block if missing.

- [ ] **Step 5: Build and verify the binary compiles**

```bash
cd ~/Projects-apps/nanite-adapter-selection
go build -o /tmp/nanite-feature ./cmd/nanite
```

Expected: success.

- [ ] **Step 6: Smoke test the flags manually**

```bash
/tmp/nanite-feature install --help 2>&1 | grep -E "adapters|reconfigure"
```

Expected: shows `--adapters`, `--no-adapters`, `--reconfigure` in the flag list.

```bash
/tmp/nanite-feature install --adapters frobnicate --project /tmp/test-x 2>&1
```

Expected: error mentioning unknown adapter (the project dir doesn't need to exist for the flag to be validated when ResolveAdapters parses it).

- [ ] **Step 7: Run the full test suite**

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add cmd/nanite/install_cmd.go
git commit -m "$(cat <<'EOF'
feat(cli): wire --adapters/--no-adapters/--reconfigure flags

Add the three new flags to the install command, plumb them into
InstallProjectOptions along with the Interactive/Stdin/Stdout fields
needed by the prompt. Add post-install summary lines that show the
resolved adapter list and any cleanup notices.

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 16: NANITE.md template addition

**Files:**
- Modify: `internal/assets/framework/templates/NANITE.md.tmpl`
- Modify: `internal/service/install/scaffold_test.go` or relevant test

Add the "First-time setup" block to the NANITE.md template per spec section 2. The instruction is permanent — its condition (`agents:` empty) makes it self-deactivating.

- [ ] **Step 1: Read the current template**

```bash
cat internal/assets/framework/templates/NANITE.md.tmpl
```

You'll see the current structure with sections: Agents, How to boot, Project-specific notes.

- [ ] **Step 2: Update the template**

Edit `internal/assets/framework/templates/NANITE.md.tmpl`. After the `## Agents` section and before `## How to boot`, insert:

```markdown
## First-time setup

If `.nanite/config.yaml` has an empty `agents:` block, this project hasn't been
configured yet. As the agent reading this file: please offer to help the user
define their first agent. Ask about the project's purpose and tech stack,
suggest roles from `~/.nanite/roles/` (browse domain/, stack/, meta/), then
edit `agents:` and re-run `nanite install --project .` to refresh the managed
sections.

```

Also update the existing "How to boot" section's last paragraph to match the new behavior. Find:

```
The four CLI files above are kept in sync automatically by `nanite install`'s adapter pipeline. Edit your project content outside the `<!-- nanite:start --> ... <!-- nanite:end -->` markers — anything inside will be overwritten on the next install or refresh.
```

Replace with:

```
The four CLI files above are managed by `nanite install` based on the `adapters:` list in `.nanite/config.yaml`. Only the listed adapters' files are created or updated. Edit your project content outside the `<!-- nanite:start --> ... <!-- nanite:end -->` markers — anything inside will be overwritten on the next install or refresh. Run `nanite install --project . --reconfigure` to change which adapters are managed.
```

- [ ] **Step 3: Find the existing scaffold test for NANITE.md**

```bash
grep -rn "ScaffoldNaniteMD\|NANITE.md" internal/service/install/*_test.go | head -10
```

There's likely an existing test that asserts NANITE.md gets created. Update it to assert the new "First-time setup" section is present:

```go
data, _ := os.ReadFile(filepath.Join(dir, "NANITE.md"))
if !strings.Contains(string(data), "First-time setup") {
	t.Error("NANITE.md missing First-time setup section")
}
if !strings.Contains(string(data), "offer to help the user") {
	t.Error("NANITE.md missing agent setup instruction")
}
```

- [ ] **Step 4: Rebuild assets and test**

The framework templates are embedded via `//go:embed`, so they're compiled into the binary. Just `go build` and `go test` will pick them up.

```bash
go test ./internal/service/install/... -v -run NaniteMD
```

Expected: PASS.

```bash
go build -o /tmp/nanite-feature ./cmd/nanite
```

Expected: success.

- [ ] **Step 5: Commit**

```bash
git add internal/assets/framework/templates/NANITE.md.tmpl internal/service/install/integration_test.go
git commit -m "$(cat <<'EOF'
docs(template): NANITE.md adds First-time setup instruction

New section in the project NANITE.md template that primes the agent
to offer help defining the first agent when agents: is empty. The
condition (empty agents:) makes the instruction self-deactivating
without templating logic.

Also update the "How to boot" section to mention the new adapters:
selection list and --reconfigure flag.

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 17: Sweep — remove dead code and obsolete tests

**Files:**
- Possibly: `internal/service/install/install.go` (`buildManagedSection` is now dead)
- Possibly: `internal/service/install/claudemd_test.go` (legacy direct path tests)

After Tasks 12-14, the legacy direct CLAUDE.md path is no longer called from any install path. The `buildManagedSection` helper at `install.go:200-217` is dead. Decide whether to delete it or leave it for documentation purposes. Per the spec's "Out of scope" — `UpdateCLAUDEmd` itself stays as a utility, but `buildManagedSection` was only used for the legacy path and should go.

- [ ] **Step 1: Check what calls `buildManagedSection`**

```bash
cd ~/Projects-apps/nanite-adapter-selection
grep -rn "buildManagedSection" .
```

Expected: only the definition at `install.go:200`. No callers.

- [ ] **Step 2: Delete `buildManagedSection`**

Remove lines 197-217 (the function and its preceding comment) from `internal/service/install/install.go`.

- [ ] **Step 3: Check what tests reference legacy direct CLAUDE.md path**

```bash
grep -n "UpdateCLAUDEmd\|CLAUDEUpdateReport" internal/service/install/*_test.go
```

Identify any test that asserts the install service writes CLAUDE.md via `UpdateCLAUDEmd`. Those tests are obsolete — the install service no longer calls `UpdateCLAUDEmd` directly. The `claudemd_test.go` tests of `UpdateCLAUDEmd` itself (calling it as a helper) should remain, but any integration test that asserted "after install, CLAUDE.md was modified by UpdateCLAUDEmd" should be deleted.

- [ ] **Step 4: Remove obsolete tests**

For each test you found in Step 3 that tests integration behavior (not the helper function itself), delete it. Keep tests that exercise `UpdateCLAUDEmd` as a unit test of the helper.

- [ ] **Step 5: Build and run all tests**

```bash
go build ./...
go test ./...
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/service/install/install.go internal/service/install/claudemd_test.go
git commit -m "$(cat <<'EOF'
chore(install): remove dead buildManagedSection and obsolete tests

buildManagedSection was only used by the legacy direct CLAUDE.md write
path, which has been replaced by the adapter-driven flow. The helper
has no callers and is removed. UpdateCLAUDEmd itself stays as a utility
(used by the migrate path's snapshot logic) and its unit tests stay.

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 18: Manual smoke test on chrispian.dev

**Files:** No file changes — verification only.

End-to-end manual verification of all the major flows on a real project. Document findings inline so the next task (PR) has results to reference.

- [ ] **Step 1: Reset chrispian.dev to a clean state**

```bash
ls -la ~/Projects/chrispian.dev/
```

Note what's there (likely `CLAUDE.md`, `NANITE.md`, `.nanite/`, plus the existing `docs/` and `.DS_Store` from the earlier test). To start fresh:

```bash
rm -rf ~/Projects/chrispian.dev/.nanite/ ~/Projects/chrispian.dev/CLAUDE.md ~/Projects/chrispian.dev/NANITE.md ~/Projects/chrispian.dev/AGENTS.md ~/Projects/chrispian.dev/GEMINI.md ~/Projects/chrispian.dev/OPENCODE.md
ls ~/Projects/chrispian.dev/
```

Expected: just `docs/` and `.DS_Store`.

- [ ] **Step 2: Build the feature binary**

```bash
cd ~/Projects-apps/nanite-adapter-selection
go build -o /tmp/nanite-feature ./cmd/nanite
```

Expected: success.

- [ ] **Step 3: Test 1 — fresh install with detection finding nothing, interactive picker (skip)**

```bash
/tmp/nanite-feature install --project ~/Projects/chrispian.dev
```

Expected: prompt shows numbered list with no `*` markers, no detected list. Enter empty input. Output should report `no CLI adapters enabled`. Verify:

```bash
ls ~/Projects/chrispian.dev/
cat ~/Projects/chrispian.dev/.nanite/config.yaml
```

Expected: `NANITE.md` exists, no CLI files, `config.yaml` has `adapters: []`.

- [ ] **Step 4: Test 2 — `--reconfigure` to add claude**

```bash
/tmp/nanite-feature install --project ~/Projects/chrispian.dev --reconfigure
```

Expected: prompt shows numbered list with no `*` markers (current is empty). Enter `1`. Output should report `adapters [claude]`. Verify:

```bash
ls ~/Projects/chrispian.dev/
cat ~/Projects/chrispian.dev/CLAUDE.md
cat ~/Projects/chrispian.dev/.nanite/config.yaml
```

Expected: `CLAUDE.md` exists with placeholder content (no agents in config). `config.yaml` has `adapters: [claude]`.

- [ ] **Step 5: Test 3 — `--reconfigure` to remove claude (cleanup)**

```bash
/tmp/nanite-feature install --project ~/Projects/chrispian.dev --reconfigure
```

Expected: prompt shows numbered list with `1) claude *`. Enter empty (keep current — but we want to remove it, so) enter a number that doesn't include 1, e.g., `2`. Output should report cleanup notice. Actually, to test removal, enter just `2` (codex). Output should report `adapters [codex]` AND a cleanup notice for CLAUDE.md.

Verify:

```bash
ls ~/Projects/chrispian.dev/
```

Expected: `CLAUDE.md` was deleted (it was managed-section-only), `AGENTS.md` exists with placeholder content.

- [ ] **Step 6: Test 4 — `--no-adapters` flag**

```bash
/tmp/nanite-feature install --project ~/Projects/chrispian.dev --no-adapters --reconfigure
```

Expected: no prompt. Output reports `no CLI adapters enabled` and a cleanup notice for AGENTS.md. Verify:

```bash
ls ~/Projects/chrispian.dev/
cat ~/Projects/chrispian.dev/.nanite/config.yaml
```

Expected: only `NANITE.md` and `docs/`. `config.yaml` has `adapters: []`.

- [ ] **Step 7: Test 5 — `--adapters` explicit flag**

```bash
/tmp/nanite-feature install --project ~/Projects/chrispian.dev --adapters claude,gemini --reconfigure
```

Expected: no prompt. Output reports `adapters [claude, gemini]`. Verify:

```bash
ls ~/Projects/chrispian.dev/
```

Expected: `CLAUDE.md`, `GEMINI.md`, `NANITE.md`, `docs/`. No `AGENTS.md` or `OPENCODE.md`.

- [ ] **Step 8: Test 6 — invalid adapter slug**

```bash
/tmp/nanite-feature install --project ~/Projects/chrispian.dev --adapters frobnicate --reconfigure 2>&1
```

Expected: error mentioning unknown adapter. Exit code non-zero. Verify the project files are unchanged from Test 5.

- [ ] **Step 9: Test 7 — pre-existing CLAUDE.md detection**

Reset:

```bash
rm -rf ~/Projects/chrispian.dev/.nanite/ ~/Projects/chrispian.dev/CLAUDE.md ~/Projects/chrispian.dev/AGENTS.md ~/Projects/chrispian.dev/GEMINI.md ~/Projects/chrispian.dev/OPENCODE.md ~/Projects/chrispian.dev/NANITE.md
echo "# Existing user CLAUDE.md" > ~/Projects/chrispian.dev/CLAUDE.md
```

Then:

```bash
/tmp/nanite-feature install --project ~/Projects/chrispian.dev
```

Expected: prompt shows `Detected CLI tools in this project: claude` and asks Y/n/e. Enter `y`. Output reports `adapters [claude]`. Verify:

```bash
cat ~/Projects/chrispian.dev/CLAUDE.md
```

Expected: the user's `# Existing user CLAUDE.md` heading is preserved (outside the markers), and a managed section with placeholder content is appended.

- [ ] **Step 10: Test 8 — first-time setup hint in NANITE.md**

```bash
cat ~/Projects/chrispian.dev/NANITE.md
```

Expected: contains the "First-time setup" section telling the agent to offer help defining agents.

- [ ] **Step 11: Document smoke test results**

Append a brief results note to the spec (or create a new note in `docs/superpowers/notes/`):

```bash
cat >> docs/superpowers/specs/2026-04-09-nanite-adapter-selection-design.md <<'EOF'

---

## Implementation Smoke Test (2026-04-09)

Verified end-to-end on `~/Projects/chrispian.dev`:

- ✅ Fresh install, no detection, interactive empty → `adapters: []`
- ✅ `--reconfigure` to add claude → CLAUDE.md placeholder created
- ✅ `--reconfigure` to switch to codex → CLAUDE.md cleanup + AGENTS.md created
- ✅ `--no-adapters` → all CLI files cleaned up
- ✅ `--adapters claude,gemini` → exactly those two files
- ✅ Invalid `--adapters frobnicate` → clear error, no changes
- ✅ Pre-existing CLAUDE.md detected, user content outside markers preserved
- ✅ First-time setup hint present in NANITE.md
EOF
```

- [ ] **Step 12: Commit smoke test results**

```bash
git add docs/superpowers/specs/2026-04-09-nanite-adapter-selection-design.md
git commit -m "$(cat <<'EOF'
docs: smoke test results for adapter selection on chrispian.dev

Verified all major flows: fresh install, --reconfigure shrink/grow,
--no-adapters cleanup, --adapters explicit list, pre-existing CLI file
detection, first-time setup hint. All passed.

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 19: Final test sweep + open PR

**Files:** No file changes.

Run the complete test suite one more time, push the branch, and open a PR.

- [ ] **Step 1: Run all tests**

```bash
cd ~/Projects-apps/nanite-adapter-selection
go test ./... -race
```

Expected: PASS with no race warnings.

- [ ] **Step 2: Run go vet**

```bash
go vet ./...
```

Expected: no warnings.

- [ ] **Step 3: Build the release binary**

```bash
go build -o /tmp/nanite-final ./cmd/nanite
ls -la /tmp/nanite-final
```

Expected: success.

- [ ] **Step 4: Push the branch**

```bash
git push -u origin feature/adapter-selection
```

- [ ] **Step 5: Open the PR**

```bash
gh pr create --title "feat(install): user-controlled CLI adapter selection" --body "$(cat <<'EOF'
## Summary

- Make CLI adapter selection (CLAUDE.md / AGENTS.md / GEMINI.md / OPENCODE.md) user-controlled and explicit
- Remove the legacy direct CLAUDE.md write from `install.go`
- Add detection + interactive prompt + persistence + cleanup-on-removal
- New flags: `--adapters`, `--no-adapters`, `--reconfigure`
- New tri-state `adapters:` key in `.nanite/config.yaml`
- Adapters write a placeholder section when no agents are configured (instead of short-circuiting)
- NANITE.md template now includes a "First-time setup" hint that primes the agent to help users define their first agent

## Spec & Plan

- Spec: `docs/superpowers/specs/2026-04-09-nanite-adapter-selection-design.md`
- Plan: `docs/superpowers/plans/2026-04-09-nanite-adapter-selection.md`

## Test plan

- [x] All unit tests pass (`go test ./...`)
- [x] Race detector clean (`go test -race ./...`)
- [x] `go vet ./...` clean
- [x] Manual smoke test on chrispian.dev (8 scenarios — see spec appendix)
- [ ] CI green
EOF
)"
```

- [ ] **Step 6: Mark task complete**

After the PR opens successfully and CI starts running, the implementation is done. Hand the PR back to the user for review and merge.

---

## Out of Scope (not implemented in this plan)

- Per-adapter custom managed section content beyond per-adapter constants
- New CLI adapters (CrewAI, AutoGen, Continue, etc.)
- DB sync of the adapter list — `.nanite/config.yaml` is the source of truth
- TUI library adoption
- Cross-project adapter defaults (a global `~/.nanite/config.yaml` adapter preference)
- Removing the `UpdateCLAUDEmd` helper itself — it stays as a utility for `claudemd.go` snapshot logic
