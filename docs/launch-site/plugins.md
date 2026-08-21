---
title: Plugins
description: How Nanite's plugin system turns the runtime into a platform builders can extend.
---

# Plugins

Plugins are how Nanite becomes a platform instead of a fixed app.

The core runtime provides sessions, agents, messages, providers, tools, cards, streaming, settings, and plugin lifecycle. Plugins add specialized behavior without requiring a core code change.

That boundary matters because Nanite is intentionally not an all-or-nothing system. Builders should be able to keep the runtime small, add the behavior their work needs, and replace pieces as better standards, tools, or agent frameworks emerge.

## What Plugins Can Add

A Nanite plugin can contribute:

- slash commands
- MCP tools
- HTTP routes
- CRUD resources
- UI components
- structured cards
- keybindings
- event hooks
- filters
- agent profiles
- configuration fields

That makes plugins useful for both small features and full app-specific integrations.

A plugin can be a tiny helper for one workflow or the bridge between Nanite and a whole local system.

## Subprocess by Default

Nanite supports subprocess plugins as the default extension model.

A subprocess plugin is built as its own binary and optional UI bundle. Nanite starts it as a child process and communicates over JSON-RPC on standard input/output.

This keeps plugin code out of Nanite internals. A plugin can be installed, updated, disabled, unloaded, or removed without rebuilding the host.

Builtin plugins exist for core functionality, but user and app-specific behavior should live in subprocess plugins.

## Manifest-Driven Registration

Plugin registration is declared in `plugin.yaml`.

The manifest is the source of truth for what the plugin contributes. The plugin binary serves runtime handlers, but it does not imperatively register tools or UI directly into the host.

This keeps plugin installation inspectable and predictable. Before a plugin runs, Nanite can see what it intends to add.

## Tools and Commands

Plugins can expose tools to hosted agents and commands to users.

For example, a plugin might add a tool for searching an internal system, a command for creating a task, or a helper that turns a URL into a structured card.

Because tools are registered through Nanite, they can participate in the same visibility, permission, and runtime surfaces as core tools.

## Cards and UI

Plugins can render structured UI inside Nanite.

A plugin can define a card schema, provide a React component, and return card data from a command, tool, hook, or route. This is how plugin-specific behavior can feel native without forcing the core app to understand that feature.

Cards are especially important for builder workflows: tables, approvals, diffs, reports, and forms should be UI, not prose.

## Hooks and Filters

Plugins can participate in the turn loop and runtime lifecycle through hooks and filters.

That includes events around sessions, messages, artifacts, bookmarks, configuration changes, tool results, assistant responses, and card data. This makes plugins useful for cross-cutting behavior such as logging, enrichment, routing, previews, integrations, or policy checks.

## Agent Profiles

Plugins can contribute agent profiles.

That matters for product-specific or workflow-specific agents. Instead of baking every possible agent into Nanite core, a plugin can ship the agent shape that belongs to a particular integration.

For example, an internal app could provide its own curator, reviewer, monitor, or support agent profile through a plugin.

## Plugin Lifecycle

Nanite is designed to manage plugins as live runtime participants.

The platform supports plugin install, reload, enable, disable, and uninstall flows. The long-term goal is for plugin changes to take effect without restarting Nanite and without leaving stale tools, cards, routes, or agent profiles behind.

## Why This Matters

Agent platforms become brittle when every new feature turns into core code.

Nanite's plugin system is the boundary that keeps the host lean. Core owns the generic runtime. Plugins own feature-specific logic.

That is what lets Nanite remain provider agnostic, framework agnostic, and skill-provider agnostic while still being useful for real, specific workflows.

Plugins are also the pressure-release valve for experiments. If a feature proves useful, it can become a stable plugin. If it turns out to be a learning artifact, it does not have to become permanent Nanite core.
