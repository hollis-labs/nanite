---
name: design-handoff
description: How to brief the design team on an agreed workflow, iterate on their designs, and hand the result to the Surface team as a module.
---
# Design handoff

## The brief

Send the design team one brief per workflow:

```markdown
# <Workflow name>: design brief
For: <who does this work>   Goal: <one sentence>
Why: <the pain it removes, from discovery>
Workflow: <the step table>
Screens: <the sketches, one per step>
Decisions and approvals: <where the person must choose or say yes>
Agent's part: <what the agent prepares at each step>
Out of scope: <what this module does not do>
Constraints: <Surface Module conventions that apply; see surface-modules>
Open questions: <what you need the designers to decide>
```

Follow the Surface Module conventions (the `surface-modules` skill): modules
use the shared component registry and card patterns, so name the patterns
you expect rather than inventing new controls.

## Iterating

- Review each design round against the workflow: one decision per screen,
  progress visible, the agent's part clear, approvals explicit.
- Comment on what the person would experience, not on taste.
- When the person and the design team agree, record the final design and the
  reasons (the `memory` skill).

## To the Surface team

Hand the agreed design to the Surface lead for building as a module, with
the brief, the final design and the agent's part. The module's own agent
setup goes to the Nanite setup expert.
