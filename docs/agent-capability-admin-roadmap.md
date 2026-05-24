# Agent Capability Admin Roadmap

**Status:** Core capability admin shipped in Nanite  
**Scope:** Reflexes, skills, prompts, tools, permissions, known rosters,
procedures, knowledge seed, and boot-plan preview/configuration for agents.

## Product Goal

Nanite should let an operator manage what an agent knows, what it can use,
what it should remember at boot, and what reflexes or procedures shape its
behavior, without forcing raw JSON editing for normal paths.

## Current Surface

Agent capability admin now covers:

- skill assignment
- prompt-template assignment
- tool permission editing
- known tools and known skills
- procedures
- knowledge seeds
- boot plans with dry-run preview and redaction
- reflexes when backend routes exist

## Mutation Policy

Internal or file source-of-truth agents remain read-only where the backend
rejects mutation. The UI should display that state explicitly rather than
pretending edits will work.

## Boot Plans

Boot plans are versioned per-agent documents covering:

- plant items
- callbacks
- dry-run preview
- secret redaction

Current boundary:

- storage and dry-run are supported
- callbacks are configured and previewed only
- callback execution is not part of the current runtime path

## Reflexes

Reflexes are exposed when the backend routes exist. If a deployment lacks those
routes, the frontend should keep the surface clearly disabled or read-only.

Current constraint:

- some reflex payload fields are still opaque JSON strings, so the UI remains
  compact and validation-oriented rather than fully schema-driven

## Agent Builder Relationship

The Agent Builder can preview many of the same capabilities, but it remains
advisory until final submit:

- draft and review are no-write
- dry-run is no-write
- final submit performs the real mutations
- ready notice remains preview-only

## Preserved Limits

- no arbitrary callback execution
- no hidden mailbox ready-notice send
- no broad power-user raw JSON editor as the primary path
