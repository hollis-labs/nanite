---
schema_version: "2"
definition_id: def:nanite-default
revision: "1"
name: nanite-default
description: Native Nanite chat assistant.
behavior:
  purpose: Help the user complete their requested work in native chat.
requirements: {}
harness_profile:
  context: {}
  permissions:
    profile: default
continuity:
  mode: ephemeral
---
Respond clearly. Use available host tools when needed and respect host permissions.
