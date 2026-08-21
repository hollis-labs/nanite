---
title: Use Cases
description: Practical ways builders can use Nanite to run and extend agent systems.
---

# Use Cases

Nanite is built for people who want to operate agents as part of real work, not just experiment with isolated prompts.

The launch audience is mostly builders already working in agentic systems: people trying to run agents against repositories, tools, projects, workflows, and internal systems without committing everything to one framework or one provider.

## Manage Multiple Coding Agents

Run CLI-backed coding agents from one local control pane instead of juggling terminal tabs.

Each agent can keep its own session, context, provider behavior, and runtime state while Nanite gives you a shared place to inspect and coordinate the work.

This is useful when you want one agent implementing a change, another reviewing it, and a third researching or planning the next step.

## Build Nanite With Nanite

The flagship use case is the one already happening: Nanite is being used to build Nanite.

Earlier versions of the idea were prototyped in a Laravel app and in CLI-based file-agent workflows. Those tools helped build the current system. Now Nanite, Torque, Tether, Tesseract, and the shared agent libraries are part of the working loop for planning, implementation, review, memory, and orchestration.

That dogfooding matters because it keeps the project honest. The roadmap is shaped by failures in real agent work, not by imagined demo flows.

## Coordinate Work Through a Relay Session

Use one conversation as the front door while it delegates to other agents.

The relay session can understand the user's intent, dispatch work to a planner, reviewer, researcher, or worker, then bring the result back into one place. This is the control-pane pattern Nanite was built around.

## Create a Durable Project Advisor

Create an agent that belongs to a project rather than a single chat.

A project advisor can hold project-specific posture, context, and recurring responsibilities. Instead of starting from scratch every time, you can return to the same durable identity.

## Run Reviewers and Planners as Reusable Agents

Some agents are useful as repeatable task shapes.

A reviewer can check work against acceptance criteria. A planner can turn a goal or design note into ordered work. A task writer can produce implementation briefs. Nanite recipes make these shapes easier to create and reuse.

## Host Agents for Other Apps

Nanite can host agents that belong to another system.

For example, an internal app can wake a Nanite-hosted agent to curate content, compile a page, check a queue, or perform a structured background task. The consuming app owns its agent's purpose; Nanite provides the runtime and control surface.

Internal examples like Loom and Torque are useful proof points: Nanite can run agents that help other systems without making those systems core dependencies.

## Compose With Other Local Tools

Nanite does not require the rest of the Hollis Labs stack, and the sibling apps do not require Nanite.

The interesting part is that they can compose when useful. Tether can provide session/runtime fabric for CLI agents. Torque can track and dispatch durable task work. Tesseract can provide memory and knowledge. Nanite can act as the builder-facing control pane and agent platform on top of those capabilities.

The same pattern applies outside Hollis Labs: bring the tools, MCP servers, agent frameworks, and local services that already fit your work.

## Build a Plugin for Your Own Workflow

Use a plugin when Nanite needs to understand something specific to your environment.

A plugin can add a slash command, expose an MCP tool, render a custom card, register an event hook, provide an HTTP route, or contribute an agent profile. That makes Nanite a practical host for app-specific behavior without turning the core into a pile of bespoke integrations.

## Add Structured Interaction to Agent Work

Use cards when text is the wrong interface.

Approval prompts, diff previews, progress states, tables, forms, reports, and timeline views should be structured and actionable. Nanite's card system lets agents and plugins surface those interactions directly in the UI.

## Schedule Recurring Agent Work

Use background agents for recurring checks, audits, monitors, and wake-on-demand tasks.

Nanite's scheduling direction is aimed at making these runs reliable and observable: due work should be tracked, dispatch should be safe, failures should be visible, and retries should be explicit.

## Experiment With Agent System Design

Nanite is also a lab for builders exploring agent architecture.

Because Nanite keeps providers, runtimes, tools, skills, plugins, and agent construction separate, it is a good place to test different approaches without committing the whole system to one framework.

## Build Toward a Personal Software Factory

Nanite is useful when the goal is not just to chat with models, but to create a reliable agentic system you can keep using.

That might mean a local team of coding agents, a set of repeatable reviewer/planner workflows, background monitors for projects you care about, or agents that help you build the next app. The common thread is ownership: Nanite is for builders who want the system to fit their way of working instead of adopting a closed, all-or-nothing platform.
