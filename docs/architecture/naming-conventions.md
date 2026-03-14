# Naming Conventions — Fragments Engine

> Established 2026-03-14. Enforced across all projects.

## The Problem

Generic CS terms (`Store`, `Service`, `Manager`, `Task`, `Provider`, `Broker`, `Engine`, `Runner`) mean different things in different projects. This confuses both humans and AI agents reading the code.

## Rules

### 1. Domain-Qualify All Types

Never use bare generic names. Always prefix with the domain:

| Bad | Good | Why |
|-----|------|-----|
| `Task` (Hadron) | `Step` | Collides with Volon's `Task` (work item) |
| `Service` (Cerberus) | `ManagedService` | Collides with Volon's domain `Service` |
| `Broker` (Cortex) | `PlannerConfig` | Collides with universal `ContextBroker` |
| `Provider` (store) | `ProviderConfig` | Collides with `Provider` interface |
| `Transport` (MCP) | `MCPTransport` | Too generic |

### 2. Service vs Client Convention

| Name | Meaning | Where It Lives |
|------|---------|---------------|
| `Broker` / `Engine` / `Store` | The core service (source of truth) | `core/` packages |
| `Client` | Consumer/wrapper of a core service | App's `internal/` |

Example:
- `core/context.Broker` = the universal ContextBroker service
- `internal/chat/context_client.go` = Mentat's client that consumes it

**Rule:** A struct in an app's `internal/` must NEVER share a name with a `core/` service.

### 3. Grep-Friendly

- `grep -r "Broker"` → finds real services
- `grep -r "Client"` → finds consumers
- `grep -r "Step"` → finds Hadron blueprint steps (not Volon tasks)
- `grep -r "ManagedService"` → finds Cerberus processes (not Volon services)

### 4. Package Names Provide Context

Go convention: the package name qualifies the type. So `service.ManagedService` is fine (reads as "a managed service from the service package"), but `service.Service` is redundant and collides.

### 5. No Prefixes on Shared Packages

Shared libraries use plain names. No "tiamat-" or "fe-" prefixes:
- `core/otel` not `fe-otel`
- `core/mcp` not `tiamat-mcp-helpers`
- `core/broker` not `tiamat-tool-broker`

## Current Renames (2026-03-14)

| Project | Old | New | Status |
|---------|-----|-----|--------|
| Hadron | `Task` | `Step` | Done |
| Hadron | `Blueprint.Blueprint` | `Blueprint.Spec` | Done |
| Mentat | `ContextBroker` | `ContextClient` | Done |
| Mentat | `ToolBroker` | `ToolClient` | Done |
| Mentat | `Provider` (store) | `ProviderConfig` | Done |
| Mentat | `Transport` | `MCPTransport` | Done |
| Cerberus | `Service` | `ManagedService` | Done |
| Cerberus | `Manager` | `ServiceManager` | Done |
| Cortex | `BrokerConfig` | `PlannerConfig` | Done |
| Cortex | `BrokerPlanRequest` | `ContextPlanRequest` | Done |
| Volon | `chat.Service` | `CompletionService` | Done |
| Volon | `ProviderAdapter` | `ExecutionAdapter` | Done |

## Prior Art

Laravel uses similar conventions — Facades (public API) vs implementations (internal), with clear naming boundaries. The principle is the same: when an AI or human reads the codebase, the name alone should tell them what it is and where it belongs.
