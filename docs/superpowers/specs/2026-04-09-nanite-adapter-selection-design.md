# Nanite Adapter Selection Design

**Date:** 2026-04-09
**Status:** Approved
**Scope:** Make CLI adapter selection user-controlled and explicit. Remove the legacy direct CLAUDE.md write. Add detection + interactive prompt + persistence + cleanup-on-removal. All four CLI files (CLAUDE.md, AGENTS.md, GEMINI.md, OPENCODE.md) become opt-in instead of partially-opt-in.
**Builds on:** `2026-04-08-agent-adapter-architecture-design.md`, `2026-04-09-nanite-agentrc-consolidation-design.md`

---

## Summary

Today the install service writes `CLAUDE.md` unconditionally via a legacy direct call (`install.go:157`) and then runs the adapter pipeline, which short-circuits each of the four CLI adapters (`adapter-claude`, `adapter-codex`, `adapter-gemini`, `adapter-opencode`) on `len(agents) == 0`. The result is a fresh install that creates `CLAUDE.md` only — never the other three files — even though the NANITE.md template promises all four are kept in sync.

This is a design pivot, not a bug fix. The original 2026-04-08 spec said "if the file doesn't exist, the adapter creates it" — but the consolidation work landed with a special-case CLAUDE.md path that contradicted both the spec and the actual short-circuit behavior in the other adapters. The deeper question the user's pushback raises is: **the framework should not be opinionated about which CLI tools a project uses.** A user who only uses Nanite shouldn't get a CLAUDE.md they didn't ask for. A user who only uses Claude Code shouldn't get AGENTS.md/GEMINI.md/OPENCODE.md polluting their project root.

The fix: introduce a user-controlled adapter selection list in `.nanite/config.yaml`, populated either by interactive prompt (with detection-based suggestions) on first install, or explicitly via CLI flag, or persisted from a prior choice. The legacy direct CLAUDE.md write is removed. Each adapter becomes responsible for its own file lifecycle (create, update, remove) gated by whether it's in the selected list.

---

## Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Selection storage | New `adapters: [...]` key in `.nanite/config.yaml`, tri-state (absent / empty / populated) | One key, three meanings; absent triggers detection/prompt, empty is "user said no", populated is the active list |
| Detection signals | File presence at project root (`CLAUDE.md`, `AGENTS.md`, `GEMINI.md`, `OPENCODE.md`) OR adapter-specific subdir (`.claude/`, `.codex/`, `.gemini/`, `.opencode/`) | Either signal indicates "this CLI is in use here" with high confidence |
| Default behavior on fresh install | Detect → if interactive, prompt with detected list pre-selected → if non-interactive, use detection result as-is | Respects existing project state without forcing choice; CI/scripted installs are deterministic |
| Brand-new project (nothing detected) prompt | Show all four adapters as a numbered list with no pre-selection, accept space-separated numbers | Most explicit, no opinion baked in |
| Pre-selection display on `--reconfigure` | Show currently-enabled adapters with `*` marker, replace-not-toggle on input | Clear visual state, simple input model |
| Empty input on `--reconfigure` | Keep current list (non-destructive) | `--reconfigure` is a destructive context; accidental Enter shouldn't wipe the list |
| Empty input on fresh install prompt | Means "none" → write `adapters: []` | Fresh install has no current state to preserve |
| Adapter behavior on empty agents | Write a placeholder managed section ("No agents configured yet — see NANITE.md") instead of short-circuiting | Once selected, the file always exists with markers; re-running install picks up new agents |
| `nanite-native` adapter visibility | Hidden from user-facing list, always loaded internally | It's not a CLI integration — it manages `.nanite/` and NANITE.md, which are always Nanite's |
| Removal-of-adapter cleanup | When the resolved list shrinks vs. previously-persisted list, strip the managed section from each dropped adapter's target file. Delete the file if it becomes empty | Keeps config and filesystem in sync; matches "no surprise on next run" |
| Cleanup notice | Print one line per removed adapter with how-to-restore | User awareness without prompting |
| Existing-install migration on decline (Scenario B) | Strip the legacy-written CLAUDE.md managed section if user declines on first prompt | Same "config = filesystem" rule; consistent with `--reconfigure` |
| New CLI flags | `--adapters X,Y` (explicit list, wins everything), `--no-adapters` (= empty), `--reconfigure` (re-prompt) | Mirrors existing flag conventions in install_cmd.go |
| First-time agent setup hint | Add an instruction block to NANITE.md template telling agents to offer help defining agents when `agents:` is empty | Bootstraps users into the framework conversationally; lives in NANITE.md (the canonical boot prompt) and is permanent (acts only when condition met) |
| Legacy direct CLAUDE.md write | Removed from `install.go:157`. The `claude` adapter handles it instead | One write path, one source of truth |
| `UpdateCLAUDEmd` helper | Stays as a utility, no longer called from `install.go` | Used internally by the claude adapter |

