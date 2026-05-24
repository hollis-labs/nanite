# Backend Agent Runtime Roadmap

**Date:** 2026-05-24  
**Scope:** Backend path from Nanite's current chat, boot-profile, and runtime
shape to a durable-agent platform built on the shared launch/session substrate.

## Current Status

As of 2026-05-24:

| Stage | Status | Notes |
|---|---|---|
| Runtime-kind stabilization | Done | `agent_runtime.runtime_kind` is persisted and managed automation defaults do not silently fall back to PTY. |
| Shared runtime contract adoption | Done | Nanite uses the shared `go-agent-runtime` substrate for runtime kind and related launch semantics. |
| Durable-agent backend substrate | Done | Durable-agent instances, attachments, launch policy, events, and wake controls are present. |
| Recipe catalog + dry-run/apply | Done | Built-in and configured recipes compile through existing durable-agent primitives. |
| Session details + start-surface contracts | Done | Backend exposes the data needed by the frontend start/admin surfaces. |
| External harness API | Done | `/api/harness/v1` ships as a versioned control-plane namespace over the same backend services. |
| Product recipe packs | Done | Stable substrate IDs and richer product IDs are both available. |

## Runtime Shape

Nanite's backend runtime model is explicit:

| Runtime kind | Keep? | Purpose |
|---|---:|---|
| `api` | Yes | Direct provider/API-backed agents and external client integrations |
| `streaming-stdio` | Yes | Headless CLI agents such as Claude streaming JSON over stdio |
| `jsonrpc-stdio` | Yes | Formal local protocols such as MCP-style agent servers |
| `subprocess` | Yes, limited | One-shot or simple CLI adapters |
| `pty` | Debug only | Native TUI compatibility only if intentionally used; not a product path |

## Durable-Agent Platform Shape

Nanite's product-facing model is:

- **Agent profile**: reusable persona, capability, and lifecycle-default source.
- **Durable agent instance**: configured durable agent with immutable profile, provider, model, runtime kind, lifecycle class, and work root captured at creation.
- **Session**: chat, managed CLI harness, durable wake/run session, or external harness conversation.

## Preserved Limits

These constraints are intentional and current:

- no raw PTY or TUI product surface
- no second runtime launch stack
- no background wake poller
- boot callbacks are stored and previewed, not executed
- ready notices remain preview-only until mailbox target resolution is explicit

## Recipes

Recipes are declarative setup inputs, not runtimes and not profiles. They
compile into:

- lifecycle class
- launch policy
- wake defaults
- runtime kind
- work root
- metadata
- planned non-secret injections

Configured recipe catalogs are loaded from app config:

```yaml
recipes:
  catalog_paths:
    - /path/to/recipe/catalog-or-directory
```

Configured IDs override built-ins. Duplicate IDs across configured sources fail
startup loudly.

## Harness API

`/api/harness/v1` is the external control-plane API. It is intentionally thin:

- initialize and capabilities
- session create, load, turn, cancel, and events
- approval responses
- durable list, get, start, resume, and wake

The event transport is SSE-only in v1.

## Remaining Work

The main remaining runtime work is product polish rather than backend
rearchitecture:

- tighter observability and operator docs
- selective execution seam for safe non-secret boot planting if later enabled
- future mailbox-ready notice delivery once target resolution is explicit
- any future external adapters as deliberate follow-on work, not as recipe side effects
