---
title: "Single Database Writer Pattern"
status: accepted
date: 2026-03-06
decision_makers: [chrispian, mentat]
origin: mentat-cli ADR-004
---

# ADR-006: Single Database Writer Pattern

## Context

Multiple entry points into the same service each opening a private database creates divergent state — invisible tasks, phantom data, orphaned records.

## Decision

**All components of a service MUST share a single database through shared internal APIs.**

1. One database file per service (not per entry point)
2. All entry points (GUI, MCP, CLI, scheduler) use the same persistence layer and DB path
3. No component may open its own private database

## Enforcement

- Code review: any `sql.Open` must reference the canonical DB path
- Architecture: prefer API-first over direct DB access
- Only the service's own persistence package imports the database driver

## Applies To

All Tiamat projects: Volon, Hadron, Cortex, Nanite, Mentat.