---

## 1. Architecture Overview

The change is contained to the install service and the four CLI adapters' `SyncProjectRoot` implementations. No changes to discovery, sandbox population, MCP, or A2A.

```
nanite install --project <dir>
  │
  ├─ Service.InstallProject (existing)
  │     │
  │     ├─ detect partial / adopt / migrate / fresh (existing)
  │     │
  │     └─ freshScaffold / adopt / migrate path
  │           │
  │           ├─ ScaffoldNaniteDir          (existing, unchanged)
  │           ├─ ScaffoldNaniteMD            (existing, content updated)
  │           ├─ ResolveAdapters             ← NEW
  │           │     │
  │           │     ├─ Read flags (--adapters, --no-adapters, --reconfigure)
  │           │     ├─ Read .nanite/config.yaml adapters: key
  │           │     ├─ DetectAdapters()      ← NEW (filesystem scan)
  │           │     ├─ promptAdapterSelection() ← NEW (TTY only)
  │           │     ├─ Persist resolved list back to config.yaml
  │           │     └─ Compute removed = previous - resolved
  │           │
  │           ├─ cleanupRemovedAdapters     ← NEW (strip + maybe delete)
  │           │
  │           └─ syncAdaptersForProject(allowedAdapters) ← FILTERED
  │                 │
  │                 └─ For each adapter in registry whose Name() ∈ allowedAdapters:
  │                       adapter.SyncProjectRoot(projectDir, agents)
  │                         (no more len(agents)==0 short-circuit;
  │                          writes placeholder if empty)
```

The legacy `UpdateCLAUDEmd()` call at `install.go:157` is **deleted**. CLAUDE.md is no longer special — it's one of N files managed by N adapters via the same path.

---

## 2. Config Schema

`.nanite/config.yaml` gains one optional top-level key:

```yaml
nanite_version: 2.3.0

adapters:           # NEW — optional, tri-state
  - claude
  - codex

agents:
  frontend:
    name: Frontend Developer
    ...
```

### Tri-state semantics

| YAML state | Parsed Go value | Meaning | Install behavior |
|---|---|---|---|
| Key absent | `Adapters == nil` | Never been chosen | Run detection. Prompt if TTY. Persist result. |
| `adapters: []` | `Adapters != nil, len == 0` | Explicit "none" | No prompt, no detection. No CLI files written. |
| `adapters: [claude]` | `Adapters != nil, len > 0` | Active list | No prompt, no detection. Sync exactly the listed adapters. |

The tri-state distinction requires using a `*[]string` or a sentinel type in the parsed config struct so absence is distinguishable from empty.

### Seed template change

The fresh-install `.nanite/config.yaml` template currently emits `agents: {}`. The new template emits no `adapters:` key at all (so the first `nanite install --project .` triggers the detect/prompt flow). The `agents:` block continues to ship as `agents: {}` with a commented example.

### NANITE.md template addition

Add a permanent "First-time setup" block to the NANITE.md template:

```markdown
## First-time setup

If `.nanite/config.yaml` has an empty `agents:` block, this project hasn't been
configured yet. As the agent reading this file: please offer to help the user
define their first agent. Ask about the project's purpose and tech stack,
suggest roles from `~/.nanite/roles/` (browse domain/, stack/, meta/), then
edit `agents:` and re-run `nanite install --project .` to refresh the managed
sections.
```

