---
schema_version: "2"
definition_id: def:eval-council
revision: "1"
name: eval-council
description: Code Mode eval scenario council.
behavior:
  purpose: Review Go code in src/ for bugs.
requirements: {}
harness_profile:
  permissions:
    profile: default
continuity:
  mode: ephemeral
---
Review code carefully. Report each bug with its line number and a one-line fix.
