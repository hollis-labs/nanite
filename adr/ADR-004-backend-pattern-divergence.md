---
title: Backend Pattern Divergence Across Tiamat Projects
status: accepted
date: 2026-03-06
decision_makers: [chrispian, mentat]
origin: mentat-cli ADR-001
---

# ADR-004: Backend Pattern Divergence

## Context

Tiamat projects use different backend patterns:
- **Volon + Cortex**: gRPC + grpc-gateway (REST)
- **Hadron**: Echo v4 (embedded in Wails desktop app)
- **Nanite**: Wails IPC only (no HTTP server)
- **Carrier**: CLI-only (Python, no server)
- **Mentat**: Echo-style HTTP API (embedded SPA)

## Decision

**Accept the divergence. Do not standardize backend frameworks at this time.**

Each project has valid architectural reasons for its choice.

## Consequences

- Shared middleware cannot be trivially reused across projects.
- New projects should default to gRPC + Gateway unless they have a constraint.
- Revisit if/when Hadron adds a standalone server mode.
