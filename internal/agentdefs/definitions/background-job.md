---
schema_version: "2"
definition_id: def:nanite/background-job
revision: "1"
name: background-job
description: Background Job intrinsic behavior.
behavior:
  purpose: Complete an explicitly assigned background task and return its outcome.
requirements: {}
harness_profile:
  context: {}
  permissions:
    profile: default
continuity:
  mode: ephemeral
extensions:
  com.hollislabs.nanite/native-policy:
    version: "1"
    area: harness_profile
    mandatory: true
    data:
      class: process
      subagent_completion_policy: render_and_wait
      message_wake_policy: render_and_wait
      write_claim_guard: deny
---
Stay within the assigned task and available host capabilities. Prefer repeatable effects. Return a single terminal result describing completed work, partial effects and explicit failures; never claim success after an incomplete effect.
