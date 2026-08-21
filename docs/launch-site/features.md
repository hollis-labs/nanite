---
title: Features
description: The core capabilities Nanite provides for building and operating agent systems.
---

# Features

Nanite combines a local agent interface, runtime, plugin host, and control plane into one builder-focused platform.

The important part is not any single feature. It is the composition model: providers, models, CLI agents, tools, skills, plugins, workflows, and agent frameworks should be pieces you can wire together, replace, and evolve as the ecosystem changes.

## Unified Agent Control Pane

Nanite gives you one place to start, inspect, resume, and coordinate agent sessions.

Use it for direct chat, coding agents, project advisors, one-shot reviewers, planners, background agents, and agents hosted for other systems.

## CLI and API Runtime Support

Nanite supports more than one way to run an agent.

CLI-managed agents let Nanite host tools like Claude, Codex, OpenCode, or similar command-line agents as managed subprocess runtimes. API-backed agents use provider SDKs and Nanite's own harness path.

Both paths matter. CLI agents are often the most capable way to work inside a project today. API-backed agents give Nanite direct control over context, tools, streaming, and durable execution. Nanite is designed to support both without turning them into separate products.

Nanite's CLI path is not just a chat window wrapped around a terminal. Nanite plants and manages CLI agents with explicit control over the launch environment, permissions, sandboxing, sessions, recovery, steering, and context. The goal is to make CLI agents operable from a control pane while preserving what makes them useful.

## Provider, Runtime, and Framework Agnostic

Nanite is not tied to one LLM provider, one agent runtime, or one agent framework.

Providers and models are configuration, not hardcoded product identity. Runtime style is configuration. Framework choice is configuration. The goal is to let builders choose the pieces that fit the task, then change them as the ecosystem changes.

That does not mean Nanite needs to support every provider or framework on day one. It means the architecture is built around adapters, contracts, and emerging standards instead of one blessed implementation path.

## Composable by Default

Nanite favors standards, protocols, and clear boundaries over all-or-nothing adoption.

Fast-moving pieces are kept behind replaceable seams where possible:

- model providers
- CLI agent runtimes
- agent frameworks
- tools and MCP servers
- skills and procedure sources
- memory and knowledge systems
- plugins and app-specific integrations

This is the main reason Nanite can keep growing without making every user buy into the whole Hollis Labs stack or one fixed agent architecture.

## Agent Recipes

Nanite includes first-party recipes for common durable-agent shapes:

- project advisor
- architect advisor
- project manager
- orchestrator
- planner
- reviewer
- task writer
- system monitor
- managed CLI harness
- web chat agent
- external company agent

Recipes are setup paths. They compile into normal Nanite durable-agent configuration rather than introducing a second runtime stack.

## Durable Agents

Durable agents have persistent identity and lifecycle behavior.

They can be reused across sessions, woken by explicit triggers, launched as fresh one-shot workers, or managed as long-running harness sessions depending on the recipe and runtime configuration.

This is the difference between "ask a model something once" and "operate an agent that belongs to a project or system."

## Plugins

Plugins extend Nanite without changing core code.

A plugin can add tools, slash commands, cards, UI components, HTTP routes, hooks, CRUD resources, keybindings, and agent profiles. Subprocess plugins run out of process and communicate with Nanite over JSON-RPC, which keeps feature-specific behavior separate from the host.

Nanite's plugin system is central to the platform: core provides the runtime and extension points; plugins provide specialized behavior. Plugins are how Nanite stays extensible without turning into a pile of hardcoded integrations.

## Structured Cards

Agents should not have to render every useful result as prose.

Nanite cards provide structured UI for approvals, tables, diffs, reports, progress, timelines, confirmation prompts, and other rich outputs. Cards let the harness show actionable data without bloating the agent's conversational context.

## MCP Integration

Nanite uses MCP as a standard way to connect tools and expose agent control surfaces.

As a client, Nanite can make MCP tools available to hosted agents. As the platform evolves, MCP is also the intended primary boundary for external systems that need to wake agents, send messages, check status, or otherwise interact with Nanite-hosted agents.

## Scheduling and Background Work

Nanite is moving toward stronger built-in scheduling for durable agents, workflows, and commands.

The direction is explicit: scheduled work should be DB-backed, observable, retry-aware, and part of the same runtime rather than a side system.

## Workflows

Nanite supports the idea that some agent tasks should be deterministic.

Open-ended chat is useful for exploration and flexible work. Workflows are for tasks that need predictable sequencing, clear step boundaries, and auditable execution.

The goal is not to replace agent frameworks. The goal is to make Nanite a place where framework-shaped workflows, free-form agents, and CLI-managed agents can operate together.
