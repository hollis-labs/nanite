# Coding Standards

Stub — real content added as it's actually established, not invented wholesale. What's genuinely observable from this codebase's own conventions today:

- **Comments explain WHY, not WHAT.** The strongest doc comments in this codebase cite a ticket, a specific historical bug, or a non-obvious constraint (e.g. "removed after causing a distinct '0 seconds' bug class") rather than restating what the code does. Follow that pattern.
- **Check the glossary before introducing a new term.** This codebase has a real, repeated, expensive history of naming collisions. Reuse existing vocabulary or pick something genuinely distinct — see `../GLOSSARY.md`.
- **Prefer a typed field over a string convention checked in multiple places.** Several of this review's worst bugs trace back to a decision being encoded as a string pattern (a `pty-` prefix, a hardcoded model string) matched independently at multiple call sites that could drift out of sync. A single typed field, checked once, is worth the migration.
- **Real foreign keys over free-text strings for anything referencing another entity.** Tool names, skill names, model IDs — the pattern of "a string that's supposed to match something else, with no enforced relationship" has been the direct cause of most of the tool-name-typo and stale-config bug classes found in this codebase's history.

## Not yet documented

Formatting/linting conventions beyond `gofmt`/standard Go tooling, error-handling conventions, package-naming conventions beyond the `agent/*`/`prompt/*`-style grouping already established, a real style guide for the frontend.
