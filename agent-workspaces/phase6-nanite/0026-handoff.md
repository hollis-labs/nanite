# CW-20260515-0026 Handoff — Nanite skills/context providers onto the shared go-agent-context manager

**Sprint:** SP-20260514-0008 Phase 6
**Branch:** `feat/cw-20260515-0034-phase6-nanite-adoption`
**Status:** complete — `go build ./...`, `go vet ./...` green; `go test ./internal/bootprofile/... ./internal/service/... ./internal/skill/... ./internal/runtime/agent/...` all pass. One unrelated pre-existing failure (`internal/chat` `TestEnvelopeRegistrySync`) — confirmed identical on base via `git stash`, see §6.

---

## 1. What this ticket did

CW-0024 ported the boot-profile compiler and left the four **deferred**
slot kinds (`cmd` / `http` / `role_summary` / `skill_index`) surfacing as
`bootprofile.Requirement` entries that `ResolveRequirements` rejected with
`ErrRequirementUnsupported`. CW-0026 wires those four kinds through the
shared `go-agent-context` resolvers so the Requirements actually resolve
via the shared context manager (`agentcontext.DefaultProvider.Assemble`).

The shared `agentcontext/resolvers` package **already shipped** all four
resolvers (`CmdResolver`, `HTTPTextResolver`, `HTTPJSONResolver`,
`RoleSummaryResolver`) plus the opt-in `SkillIndexResolver`. CW-0026 added
**no new code to the shared package** — Nanite simply consumes resolvers
that were already there. Acceptance criterion "shared package gains
reusable providers without importing Nanite service/store internals" is
satisfied trivially: nothing was added to the shared package, and all four
resolvers are app-neutral (no Nanite imports).

---

## 2. What moved to shared providers vs stayed in Nanite

### Now resolved via shared `go-agent-context` resolvers (mechanical IO)

| Nanite slot kind | Shared resolver | Notes |
|---|---|---|
| `cmd` | `resolvers.CmdResolver` (`SlotSourceKindCmd`) | `sh -c`, stdout capture, timeout |
| `http` | `resolvers.HTTPTextResolver` (`SlotSourceKindHTTPText`) | default |
| `http` + `response_format: json` | `resolvers.HTTPJSONResolver` (`SlotSourceKindHTTPJSON`) | JSON pretty-print path |
| `role_summary` | `resolvers.RoleSummaryResolver` (`SlotSourceKindRoleSummary`) | role markdown body + optional `section` |
| `skill_index` | `resolvers.SkillIndexResolver` (`SlotSourceKindSkillIndex`) | opt-in via `resolvers.WithSkillIndex`; layered skill discovery + deterministic index render |

This is the same compile-time-IO delegation pattern CW-0024 used for
`text`/`static` — only now extended to the launch-time deferred kinds.

### Kept Nanite-side (app business logic — NOT pushed to shared packages)

- **`internal/skill/`** (`discovery.go`, `loader.go`, `parser.go`,
  `convert.go`, `context.go`, `builtin/`) — Nanite's full skill
  discovery/loading/conversion stack. This is **chat-side** skill
  surfacing (skill broker, slash-command routing, plugin skills, builtin
  embeds) — NOT mechanical context assembly. The mechanical skill-index
  *for boot prompts* now rides the shared `skills` model via
  `SkillIndexResolver`; the chat-side `internal/skill` stack is untouched
  and stays Nanite-owned.
- **`internal/context/`** — chat compaction / window / handoff. CHAT
  logic, left in Nanite per the task framing.
- **`internal/bootprofile/` schema + compiler + registry** — `Profile`,
  `SlotSource`, `LaunchSpec`, `Compile`, `Registry`, dropdown encoding.
  Pinned by acceptance criteria, unchanged in shape (one additive field —
  see §3).
- **`{{var}}` substitution, the `### filename` static-dir concat,
  deferred-vs-compile-time split, `renderDefaultPrompt`** — Nanite
  presentation/business semantics, stay in `slots.go` / `compiler.go` /
  `requirements.go`.

---

## 3. Changed paths (Nanite repo only — NO shared-package changes)

