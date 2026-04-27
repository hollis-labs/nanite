---
pattern: "re:(search code|investigate|find function|explore codebase)"
prefer:
  - dev_glob
  - dev_grep
  - dev_read
weight: 7
rationale: |
  Codebase exploration almost always starts with the dev_* triad: dev_glob to find
  files, dev_grep to find call sites or definitions, dev_read to inspect.
  Operators surface this skill so the broker biases toward those three tools when
  the LLM's intent reads as code investigation, even before the keyword scorer
  ranks them.
---

# Codebase exploration

When the user asks to investigate, search, or explore code, prefer the dev_*
triad. This skill outweighs the keyword match because exploration intents
often surface non-code tools (e.g., context_search, knowledge_get) higher
than code-specific tools when scored purely on lexical overlap.
