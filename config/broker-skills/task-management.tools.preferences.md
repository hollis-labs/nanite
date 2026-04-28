---
pattern: "re:audit (tasks|backlog|sprint)"
prefer:
  - clockwork_task_list
  - clockwork_task_get
weight: 6
rationale: |
  "Audit tasks/backlog/sprint" is a recurring orchestrator pattern. Recall +
  inspect, in that order. The broker would pick clockwork_task_list on keyword
  alone, but clockwork_task_get rarely scores high on its own — the skill
  pulls it in.
---

# Task / backlog audit

Boots clockwork_task_list + clockwork_task_get for any "audit X" intent that
mentions tasks, backlog, or sprint.
