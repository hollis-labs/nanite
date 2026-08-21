---
title: Getting Started
description: A first-run path for builders trying Nanite locally.
---

# Getting Started

Nanite is currently best approached as a local builder tool: clone it, run it, configure a provider, and start experimenting with agents and plugins.

Distribution and packaged install polish are part of the launch-readiness roadmap. For now, the development path is the clearest way to try the system.

## Prerequisites

You will need:

- Go
- Node.js, if you are working on the frontend or plugin UI
- At least one model provider key, such as Anthropic or OpenAI
- A local checkout of the Nanite repository

## Build and Run

From the repository root:

```bash
go build ./cmd/nanite/
./nanite serve -port 8090 -db ./nanite.db -dev
```

Then open the local Nanite UI in your browser.

## Configure a Provider

Nanite needs at least one configured provider before normal chat can work.

Use the provider settings UI to add an API key and choose your default provider/model. Nanite's provider system is designed so model choice is configuration, not hardcoded product identity.

If you are running a current development build, check the release notes or setup docs for any provider activation caveats. The project is actively improving the fresh-install path.

## Start a Session

Once a provider is configured, create a new session and send a message.

The simplest use is direct chat. From there, you can experiment with agent profiles, runtime kinds, plugins, and durable-agent recipes.

## Try a Durable Agent Recipe

Recipes are setup paths for reusable agent shapes.

Good starting points:

- `project-advisor`: reusable project or topic advisor
- `architect-advisor`: system design and architecture partner
- `reviewer`: one-shot review pass
- `planner`: one-shot sequencing and task planning pass
- `managed-cli-harness`: managed CLI-backed agent session

Recipes compile into normal durable-agent configuration. They are not a separate runtime.

## Explore CLI Access

Nanite also provides CLI surfaces for interacting with the runtime.

Use the CLI when you want to script setup, inspect paths, manage plugins, or work with Nanite without staying in the browser.

## Install or Build a Plugin

Plugins can be managed through the GUI or CLI.

Common CLI operations:

```bash
nanite plugin list
nanite plugin install <plugin-id>
nanite plugin disable <plugin-id>
nanite plugin enable <plugin-id>
nanite plugin uninstall <plugin-id>
```

Plugin development starts from a manifest and a subprocess binary. A plugin can add tools, commands, cards, UI, hooks, routes, and agent profiles.

## What to Try First

1. Start a normal chat session.
2. Create a project advisor.
3. Run a reviewer or planner recipe.
4. Try a CLI-backed agent runtime.
5. Install or scaffold a plugin.
6. Inspect the roadmap to understand what is stable, what is rough, and what is coming next.

## Current Status

Nanite is in active development and close to public testing, but it is not being presented as finished. The core platform direction is already far enough along to explore. The roughest areas are testing, review, polish, first-run onboarding, install/distribution, and UI/UX.

That is part of why it is opening up now. Builder feedback is most useful while the public surface is still flexible and the architecture is still easy to discuss candidly.
