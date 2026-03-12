---
title: "Rename .volon/ to .agentrc/ (system-agnostic agent config)"
status: accepted
date: 2026-03-06
decision_makers: [chrispian, mentat]
origin: mentat-cli ADR-003
---

# ADR-005: Rename .volon/ to .agentrc/

## Context

The `.volon/` directory was named after the Volon GUI application, causing confusion between the Volon app and the local agent config convention. The convention should support multiple agent runtimes without implying dependency on the Volon product.

## Decision

Rename `.volon/` to `.agentrc/` ("agent runtime configuration") across all repos.

- `.volon/` → `.agentrc/`
- `volon.yaml` → `agentrc.yaml`
- Volon GUI application keeps its name

## Status

**Complete.** All repos migrated. Fallback support removed.