The instruction is permanent in the template — its condition (`agents:` is empty) makes it self-deactivating without templating logic.

---

## 3. Detection Rules

`func DetectAdapters(projectDir string) []string` lives in a new file `internal/service/install/adapter_detect.go`.

| Adapter | Evidence (any one triggers) |
|---|---|
| `claude` | `<projectDir>/CLAUDE.md` exists OR `<projectDir>/.claude/` is a directory |
| `codex` | `<projectDir>/AGENTS.md` exists OR `<projectDir>/.codex/` is a directory |
| `gemini` | `<projectDir>/GEMINI.md` exists OR `<projectDir>/.gemini/` is a directory |
| `opencode` | `<projectDir>/OPENCODE.md` exists OR `<projectDir>/.opencode/` is a directory |

`nanite-native` is **not** in this map. It's loaded unconditionally inside the install service because it manages `.nanite/` and `NANITE.md` itself, which are always Nanite's responsibility.

Returns a slice of slugs (no fixed order; the prompt sorts them deterministically).

---

## 4. Resolution Logic

`func ResolveAdapters(cfg *projectConfig, projectDir string, opts ResolveOpts) (resolved []string, previous []string, err error)` lives in a new file `internal/service/install/adapter_select.go`.

```go
type ResolveOpts struct {
    Flag         string  // --adapters value, empty = unset
    NoAdapters   bool    // --no-adapters
    Reconfigure  bool    // --reconfigure
    Interactive  bool    // isStdinTTY() result
    Stdin        io.Reader // injection point for tests
    Stdout       io.Writer
}
```

### Priority order

`ResolveAdapters` is **read-only** — it computes and returns the resolved list and the previous list. It does not write to disk. The caller (`freshScaffold`/`adopt`/`migrate` in `install.go`) is responsible for cleanup-then-persist ordering, because cleanup needs to compute `previous - resolved` *before* the persisted list changes.

