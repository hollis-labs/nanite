---
title: Roadmap
description: Where Nanite is headed, what is real today, and what still needs hardening.
---

# Roadmap

Nanite is in active development and getting close to public release.

This roadmap is intentionally candid. The foundations are real and already useful, but the project still needs testing, review, and polish. The UI/UX is the biggest gap overall.

## Launch Readiness

The immediate goal is a public repo that builders can clone, run, understand, and explore.

Focus areas:

- clearer install and setup documentation
- provider onboarding that reaches working chat without dead ends
- version and build metadata in release artifacts
- a cleaner distribution path beyond development builds
- public-facing website content
- examples that show realistic agent and plugin usage
- a clearer active-development status without hiding rough edges

## Agent Control Pane

Nanite will continue strengthening the local control pane for interactive and background agents.

Planned direction:

- better session start surfaces
- clearer runtime-kind selection
- stronger progress and completion reporting
- more reliable subagent result surfacing
- better inspection of running, completed, and failed agent work
- UI/UX cleanup so the system feels as understandable as the architecture already is

## CLI and API Runtime Parity

Nanite supports both CLI-managed and API-backed agents, but the platform is still converging on cleaner runtime boundaries.

Planned direction:

- make runtime kind explicit and easy to choose
- improve managed CLI agent lifecycle
- keep planting, host policy, sandboxing, permissions, recovery, and steering as explicit user-controlled surfaces
- keep API-backed agents first-class
- avoid duplicating runtime rules across GUI, CLI, API, and MCP surfaces
- continue protocol work around agent host and ACP support

## Agent Construction

Nanite is moving toward a more composable agent model.

Planned direction:

- roles as reusable behavior templates
- agents as compositions of role, runtime, model, class, tools, skills, and scope
- catalog-backed tool and skill grants
- clearer assignment APIs
- plugin-provided agent profiles wired through the same model

The goal is to make agents reusable without turning every agent definition into a copied pile of settings.

## Plugin Platform

Plugins are central to the platform story, and the plugin system will keep getting harder edges.

Planned direction:

- installed/enabled state that works uniformly for builtin and subprocess plugins
- hot reload parity across GUI, API, and CLI paths
- plugin-provided agent profiles
- plugin-extensible HTTP middleware
- better plugin catalog, update, and verification flows
- clean unload behavior for tools, cards, routes, hooks, and profiles

## Cards and Structured UI

Nanite will keep investing in cards as the structured interaction layer for agents.

Planned direction:

- composable card primitives instead of one-off card types
- interactive tables with row-level actions
- plan review and todo list cards rebuilt on common primitives
- better context discipline so large card data does not bloat future turns
- cleaner card type discovery for CLI-backed agents

## Durable and Background Agents

Durable agents are a core pillar, not a side experiment.

Planned direction:

- better wake/resume semantics
- clearer lifecycle classes
- improved durable-agent recipes
- explicit ownership for agents hosted on behalf of other systems
- stronger observability for unattended work
- safer failure handling

## Scheduling

Nanite is adopting a DB-backed scheduling engine for durable-agent wakeups, workflow runs, command runs, and reflex dispatch.

Planned direction:

- replace manual polling with a scheduler engine
- track schedule runs explicitly
- add retry, backoff, and failure policy
- emit telemetry for schedule fires
- add operator HTTP APIs
- allow agents to schedule follow-up work through a first-party self-tool

## Workflows

Nanite is adding workflow capability for tasks that need predictable sequencing, while integrating with sibling tools that already own adjacent orchestration concerns.

Planned direction:

- support deterministic steps where the graph, not the LLM, owns sequencing
- preserve open-ended chat for tasks that need flexibility
- make workflows useful for planner/reviewer/curator/monitor patterns
- evaluate external framework integration where it earns its complexity
- keep Torque as the task orchestration runtime when durable task queues, scheduler dispatch, run events, and execution records are the right abstraction

## Dogfooding With Torque

Nanite is already managed as real agent work.

Torque tracks Nanite projects, epics, sprints, plans, and task runs. Current and recent work includes the Conductor Console, Loom wiki pilot, Nanite-native scheduling, periodic audit agents, hybrid session search, skills/procedures, handoff semantics, and Tesseract/Vanta memory integration.

This matters because the roadmap is not a whiteboard. The platform is being built by the same agentic system it is meant to improve.

## External Control Surfaces

Nanite's external boundary is moving toward MCP-first control-plane access.

Planned direction:

- expose standard tools for waking agents, sending messages, checking status, and coordinating work
- avoid bespoke HTTP endpoints for each consuming app
- keep external-control tools separate from hosted-agent self-tools
- keep dependency direction clean: consumers depend on Nanite; Nanite does not depend on each consumer

## Current Boundaries

Nanite is open source first. Future product or service plans are left for the future.

Not in scope for the current public launch:

- pretending the UI/UX is finished
- claiming broad production hardening before it exists
- forcing every agent into one framework
- requiring the rest of the Hollis Labs stack
- smoothing over rough edges instead of naming them

The goal is simpler and more useful: make Nanite strong enough as a local builder platform that people can explore it, understand it, extend it, and decide whether it fits their own agent work.
