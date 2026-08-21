---
title: Why Nanite
description: Why we built a control pane and runtime for agent systems instead of another agent loop.
---

# Why Nanite

Nanite came from a practical problem: powerful agents were getting harder to operate from raw terminals, one-off chat sessions, and file-based agent experiments.

The early need was simple. CLI-based agents were useful, but the terminal was a poor interface for rich feedback, approvals, state, and parallel work. When an agent needs input, a structured card is better than a wall of scrollback. When several agents are running, one terminal per agent stops scaling quickly.

This is the kind of tool Chrispian tends to build: something that scratches a real itch, teaches something difficult, and becomes useful enough to either release or turn into writing, video, notes, and lessons for the next pass.

That led to the first version of the idea: a GUI that could hold multiple agent sessions side by side.

Then the shape changed again. Instead of a human manually switching between every agent, one session could act as a relay and coordinate the others. That pattern became more important than the interface itself: Nanite was becoming a control pane.

Some of the ideas were prototyped in earlier forms: a Laravel-based app, CLI-based file agents, and the surrounding Hollis Labs tools. Those experiments helped build the current Nanite, and the current Nanite is now used to build Nanite itself.

## Not Just Another Harness

An agent harness usually answers one question: how do we run this model/tool loop?

Nanite answers a broader question: once you have agents, where do they live?

That means Nanite has to care about things a simple loop can avoid:

- session lifecycle
- agent identity
- tool and skill visibility
- provider and model configuration
- plugin registration and unloading
- structured UI responses
- approvals and user feedback
- background wakeups
- inter-agent coordination
- external systems calling into hosted agents

Those concerns are not incidental. They are what make an agent system operable.

On the surface, Nanite can look like a chat app plus a harness. That is the least interesting reading of it. The chat surface is a way into the system; the platform is the configuration, runtime, control plane, plugin model, and composition layer underneath it.

## Why a Platform

Nanite is a platform because real agent work rarely belongs to one framework boundary.

One project might use a CLI coding agent. Another might need an API-backed assistant. Another might need a durable reviewer that wakes on demand. Another might need a plugin that adds custom tools, UI, and hooks. Another might need an external app to wake an agent through a standard interface.

Nanite is the place those pieces can be composed without forcing every app, agent, and workflow to use the same implementation style.

The platform stance is also what keeps Nanite honest. The core should not grow a bespoke integration every time a new use case appears. The core should provide the runtime, control pane, plugin host, card system, and external boundary. Specific behavior should live in plugins and consumers.

That bias comes from watching AI infrastructure change quickly. Some early problems around orchestration, memory, continuity, and agent control were not well served by existing tools when the work started. Some of those gaps are now being filled by the broader ecosystem. Nanite is designed so useful external tools can be adopted instead of rebuilt, as long as they fit through open, extensible boundaries.

## Why Local First

Nanite is local-first today. That is intentional.

The immediate goal is to give builders a serious operating surface for their own agents, repositories, provider keys, and workflows. Multi-user, hosted, and larger product shapes are not ruled out; they are just not the current center of gravity.

Local-first also keeps the design pressure clear. Nanite should be easy to run, inspect, modify, and extend. It should not require a fleet of services to prove its value.

## Why Both CLI and API Runtimes

API-backed agents are flexible, direct, and increasingly capable. CLI-backed agents are also valuable because many of the best developer agents already ship as CLI tools with their own session handling, permissions, model support, and project behavior.

Nanite supports both because builders should not have to choose a single runtime style up front.

The important part is that Nanite treats runtime choice as a configuration boundary, not a product boundary. GUI, CLI, API, and MCP are different doors into the same platform.

Nanite has been provider- and framework-agnostic from the beginning. That does not mean every provider and every framework is supported today. It means Nanite's design does not require one blessed provider, one blessed framework, or one blessed way to build an agent. The platform tracks emerging standards and supports the useful pieces as they become real.

That is also why abstraction and adapter seams show up throughout the architecture. Memory, skills, agent frameworks, provider integrations, CLI runtimes, and workflow paths are all areas where the right answer may change. Nanite should give builders room to change their minds.

## Why Workflows

Open-ended chat is powerful, but prompting alone is not always the right control mechanism.

Some tasks need predictable sequencing: fetch this input, run this step, check this condition, then dispatch the next agent. Some tasks need an auditable path rather than a free-form conversation. That is why Nanite includes workflow direction alongside chat and durable agents.

Workflows are not meant to replace open-ended agents. They make rigidity available when a task needs it.

## Built by Dogfooding

Nanite is developed through real use. A common loop is:

1. Build or fix a piece of Nanite.
2. Use a live Nanite agent to do real work.
3. Watch what breaks.
4. Turn the failure into the next task.

That has shaped the project more than abstract architecture.

Torque already tracks and dispatches real Nanite work. Nanite tasks, plans, sprints, and epics live there, including work on the Conductor Console, the Loom wiki pilot, scheduler work, audit agents, session search, skills, procedures, and memory/knowledge integration. Those are not hypothetical examples; they are the working material of the project.

The surrounding Hollis Labs apps stay independent. Tether manages agent sessions and runtime fabric. Torque manages task orchestration. Tesseract provides memory and knowledge. Nanite gives builders the control pane and platform layer. Each can run on its own; together they are more interesting.

## Why Release It

Nanite is being built open source first because that is the most natural path for this kind of project.

The goal is not to rush a polished product page into the world. The goal is to make the work visible enough that other builders can follow the journey, try the tool, pressure-test the architecture, and decide whether parts of it fit their own agent systems.

That is also part of the larger Hollis Labs pattern. Some experiments turn into released tools. Some turn into articles, videos, examples, or hard-won lessons. Either way, the work has value because it builds deeper understanding of AI systems and moves the larger agentic software-factory idea forward.

There is personal history behind that approach. Early web projects like Lit.Org, a pre-social-media community for writers, were not major businesses, but they created learning, community, credibility, and opportunity. Nanite comes from the same builder instinct: make the tool needed for the work, share what survives contact with reality, and keep the system open enough for others to learn from or extend.

Nanite exists because agents need an operating surface, not just another loop.
