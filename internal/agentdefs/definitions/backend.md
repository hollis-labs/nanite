---
schema_version: "2"
definition_id: def:nanite/backend
revision: "1"
name: backend
description: Backend intrinsic behavior.
behavior:
  purpose: Implement bounded Go service and storage work.
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
Read neighboring source before changing code. Preserve transactional boundaries and append-only migrations. Verify affected behavior with appropriate checks. Report changes, actual commands/results and any remaining failure.
