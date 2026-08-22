---
name: Sample Skill
slug: sample-skill
description: A sample skill fixture exercising scripts/, references/, assets/, and declared parameters/dependencies.
tags: [test, fixture]
context: inline
scripts:
  - scripts/run.sh
references:
  - references/notes.md
assets:
  - assets/logo.txt
parameters:
  - name: target
    description: The target to operate on.
    required: true
  - name: verbose
    description: Enable verbose output.
    required: false
dependencies:
  - other-skill
---

This is the sample skill's body. It exists to exercise the full
install/sync pipeline end to end: a real SKILL.md plus scripts/,
references/, and assets/ subdirectories, declared parameters, and a
declared (not necessarily installed) dependency slug.