| File | Change |
|---|---|
| `internal/bootprofile/agentcontext_adapter.go` | **Extended.** Added the deferred-Requirement resolution machinery: `requirementProvider()` (builds a `DefaultProvider` wired with cmd/http/role_summary/skill_index resolvers), `requirementToSlotSpec` (Nanite `Requirement` → shared `agentcontext.SlotSpec`, with the kind mapping from the CW-0024 handoff §6), `parseRequirementTimeout` (cmd-slot timeout string → `time.Duration`), and `assembleRequirements` (runs `Assemble`, returns resolved bodies keyed by slot name, promotes the first resolver failure to a hard error). |
| `internal/bootprofile/requirements.go` | **Rewritten.** `ResolveRequirements(spec)` is now a back-compat wrapper over the new `ResolveRequirementsContext(ctx, spec)`. The latter: guards unknown Requirement types (still `ErrRequirementUnsupported`), calls `assembleRequirements`, folds resolved bodies into `spec.Slots`, drains `spec.Requirements`, and re-renders `spec.BootPrompt` via the same `renderDefaultPrompt` `Compile` uses. |
| `internal/bootprofile/compiler.go` | `Requirement` gained a `Roots []string` field (for `skill_index` discovery roots). Struct is now non-comparable — see §5. |
| `internal/bootprofile/profile.go` | `SlotSource` gained a `Roots []string` YAML field so a catalog can declare explicit skill-discovery roots for a `skill_index` slot. |
| `internal/bootprofile/slots.go` | `requirementFromSlot` copies `src.Roots` onto the `skill_index` Requirement. |
| `internal/bootprofile/requirements_test.go` | **Rewritten.** Old stub tests (expected `ErrRequirementUnsupported` for cmd/http/role_summary/skill_index) replaced with: unknown-type-still-errors, cmd-resolves, cmd-failure-surfaces, role_summary-resolves, skill_index-resolves. |
| `internal/bootprofile/slots_test.go` | `*req != tc.expect` → `reflect.DeepEqual` (Requirement is no longer comparable); `reflect` import added. |
| `internal/service/chat_bootprofile_resolve_test.go` | `writeCatalogForResolveTest` fixture: `deferred` profile now pairs with a new workdir-less `deferred-launch` and uses a cwd-independent cmd (`printf`); added a `badcmd` profile. `TestResolveBootProfile_RequirementsStub` (pinned the old stub behavior) replaced with `TestResolveBootProfile_DeferredRequirementResolves` + `TestResolveBootProfile_DeferredRequirementFailureSurfaces`. Unused `errors` import dropped. |

**Shared package (`/Users/chrispian/dev/hollis-labs/libs/go-agent-context`): NO changes, NO commit.**

---

## 4. How Nanite skills map to the shared skill model

Two distinct skill surfaces — keep them straight:

1. **Boot-prompt `skill_index` slot** → shared `skills` model. The
   `skill_index` Requirement (slot name + optional `Roots` + `Limit`)
   maps onto `agentcontext.SkillIndexSource`; the shared
   `SkillIndexResolver` walks the roots via `skills.Discover`
   (layered, non-recursive by default, `*.md` glob) and renders a
   deterministic `<trigger> — <description>` index. The shared
   `skills.Skill` model (Name/Description/Triggers/Body/Frontmatter)
   is the on-disk contract.
2. **Chat-side `internal/skill/` stack** → unchanged Nanite code. The
   skill broker, slash-command routing, plugin/builtin skills, and the
   4-location priority discovery (`.nanite/skills`, `~/.nanite/skills`,
   `.claude/skills`, `plugins/*/skills`) stay Nanite-owned. They are
   chat-runtime concerns, explicitly out of scope.

