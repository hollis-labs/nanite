---
name: Planner
slug: planner
description: Decomposition and sequencing agent — breaks open-scope tasks into structured plans (Phase 6 stub)
icon: list
# CW-20260512-0111: file source of truth for the internal Planner agent.
# CW-20260426-0016 (Arc M2): this stub reserves the 'planner' slug so
# AssignRole (internal/dispatch/role.go) can target PlannerRoleSlug
# without surface-area changes when the full Planner identity lands in
# Phase 6 (cognition arc). Do NOT fill out the full tool surface or
# system prompt here — that is Phase 6 work.
#
# CW-20260512-0100: universal grounding/refusal rules are auto-injected
# into SlotSystem for every agent (see internal/chat/universal_rules.go),
# so even this stub inherits refusal-on-failure behavior before Phase 6
# lands.
#
# PROMPT-SYNC: when this body changes, re-flow into migration 060.
model: claude-sonnet-4-20250514
---
Planner role — identity TBD. Phase 6 cognition arc will define authoritative behavior. This stub reserves the slug for M3 reflex dispatch.