1. `opts.NoAdapters == true` → return `(resolved=[], previous=cfg.Adapters)`
2. `opts.Flag != ""` → split on `,`, validate each slug against the registry, return `(resolved=parsed, previous=cfg.Adapters)`
3. `opts.Reconfigure == false` AND `cfg.Adapters != nil` → return `(resolved=cfg.Adapters, previous=cfg.Adapters)` (no-op case; cleanup diff is empty)
4. (We're now in detect/prompt territory — either fresh install or `--reconfigure`)
5. Run `DetectAdapters(projectDir)` → `detected []string`
6. Determine "current" list for the prompt: if `--reconfigure` and `cfg.Adapters != nil`, current = `cfg.Adapters`. Otherwise current = `detected`.
7. If `opts.Interactive == true` → call `promptAdapterSelection(detected, current, opts.Reconfigure, ...)` → user list
8. If non-interactive → use `current` (which is `detected` for fresh install, or `cfg.Adapters` for `--reconfigure`). Non-interactive `--reconfigure` is documented as "no-op against existing config."
9. Return `(resolved, previous=cfg.Adapters, nil)`. Caller handles persistence.

### Slug validation

The registry-aware validation rejects unknown slugs with a clear error: `nanite install: unknown adapter "frobnicate" (available: claude, codex, gemini, opencode)`. `nanite-native` is rejected from user input — it's never user-facing.

---

## 5. Prompt UX

Lives in `cmd/nanite/install_cmd.go` next to `handlePartialInteractive`. Reuses `isStdinTTY()` and the `fmt.Scanln` pattern.

### Fresh install, detection found something

```
Detected CLI tools in this project: claude, codex
Manage these with Nanite? [Y]es / [n]o / [e]dit list
> 
```

- `y` or empty → accept detected list as resolved
- `n` → write `adapters: []`
- `e` → fall through to the edit prompt below

### Fresh install, detection found nothing (or `e` chosen above)

```
Select CLI tools to manage with Nanite:
  1) claude
  2) codex
  3) gemini
  4) opencode
Enter numbers separated by spaces (empty for none):
> 
```

- Empty input on fresh install → `[]`
- Invalid input → re-prompt up to 3 times, then fall back to `[]` with a warning

### `--reconfigure` (config already has `adapters:`)

```
Select CLI tools to manage with Nanite (* = currently enabled):
  1) claude *
  2) codex
  3) gemini *
  4) opencode
Enter numbers separated by spaces (empty to keep current):
> 
```

- Empty input on `--reconfigure` → **keep current** (non-destructive)
- Numbers replace the list (not toggle individual items)
- Invalid input → re-prompt up to 3 times, then keep current

### Implementation note

The prompt function returns the chosen `[]string`. It does not write to disk — the caller (`ResolveAdapters`) handles persistence. This keeps the prompt testable with injected `io.Reader`/`io.Writer`.

---

## 6. CLI Surface

New flags on the existing `nanite install` command in `cmd/nanite/install_cmd.go`:

| Flag | Type | Effect |
|---|---|---|
| `--adapters claude,codex` | string | Explicit list. Validated against registry. Wins over config and detection. |
| `--no-adapters` | bool | Equivalent to `--adapters ""`. Convenience for "I only want Nanite-native." |
| `--reconfigure` | bool | Re-runs detection and prompt even if config has `adapters:` set. Triggers cleanup if list shrinks. |

Mutually exclusive: `--adapters` and `--no-adapters` cannot be combined. `--reconfigure` is ignored if `--adapters` is given (explicit always wins).

### Output

After install completes, print a summary line listing the active adapters:

```
nanite install: adapters [claude, codex] (2 enabled, 2 disabled)
```

Or for the empty case:

```
nanite install: no CLI adapters enabled (run with --reconfigure to add them later)
```

---

## 7. Adapter Behavior Change

Each of the four CLI adapters' `SyncProjectRoot` implementations:

```go
// BEFORE
func (a *Adapter) SyncProjectRoot(projectDir string, agents []store.AgentProfile) error {
    if len(agents) == 0 {
        return nil
    }
    content := buildNaniteAgentsSection(agents)
    return agent.WriteManagedSection(filepath.Join(projectDir, "AGENTS.md"), content)
}

// AFTER
func (a *Adapter) SyncProjectRoot(projectDir string, agents []store.AgentProfile) error {
    var content string
    if len(agents) == 0 {
        content = placeholderContent  // package constant
    } else {
        content = buildNaniteAgentsSection(agents)
    }
    return agent.WriteManagedSection(filepath.Join(projectDir, "AGENTS.md"), content)
}
```

### Placeholder content (per adapter)

Each adapter defines its own `placeholderContent` constant. Suggested template:

```markdown
## Nanite Agents

No agents configured for this project yet. See NANITE.md for setup help, or
add an agent definition to `.nanite/config.yaml` and re-run `nanite install
--project .`.
```

The constant is per-adapter so each can customize wording (e.g., the codex one might use `## Nanite Agents` to fit the AGENTS.md schema, the gemini one might omit the heading entirely if Gemini renders differently). Initial implementation: identical content across all four; tune later based on each CLI's parser.

`adapter-nanite-native` is **unchanged** — its `SyncProjectRoot` remains a no-op.

---

## 8. Cleanup Logic (Removed Adapters)

`func cleanupRemovedAdapters(projectDir string, removed []string) ([]CleanupReport, error)` lives in `internal/service/install/adapter_cleanup.go`.

For each removed adapter:

1. Resolve target file path (per adapter — same map as detection)
2. Read the file. If it doesn't exist → no-op, skip.
3. Strip the `<!-- nanite:start --> ... <!-- nanite:end -->` block (preserving any user content outside the markers — same logic as `UpdateCLAUDEmd` cleanup path).
4. If the resulting file is empty or whitespace-only → delete the file.
5. Otherwise write the cleaned content back.
6. Append a `CleanupReport{Adapter, FilePath, Action: "stripped" | "deleted"}` to the return slice.

### Edge cases

| Case | Behavior |
|---|---|
| Target file doesn't exist | No-op, no error |
| Markers missing (file is fully user-owned) | Skip, log warning, do not modify |
| Content outside markers | Preserved verbatim |
| File becomes empty after strip | Deleted |
| Read/write error | Return error, fail the install (rollback recovers via existing snapshot) |

### Snapshot for rollback

The existing `snapshotAdapterTargets()` already snapshots all four CLI files to the archive dir before the install starts (it walks `adapterTargetFiles` unconditionally). Cleanup is automatically rollback-safe because the pre-install file states are already captured. No new snapshot logic needed.

### Notice format

Per cleanup report, print one line:

```
nanite install: removed managed section from CLAUDE.md (re-run with --reconfigure to add it back)
nanite install: removed AGENTS.md (was managed-section-only)
```

The "deleted" notice is distinct so the user knows the file is gone, not just modified.

---

## 9. Integration Points

### `freshScaffold` path (`install.go:145-172`)

```go
func (s *Service) freshScaffold(projectDir, globalHome string) (*InstallProjectReport, error) {
    src := ScaffoldSource{...}
    if err := ScaffoldNaniteDir(projectDir, globalHome, src); err != nil { ... }
    if err := ScaffoldNaniteMD(projectDir, src); err != nil { ... }

    // REMOVED: managed := buildManagedSection(src)
    // REMOVED: report, err := UpdateCLAUDEmd(filepath.Join(projectDir, "CLAUDE.md"), managed, nil)

    cfg, err := loadProjectConfig(projectDir)
    if err != nil { return nil, err }

    resolved, previous, err := ResolveAdapters(cfg, projectDir, s.resolveOpts())
    if err != nil { return nil, fmt.Errorf("resolve adapters: %w", err) }

    removed := setDifference(previous, resolved)  // previous is nil → removed is empty
    cleanupReports, err := cleanupRemovedAdapters(projectDir, removed)
    if err != nil { return nil, fmt.Errorf("cleanup removed adapters: %w", err) }

    if err := persistAdapterList(projectDir, resolved); err != nil {
        return nil, fmt.Errorf("persist adapter list: %w", err)
    }

    if err := syncAdaptersForProject(projectDir, resolved); err != nil {
        return nil, fmt.Errorf("adapter sync: %w", err)
    }

    return &InstallProjectReport{
        FreshScaffold:    true,
        Adapters:         resolved,
        AdapterCleanups:  cleanupReports,
    }, nil
}
```

### `adopt` path

Same `ResolveAdapters → cleanup → persist → sync` sequence. Adopt is the most common case for `--reconfigure` since the project already exists.

### `migrate-from-agentrc` path

Same sequence runs once after migration completes, since the migrated `config.yaml` arrives without an `adapters:` key (legacy `.agentrc/` doesn't have the concept).

### `syncAdaptersForProject` signature change

```go
// BEFORE
func syncAdaptersForProject(projectDir string) error

// AFTER
func syncAdaptersForProject(projectDir string, allowedAdapters []string) error
```

The function still constructs the registry via `newBuiltinAdapterRegistry()` and reads agents via `extractAgentsFromConfig()`, but now wraps `reg.SyncAllProjectRoots(...)` with a filter: only adapters whose `Name()` is in `allowedAdapters` (plus `nanite-native`, always) actually run. The simplest implementation: build a one-off filtered `AdapterRegistry`, or add a new `reg.SyncAllProjectRootsFiltered(projectDir, agents, allowed)` method to the registry.

I prefer the latter — it keeps the filter logic inside the registry where adapter iteration already lives, and the install service stays slim.

---

## 10. Migration of Existing Installs

Three scenarios after this change ships, for projects already on Nanite that predate it.

### Scenario A — Accept on first prompt

| State going in | What happens |
|---|---|
| `.nanite/config.yaml` has no `adapters:` key. `CLAUDE.md` exists from legacy direct write. | Detection finds `CLAUDE.md` → prompt → user accepts `[claude]`. Claude adapter takes over `CLAUDE.md`: the existing `<!-- nanite:start --> ... <!-- nanite:end -->` markers are reused, but the content inside may differ from what `UpdateCLAUDEmd` wrote (the adapter has its own content format). Any user content outside the markers is preserved. Config gets `adapters: [claude]`. Subsequent installs are silent. |

### Scenario B — Decline on first prompt

| State going in | What happens |
|---|---|
| Same as A, user picks "none" at prompt | Config gets `adapters: []`. The previously-implicit `[claude]` becomes the "removed" set. Cleanup runs against `[claude]` → managed section stripped from `CLAUDE.md`. If `CLAUDE.md` was managed-section-only, it's deleted. Notice printed. |

This is the only mildly-surprising path. The notice will be loud:

```
nanite install: removed managed section from CLAUDE.md (re-run with --reconfigure to add it back)
nanite install: deleted CLAUDE.md (was managed-section-only)
```

### Scenario C — Pre-existing other CLI files

| State going in | What happens |
|---|---|
| Project has `AGENTS.md` and/or `GEMINI.md` from other tools but never went through Nanite install before | Detection finds them → prompt shows them as detected → user accepts/edits. Selected adapters take over via standard `WriteManagedSection` (which preserves user content outside the markers). |

### Non-interactive migration

For projects migrated via the Phase 5 portfolio rollout (sub-agents running `nanite install` non-interactively), the path is:

- Detection runs
- Detected list is used as-is (no prompt)
- Persisted to config

This means the rollout will pick up whatever CLI files exist in each project. For projects that should stay "Nanite-only," the rollout script can pass `--no-adapters` explicitly per project. Documented in the per-project rollout script.

---

## 11. Testing Strategy

### Unit tests

| Test file | Coverage |
|---|---|
| `adapter_detect_test.go` | Table-driven across all 16 file-presence combinations + `.cli/` dir variants |
| `adapter_select_test.go` | `ResolveAdapters` priority order: `--no-adapters` wins / `--adapters` wins / `cfg.Adapters` wins / detection fallback / `--reconfigure` bypass / interactive vs non-interactive paths |
| `adapter_select_test.go` | Slug validation: unknown slug rejected, `nanite-native` rejected from user input |
| `adapter_cleanup_test.go` | File doesn't exist / markers missing / content outside markers preserved / file becomes empty → deleted / read-write errors |
| `config_test.go` | Tri-state YAML parsing: nil vs `[]` vs `[claude]` distinguishable in `projectConfig` |
| Per-adapter `plugin_test.go` (4 files) | `SyncProjectRoot` with empty agents writes placeholder content, not nil |
| `prompt_test.go` (in cmd/nanite) | `promptAdapterSelection` with injected reader/writer: detected-found / detected-empty / `--reconfigure` / invalid-then-valid / empty-input semantics differ between fresh and reconfigure |

### Integration tests

| Test | Verifies |
|---|---|
| Fresh install, non-interactive, no CLI files | Writes `adapters: []` to config, creates no CLI files |
| Fresh install, non-interactive, pre-existing `CLAUDE.md` | Detects, persists `[claude]`, updates managed section in `CLAUDE.md` |
| Fresh install, non-interactive, pre-existing `CLAUDE.md` + `AGENTS.md` | Detects both, persists `[claude, codex]`, updates both files |
| `--adapters claude,gemini` flag | Only those two synced, others untouched (even if detected) |
| `--no-adapters` flag | Empty list persisted, no CLI files touched |
| `--reconfigure` shrinking list (config has `[claude, codex]`, user picks `[claude]`) | `AGENTS.md` cleaned up (or deleted), notice printed, config updated |
| `--reconfigure` growing list | New adapter's file created with placeholder or agent content |
| Migration scenario A (legacy `CLAUDE.md`, accept) | Adapter takes over file, config gets `[claude]` |
| Migration scenario B (legacy `CLAUDE.md`, decline) | File is cleaned up, config gets `[]`, notice printed |
| `--reconfigure` with empty input | Current list preserved, no cleanup, no sync changes |
| Fresh install with empty input on prompt | Empty list persisted |

### Tests to update (existing)

- `internal/service/install/integration_test.go` — fresh-install assertions: now expects `adapters:` key in config, expects no `CLAUDE.md` unless detected
- `internal/service/install/claudemd_test.go` — legacy direct path tests removed; `UpdateCLAUDEmd` is still tested at the helper level but not via install flow
- `internal/service/install/adapters_test.go` — extends with the filter parameter

---

## 12. Out of Scope

- Changing how adapters `Discover` or `PopulateSandbox` (only `SyncProjectRoot` is touched)
- Adding new CLI adapters (CrewAI, AutoGen, Continue, etc.)
- Per-adapter custom managed section content beyond per-adapter placeholder constants
- Changing `NANITE.md` ownership model (still human-owned, never managed)
- Removing the `UpdateCLAUDEmd` helper itself — it stays as a utility, just no longer called from `install.go`
- DB sync of the adapter list (the file is the source of truth, same as the rest of `config.yaml`)
- TUI library adoption — sticking with `fmt.Scanln` and `isStdinTTY()` for consistency with `handlePartialInteractive`
- Cross-project adapter defaults (e.g., a global `~/.nanite/config.yaml` adapter preference) — out of scope, project-local is sufficient

---

## 13. Open Questions

None remaining — all design questions resolved during the brainstorming session.

---

## 14. Files Touched (preview, refined in implementation plan)

### New files

- `internal/service/install/adapter_detect.go` + `_test.go`
- `internal/service/install/adapter_select.go` + `_test.go`
- `internal/service/install/adapter_cleanup.go` + `_test.go`

### Modified files

- `internal/service/install/install.go` — remove legacy `UpdateCLAUDEmd` call from `freshScaffold`, wire `ResolveAdapters` + `cleanupRemovedAdapters` into `freshScaffold`/`adopt`/`migrate` paths, extend `InstallProjectReport`
- `internal/service/install/adapters.go` — extend `projectConfig` with tri-state `Adapters *[]string`, change `syncAdaptersForProject` signature to take `allowedAdapters []string`
- `internal/agent/adapter.go` — add `SyncAllProjectRootsFiltered` method to `AdapterRegistry` (defined at `adapter.go:77`)
- `internal/plugin/builtin/adapter-claude/plugin.go` — remove short-circuit, add placeholder content
- `internal/plugin/builtin/adapter-codex/plugin.go` — same
- `internal/plugin/builtin/adapter-gemini/plugin.go` — same
- `internal/plugin/builtin/adapter-opencode/plugin.go` — same
- `cmd/nanite/install_cmd.go` — add `--adapters`/`--no-adapters`/`--reconfigure` flags, add `promptAdapterSelection`, wire flag values into install service
- `internal/assets/framework/templates/nanite-config.yaml.tmpl` — leave seed as no `adapters:` key
- `internal/assets/framework/templates/NANITE.md.tmpl` — add "First-time setup" section

### Existing tests to update

- `internal/service/install/integration_test.go`
- `internal/service/install/claudemd_test.go`
- `internal/service/install/adapters_test.go`
- Per-adapter `plugin_test.go` files

---

## Cross-references

- `2026-04-08-agent-adapter-architecture-design.md` — defines `CLIAgentAdapter`, `AdapterRegistry`, managed section protocol, `WriteManagedSection` helper
- `2026-04-09-nanite-agentrc-consolidation-design.md` — sibling spec; the legacy direct CLAUDE.md write was introduced during that work and is now retired
- `internal/service/install/install.go:145-172` — `freshScaffold` (the integration point being changed)
- `internal/service/install/adapters.go:136-147` — `syncAdaptersForProject` (the signature being changed)
- `internal/plugin/builtin/adapter-claude/plugin.go:161-178` — example `SyncProjectRoot` (the short-circuit being removed)
- `cmd/nanite/install_cmd.go:162-198` — `handlePartialInteractive` (the prompt pattern being reused)
