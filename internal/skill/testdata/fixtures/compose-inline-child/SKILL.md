---
name: Compose Inline Child
slug: compose-inline-child
description: A minimal inline-composed nested skill with one required parameter, used to prove both content splicing and parameter substitution.
context: inline
parameters:
  - name: target
    description: The target to act on.
    required: true
---

Child body: operate on {{target}}.
