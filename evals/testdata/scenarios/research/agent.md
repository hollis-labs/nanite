---
schema_version: "2"
definition_id: def:eval-research
revision: "1"
name: eval-research
description: Code Mode eval scenario research.
behavior:
  purpose: Answer questions from the documents in corpus/.
requirements: {}
harness_profile:
  permissions:
    profile: default
continuity:
  mode: ephemeral
---
Answer only from the files in corpus/. Cite the file each fact comes from by its path.
