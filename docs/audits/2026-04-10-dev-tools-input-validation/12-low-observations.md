# [Low] Grouped: minor observations and small refactor opportunities

**Scope:** `internal/mcp/dev_tools.go`, `internal/mcp/general_tools.go`
**Topic:** Idioms, standards, naming
**Date:** 2026-04-10

Small items that are worth noting but don't individually merit a file. Each has one-line context.

---

## 12.1 — `ListTools` describes `dev_bash` alongside `dev_read`/`dev_write` without flagging the sandbox discrepancy

**File:** `internal/mcp/dev_tools.go:L124-L136`
**Observation:** the tool description says "Execute a shell command" and is listed alongside tools that are path-scoped. The LLM has no signal that this tool is more dangerous than `dev_write`. Even after finding 01 is fixed, the description should explicitly say "runs in the sandbox" so the model's risk model matches the implementation.

## 12.2 — Hardcoded 8000-char limit in `web_fetch` is a magic number

**File:** `internal/mcp/general_tools.go:L188-L189`
**Observation:** `io.LimitReader(resp.Body, 8000)` — the value is repeated in the tool description. Move to a `const webFetchMaxBody = 8000` at the top of the file so the description and the implementation can reference the same constant.

## 12.3 — `callHash` supports MD5 as a default-available algorithm

**File:** `internal/mcp/general_tools.go:L384-L404`
**Observation:** MD5 is still useful for content addressing and compatibility, so it is not wrong to expose it — but the tool description should note "MD5 is for non-cryptographic use only." A naive user invoking `hash(input=secret, algorithm=md5)` thinking they're getting a cryptographic hash is a documentation gap, not a bug.

## 12.4 — `callThink` returns `"Thought recorded."` for any non-empty thought

**File:** `internal/mcp/general_tools.go:L424-L437`
**Observation:** the tool is documented as a scratchpad that "acknowledges receipt." The implementation does exactly that and is clean. Info/Low praise.

## 12.5 — `globMatch` custom implementation rather than stdlib `filepath.Match`

**File:** `internal/mcp/dev_tools.go:L465-L497`
**Observation:** `filepath.Match` doesn't support `**`, so the custom implementation is justified. Consider `doublestar` (`github.com/bmatcuk/doublestar/v4`) which is the de-facto Go stdlib-extension for this and is well-tested. If the dependency is undesirable, add a unit test for `**` edge cases (`**/foo`, `a/**/b`, `**/*.md`, patterns with literal `[` or `?`). No unit tests for `globMatchParts` exist today.

## 12.6 — `callDatetime` operation parser duplicates `time.ParseDuration` for `h`/`m`/`s`

**File:** `internal/mcp/general_tools.go:L299-L342`
**Observation:** Go's `time.ParseDuration` already handles `h`/`m`/`s` (and `ms`/`us`/`ns`). Hand-rolling the parser just for `d`/`w` is fine, but the hand-rolled version is more code than necessary. Could be `parseDuration := strings.NewReplacer("d","h").Replace(...)` with a `*24` — tricky but smaller. Not worth fighting over; the current code is clear.

## 12.7 — Scanner buffer cap duplicated in two handlers

**File:** `internal/mcp/dev_tools.go:L182`, `L258`
**Observation:** `scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)` appears in both `callRead` and `callGrep`. Extract to a package-level constant `const maxScannerLineSize = 1 << 20`.

## 12.8 — Tools dispatch on a hardcoded switch rather than a `map[string]func(args) (*ToolResult, error)`

**File:** `internal/mcp/dev_tools.go:L141-L158`, `internal/mcp/general_tools.go:L148-L173`
**Observation:** ~12 cases each, fine to dispatch via switch. A map would make the `ListTools` and `CallTool` definitions share a single source of truth, reducing the drift risk where a tool is added to one but not the other. Medium-term refactor opportunity. Not urgent.

## 12.9 — `dev_edit` uses a 0o644 mode literal in two places

**File:** `internal/mcp/dev_tools.go:L323`, `L367`
**Observation:** both handlers should read the existing file's mode and preserve it (see finding 09). The literal `0o644` appears twice; once the fix lands, the literal is a constant like `defaultNewFileMode = 0o600`.

## 12.10 — `url_encode` uses `url.QueryEscape` which uses `+` for space

**File:** `internal/mcp/general_tools.go:L364-L370`
**Observation:** `url.QueryEscape` is correct for query string values but not for path components (which need `%20`, not `+`). The tool description says "URL percent-encode a string" without specifying which encoding. Consider two tools — `url_query_escape` and `url_path_escape` — or document the behavior and let the caller wrap.

---

## References

- `internal/mcp/dev_tools.go` — all locations
- `internal/mcp/general_tools.go` — all locations
