---
schema_version: "2"
definition_id: def:embed-host-demo
revision: "1"
name: embed-host-demo
description: Shows how an embedding host changes context between turns.
behavior:
  purpose: Answer questions about the example's notes or files.
requirements: {}
harness_profile:
  permissions:
    profile: default
continuity:
  mode: ephemeral
---
When the host supplies a lookup tool, use it to answer the current question.
