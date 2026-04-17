# [Medium] 8 builtin plugins with zero test coverage

**Scope:** internal/plugin/builtin/
**Topic:** Test Quality — plugin coverage gaps
**Date:** 2026-04-11

## Problem

8 of 13 builtin plugin packages have no test files. The untested plugins total ~1200 lines of code. While most are UI widget registrations (low risk), two — `giphy` and `oembed` — contain HTTP client logic and response parsing that operate at trust boundaries.

## Evidence

Untested builtin plugins:

| Package | Lines | Risk |
|---------|-------|------|
| `giphy` | 312 | Medium — makes outbound HTTP requests to Giphy API, parses JSON responses |
| `oembed` | 304 | Medium — makes outbound HTTP requests to oEmbed providers, parses HTML/JSON |
| `sessionstats` | 345 | Low — reads from store, computes statistics, registers API handlers |
| `agentwidgets` | 60 | Low — widget registration only |
| `bookmarks` | 49 | Low — widget registration only |
| `contextwidgets` | 66 | Low — widget registration only |
| `debugwidgets` | 68 | Low — widget registration only |
| `observabilitywidgets` | 49 | Low — widget registration only |

Tested builtin plugins (all 5 adapter plugins):
- `adapter-claude` — `plugin_test.go`
- `adapter-codex` — `plugin_test.go`
- `adapter-gemini` — `plugin_test.go`
- `adapter-nanite-native` — `plugin_test.go`
- `adapter-opencode` — `plugin_test.go`

Note: The reviewer-backend context notes that `oembed` has a dead envelope path. The `giphy` plugin makes outbound HTTP requests with user-influenced search terms (potential SSRF vector if the Giphy API URL is not hardcoded).

## Impact

The widget registration plugins are low risk — they declare UI components and return. `giphy` and `oembed` make outbound HTTP requests, which is a trust boundary. `sessionstats` registers API handlers that serve aggregated data, which could expose unexpected information if the queries are wrong.

## Recommendation

Priority:
1. `giphy` — test Giphy API integration with httptest mock, verify URL construction does not allow SSRF
2. `sessionstats` — test handler responses with a seeded test store
3. Widget plugins can remain untested (registration-only code with no branching logic)

## References

- Reviewer-backend context: "`oembed` envelope path is dead"
- `internal/plugin/builtin/adapter-claude/plugin_test.go` — reference for builtin plugin test patterns
