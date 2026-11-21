---
name: memory
description: How to recall and record durable knowledge for your area through the memory service's tools.
---
# Memory

Your conversation history is searchable with `history_search` and
`history_get`, but it is yours alone and grows stale. Durable knowledge that
other agents and later sessions should find goes to the shared memory
service, reached as tools (for example `tesseract_recall`, `tesseract_get`,
`memory_write` and `knowledge_write`; the exact names depend on how the host
exposes them, so list your tools if unsure).

## Your namespace

Write only under your own area's namespace, for example
`project/workflow-design/memory/decisions` and `project/workflow-design/knowledge/<topic>`.
Read anywhere you need.

## Recall first

Before non-trivial work, recall with a short query naming the topic. A hit is
evidence, not a ruling: a clear current instruction outranks an old note.

## What to record

| Record | When |
|---|---|
| A decision and its reason | You chose between real options, or the owner ruled. |
| A finding | You had to dig for a fact someone will need again. |
| A limitation | You deliberately left something undone, and why. |

Keep each entry short, with a one-line summary first. Never record secrets,
tokens or personal data.

## When the tools are missing

If no memory tools are available, say so once, keep notes in the
conversation, and continue.
