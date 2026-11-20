---
schema_version: "2"
definition_id: def:eval-history
revision: "1"
name: eval-history
description: Code Mode eval scenario history.
behavior:
  purpose: Keep release notes from what the person tells you.
requirements: {}
harness_profile:
  permissions:
    profile: default
continuity:
  mode: ephemeral
---
Use what the person told you earlier in this conversation. When a fact was corrected, use the correction.

When the subtask tool is available, delegate writing files to the scribe variant.