**Friction noted:** Nanite's `skill.Discover` 4-location priority order
is NOT expressed by a single `skill_index` slot — a boot profile that
wants that exact layering must declare the roots explicitly in
`SlotSource.Roots` (in priority order; the shared resolver treats later
layers as overriding earlier by skill Name, which matches "first slug
wins" only if roots are listed highest-priority-LAST). For the common
single-root boot-prompt case this is a non-issue. If a future ticket
wants the boot prompt to mirror the full chat-side 4-location skill set,
it should build the `Roots` list from the same paths
`skill.DiscoverOptions` uses. Not done here — no current boot profile
uses a `skill_index` slot, and inventing the root list would be
app-policy guessing.

---

## 5. go.mod / replace directives / API notes

- **No `go.mod` change.** `go-agent-context v0.1.0` (added by CW-0024) is
  already a direct require and ships every resolver used. The
  `resolvers` and `skills` subpackages are part of that tagged release.
- **No `replace` directives added.** No shared-package gap — nothing had
  to be patched locally. **No new tagged release is required.**
- **`Requirement` is now non-comparable** (it has a `[]string` field).
  Any future test or code doing `req1 == req2` must switch to
  `reflect.DeepEqual`. `slots_test.go` was the only existing site.
- **`ResolveRequirements` signature unchanged** — `func(*LaunchSpec)
  error` — so the two existing call sites
  (`chat_bootprofile_resolve.go`, `chat_bootprofile_recovery.go`) did
  NOT need to change. A new `ResolveRequirementsContext(ctx, spec)` is
  available for callers that want to thread cancellation into the
  cmd/http resolvers.
- **Behavior change for callers:** `ResolveRequirements` no longer
  errors on cmd/http/role_summary/skill_index slots — it resolves them
  and mutates `spec` in place (`spec.Slots` filled, `spec.Requirements`
  drained, `spec.BootPrompt` re-rendered). A *resolver-level* failure
  (cmd non-zero exit, HTTP non-2xx, missing role file) still returns a
  pointed error; an *unknown* Requirement type still returns
  `ErrRequirementUnsupported`.

### Environment note (same as CW-0024/0025, not committed)

This worktree needs the `libs/go-modelsdev` symlink
(`/Users/chrispian/agent-mux/workspaces/nanite/libs/go-modelsdev →
/Users/chrispian/dev/hollis-labs/libs/go-modelsdev`) for `go build` to
resolve the repo's existing `replace` directive. Already present;
unrelated to this ticket.

---

## 6. Verification

```
go build ./...                          → ok
go vet ./...                            → ok
go test ./internal/bootprofile/...      → ok
go test ./internal/service/...          → ok
go test ./internal/skill/...            → ok
go test ./internal/runtime/agent/...    → ok
```

**Pre-existing unrelated failure:** `internal/chat` `TestEnvelopeRegistrySync`
fails reading a sibling `go-envelopes/manifest/envelopes.yaml` checkout
absent in this worktree layout. Confirmed identical failure on the base
commit (`git stash` + re-run) — NOT caused by this change. Same
worktree-layout class as the CW-0024 `go-modelsdev` symlink note and the
CW-0025 handoff §6.

**Acceptance — "Nanite chat still assembles equivalent context":** the
chat-resolve path (`resolveBootProfile` → `CompileFor` →
`ResolveRequirements`) is exercised by the rewritten
`internal/service/chat_bootprofile_resolve_test.go` and the unchanged
`chat_bootprofile_smoke_test.go` (example catalog, text+static only —
still drains a now-empty Requirement list and renders the same prompt).
A profile with only text/static slots produces a byte-identical
`BootPrompt` to pre-change (the deferred path is a no-op for it). A
profile WITH deferred slots now renders a *complete* prompt instead of
erroring.

---

## 7. Notes for later workstreams

### CW-0027 (standalone launcher)
- `ResolveRequirements` / `ResolveRequirementsContext` must run BEFORE
  `LaunchSpec.ToLaunchPlan` if the profile has deferred slots — an
  unresolved `LaunchSpec` has an empty `BootPrompt`, so
  `ToBootProfileInline` would carry an empty boot body. Drain
  Requirements first, then bridge to `agentlaunch.LaunchPlan`.
- Use `ResolveRequirementsContext` (not the background-context wrapper)
  so a slow cmd/http boot slot can be cancelled with the launcher's
  context.

### CW-0028 (smoke)
- For a profile using only `text`/`static` slots: `BootPrompt` is
  byte-identical to pre-CW-0026. Existing smoke assertions hold.
- A NEW smoke worth adding: a boot profile with a `cmd` slot (e.g.
  `git log -1`) and a `skill_index` slot — verify the resolved cmd
  output and the skill index land in the rendered prompt. The
  `deferred` / `badcmd` fixtures in
  `chat_bootprofile_resolve_test.go` show the shape.
- `skill_index` slots need a `roots:` list in the catalog YAML
  (`SlotSource.Roots`) or they discover nothing — there is no implicit
  default root.

### General
- `agentcontext_adapter.go` is now the single home for ALL Nanite↔
  go-agent-context bridging — both the compile-time `text`/`static`
  delegation (CW-0024) and the launch-time deferred-Requirement
  resolution (CW-0026). A future ticket consolidating boot-prompt
  assembly should start there.
- The `http` → `http_text`/`http_json` split keys on
  `Requirement.ResponseFormat == "json"`. `ResponseFormat` is otherwise
  unconsumed Nanite-side; if a richer HTTP-response policy is ever
  needed, that field is the seam.
