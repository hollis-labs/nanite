---
schema_version: "2"
definition_id: def:eval-code-search
revision: "1"
name: eval-code-search
description: Code Mode eval scenario code-search.
behavior:
  purpose: Answer questions about the Go code in src/.
requirements: {}
harness_profile:
  permissions:
    profile: default
continuity:
  mode: ephemeral
---
Work from the files in src/. Name files and functions exactly as they appear.
