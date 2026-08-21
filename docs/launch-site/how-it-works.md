---
title: How Nanite Works
description: A public technical overview of Nanite's runtime, agents, plugins, cards, and external surfaces.
---

# How Nanite Works

Nanite is one runtime with several doors into it.

The GUI, CLI, API, and MCP surfaces are different ways to operate the same underlying system. They should not become separate execution paths with different rules.

Nanite is also deliberately compositional. It is not trying to replace every agent framework, CLI agent, memory system, or workflow engine. It provides the control pane, runtime, configuration model, and extension boundaries that let those pieces operate together.

## The Runtime

Nanite's runtime is responsible for sessions, messages, providers, models, tools, context, streaming, agents, cards, plugins, and persistence.

The current system supports two broad execution substrates:

- CLI-managed agents, where Nanite starts and manages an external agent process.
- API-backed agents, where Nanite drives model calls directly through provider integrations.

The choice is intentional. Some agents are best run through existing CLI tools. Some are better hosted directly through Nanite's API harness. Nanite's job is to make both operable.

For CLI-managed agents, Nanite does more than open a process and stream text back. The launch path can plant the agent into a controlled environment with explicit context, session identity, permissions, sandboxing, recovery behavior, and steering surfaces. That gives builders more control than a raw terminal session while still using the CLI agents that already work well.

## The Doors

Nanite is designed around multiple access surfaces:

- GUI: the human-facing control pane.
- CLI: command-line access to the same runtime concepts.
- API: programmatic access for first-party clients and local automation.
- MCP: the primary external boundary for Nanite-aware consumers and other agents.

This makes Nanite more than a web UI. The UI is one consumer of the runtime, not the runtime itself.

## Agents

Nanite agents are composed rather than defined as one flat object.

The core model separates:

- roles: reusable behavior and prompt templates
- agents: concrete compositions with runtime, model, class, and ownership
- scopes: projects or other references that give an agent context
- tools and skills: catalog-backed capabilities granted to agents
- invocation overrides: task-specific choices made at run time

The cascade is simple: role defaults, then agent configuration, then task-specific overrides.

That model lets builders reuse behavior without duplicating every setting across every agent.

## Runtime Kinds

Nanite treats runtime kind as a real configuration choice.

A CLI-backed agent can use the behavior of an existing command-line tool while still being visible inside Nanite's control pane. An API-backed agent can use Nanite's direct provider integrations and harness services.

This is especially important for builders because the agent ecosystem changes quickly. Nanite should not require every useful agent to be rewritten into Nanite's own internal loop.

## Adapter Boundaries

Nanite keeps fast-moving parts behind explicit boundaries where possible.

Providers, model catalogs, runtime kinds, tools, skills, memory systems, plugins, and external control surfaces should be replaceable without rewriting the whole platform. Some of those seams are mature today. Others are still being hardened. The design direction is consistent: use standards, specs, protocols, and adapter contracts where they exist, and avoid treating any single implementation as permanent.

That is why Nanite can be provider agnostic and framework agnostic without pretending every possible integration already exists.

## Plugins

Plugins are how Nanite grows without bloating core.

A plugin declares what it contributes in `plugin.yaml`, then serves runtime handlers from its own process. The manifest can register tools, commands, cards, UI components, hooks, routes, CRUD resources, keybindings, and agent profiles.

Nanite applies those registrations on the plugin's behalf and can load, unload, enable, disable, or reload plugin behavior.

The core product provides the host. Plugins provide specialized behavior.

## Cards

Cards are structured UI outputs that agents and plugins can use instead of plain text.

Examples include approval cards, table cards, diff cards, progress cards, report cards, confirmation cards, timelines, and document viewers.

The reason cards matter is context discipline. An agent should not have to paste a giant table into conversation history just so the user can inspect it. The runtime can render structured data as UI while keeping the agent's context focused.

## Durable Agents

Durable agents have identity beyond one message.

They can act as advisors, process monitors, one-shot templates, managed CLI harnesses, or other reusable shapes. Recipes make those setup paths easier to create while still compiling into normal Nanite runtime configuration.

Durability is about operational identity: this agent belongs to a project, consumer, or workflow, and Nanite knows how to start, resume, wake, or reuse it.

## Scheduling and Workflows

Nanite is growing scheduling and workflow capabilities for work that should not depend on a human typing the next message.

Scheduling handles recurring or one-shot dispatch. Workflows handle predictable multi-step execution. Both belong in the same platform because they are part of making agents operable, observable, and reliable.

## External Systems

Nanite is designed so other systems can build on it without becoming part of Nanite core.

The direction is MCP-first for external control-plane access. Instead of adding a new bespoke HTTP handler for every consumer, external systems should be able to discover and call standard Nanite tools for waking agents, sending messages, checking status, and coordinating work.

That keeps the dependency direction clean: other systems can depend on Nanite-hosted agents, but Nanite should not need to know those systems exist to run.

The Hollis Labs apps are examples of this pattern, not requirements. Tether, Torque, Tesseract, Hadron, Cerberus, and other tools can each stand alone. When composed, they can provide a richer local agentic system, but Nanite should still be understandable and useful by itself.
