---
title: Nanite
description: An open agent platform and control pane for builders.
---

# Nanite

Nanite is an open agent platform and control pane for builders who want to run, coordinate, extend, and inspect their own agentic systems.

It gives you one place to work with models, agents, tools, plugins, sessions, and background workflows without locking you into one provider, one agent framework, or one skill ecosystem.

Out of the box, Nanite is a fast local interface for working with AI agents. As you extend it, Nanite becomes the runtime, config surface, and control plane for building agent systems around your own projects, tools, and workflows.

## Build and Operate Your Own Agent Systems

Most agent tooling starts with a loop: send a prompt, call tools, return text. That is useful, but it is not enough once agents become part of real work.

Real agent systems need a place to run. They need persistent sessions, tool visibility, approval flows, structured outputs, plugin lifecycle, background wakeups, and a way for other systems to call into them. Nanite is built for that operating layer.

Nanite is not trying to be the one agent framework. It is the surface where different agent approaches can coexist: bring your own LLM, CLI agents, providers, skills, MCP servers, and agentic frameworks.

## What Nanite Gives You

- A local control pane for interactive and background agents
- First-class support for CLI-managed and API-backed agent runtimes
- Provider- and framework-agnostic configuration
- Durable agents with persistent identity and reusable recipes
- A plugin system for tools, commands, UI, hooks, routes, cards, and agent profiles
- Structured cards for approvals, diffs, tables, reports, progress, and other rich outputs
- MCP integration for tools and external control-plane access
- A roadmap toward stronger workflows, scheduling, telemetry, and agent host protocols

## CLI Agents, Treated Differently

CLI agents are powerful because many of them already know how to manage projects, files, tools, permissions, and long-running sessions. Nanite does not treat them as disposable terminal transcripts hidden behind a thin chat wrapper.

Nanite plants and hosts CLI agents deliberately. The planted environment gives the user control over security, permissions, sandboxing, sessions, recovery, steering, and context. The hosted agent can survive compaction, carry a useful runtime identity, and be operated through Nanite instead of being reduced to a log stream.

That matters because the goal is not to replace every CLI or framework. The goal is to give builders a coherent place to operate them.

## Local First, Composable by Design

Nanite is local-first today. It is designed to run on your machine, against your projects, with your tools and providers.

The core stays focused: sessions, messages, agents, providers, models, tools, context, streaming, settings, cards, and plugin lifecycle. Feature-specific behavior belongs in plugins.

That separation is the point. Nanite should be useful as a minimal agent interface, but it should get more powerful when you connect it to your own systems.

Nanite is part of the Hollis Labs portfolio, a set of Chrispian's personal AI-system experiments. Each app stands alone. When the pieces compose, they unlock more: Tether can manage agent sessions, Torque can orchestrate task execution, Tesseract can provide memory and knowledge, and Nanite can give builders the agent-facing control pane.

The apps that pan out get released as permissive open source. The experiments that do not still produce useful findings, writing, and lessons.

## Start Here

- Read why Nanite exists.
- Explore the feature set.
- Try the getting started path.
- Build or install a plugin.
- Follow the roadmap to see what is real, what needs testing and polish, and where the platform is heading.
