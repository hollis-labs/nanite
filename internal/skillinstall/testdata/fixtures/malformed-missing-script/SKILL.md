---
name: Malformed Skill
slug: malformed-missing-script
description: A malformed skill fixture whose scripts entry does not exist on disk.
context: inline
scripts:
  - scripts/does-not-exist.sh
---

This package declares a scripts/ entry that was never actually created,
to exercise the install/sync pipeline's Validator failure path.
