---
name: Resolver Parent
slug: resolver-parent
description: A skill declaring both a required, resolver-bound parameter and a nested dependency on resolver-child.
parameters:
  - name: greeting
    description: A greeting to include in the materialized output.
    required: true
    resolver_slot: greeting-slot
  - name: optional-note
    description: An optional note with no dynamic binding.
    required: false
dependencies:
  - resolver-child
---

This is the resolver-parent skill's body.
