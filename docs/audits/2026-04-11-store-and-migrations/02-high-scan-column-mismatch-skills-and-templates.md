# [High] SELECT/Scan column count mismatch in ListAgentSkills and ListPromptTemplatesForAgent

**Scope:** internal/store/skills.go, internal/store/prompt_templates.go
**Topic:** Error handling / Correctness
**Date:** 2026-04-11

## Problem

Two query methods SELECT fewer columns than the Scan call expects, causing a runtime panic when any rows are returned.

## Evidence

### ListAgentSkills (skills.go:L163-188)

The SELECT returns 11 columns:

```sql
SELECT sk.id, sk.name, sk.slug, sk.description, sk.category, sk.tool_bindings,
       sk.input_schema, sk.is_builtin, sk.settings, sk.created_at, sk.updated_at
```

But the Scan reads 12 values (includes `&sk.Icon` between `&sk.Settings` and `&sk.CreatedAt`):

```go
// skills.go:L180-182
rows.Scan(&sk.ID, &sk.Name, &sk.Slug, &sk.Description, &sk.Category,
    &sk.ToolBindings, &sk.InputSchema, &sk.IsBuiltin, &sk.Settings, &sk.Icon,
    &sk.CreatedAt, &sk.UpdatedAt)
```

The 10th scan target is `&sk.Icon`, but the 10th column in the SELECT is `sk.created_at` (a datetime string). This will either return an error on every call or silently misalign all fields after `Settings`.

### ListPromptTemplatesForAgent (prompt_templates.go:L146-170)

The SELECT returns 10 columns:

```sql
SELECT pt.id, pt.name, pt.slug, pt.scope, pt.template, pt.variables, pt.priority,
       pt.is_builtin, pt.created_at, pt.updated_at
```

But the Scan reads 11 values (includes `&pt.Icon` between `&pt.IsBuiltin` and `&pt.CreatedAt`):

```go
// prompt_templates.go:L163-164
rows.Scan(&pt.ID, &pt.Name, &pt.Slug, &pt.Scope, &pt.Template,
    &pt.Variables, &pt.Priority, &pt.IsBuiltin, &pt.Icon, &pt.CreatedAt, &pt.UpdatedAt)
```

## Impact

Any API call that lists skills or prompt templates for a specific agent will fail with a `sql: expected N destination arguments in Scan, not M` error. The functions that use these are:

- `ListAgentSkills` -- called from `internal/chat/context.go` during system prompt assembly (tool awareness template), and from `internal/api/skills.go` for the agent skills API endpoint.
- `ListPromptTemplatesForAgent` -- called from `ComposePromptForAgent` (same file), which is the system prompt composition pipeline.

Both are on the hot path for every chat turn where the agent has assigned skills or prompt templates. If no agent has assigned skills/templates yet, the bug is latent (no rows = no scan = no crash), which explains how it passed tests.

## Recommendation

Add `COALESCE(sk.icon,'')` / `COALESCE(pt.icon,'')` to the SELECT clause of each query:

**skills.go:L164-167:**
```sql
SELECT sk.id, sk.name, sk.slug, sk.description, sk.category, sk.tool_bindings,
       sk.input_schema, sk.is_builtin, sk.settings, COALESCE(sk.icon,''),
       sk.created_at, sk.updated_at
```

**prompt_templates.go:L147-149:**
```sql
SELECT pt.id, pt.name, pt.slug, pt.scope, pt.template, pt.variables, pt.priority,
       pt.is_builtin, COALESCE(pt.icon,''), pt.created_at, pt.updated_at
```

Add test cases for `ListAgentSkills` and `ListPromptTemplatesForAgent` that insert an agent, assign a skill/template, and verify the list returns correctly.

## References

- Compare with `ListSkills` (skills.go:L36-37) which correctly includes `COALESCE(icon,'')` in the SELECT.
- Compare with `ListPromptTemplates` (prompt_templates.go:L30-31) which correctly includes `COALESCE(icon,'')`.
