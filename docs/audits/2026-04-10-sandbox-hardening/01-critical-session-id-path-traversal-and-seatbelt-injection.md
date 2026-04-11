# [Critical] Unvalidated MCP `session_id` yields path traversal and seatbelt profile injection

**Scope:** sandbox / built-in MCP tool `nanite_code_execute`
**Topic:** Security — input validation at a trust boundary, sandbox escape
**Date:** 2026-04-10

## Problem

The built-in MCP tool `nanite_code_execute` forwards its `session_id` argument directly into `sandbox.Dir(sessionID)` and then into `sandbox.AgentExec(AgentExecOpts{SessionID: sessionID})` without any validation. `sessionID` is joined straight into a filesystem path and, on macOS, interpolated **unescaped** into a TinyScheme seatbelt profile. This yields two distinct exploit primitives, either of which is a sandbox escape.

The trust boundary is real: tool arguments are LLM-generated and influenceable by anyone who can deliver a prompt-injection payload (an MCP server result, a user message, a web_fetch response, a read file, etc.).

## Evidence

Tool entry point — `session_id` is read from `args` with no validation:

```go
// internal/mcp/code_exec_tools.go:115-140
sessionID, _ := args["session_id"].(string)
if sessionID == "" {
    sessionID = c.DefaultSessionID
}

// Resolve sandbox directory to write the temp script file.
sandboxDir, err := sandbox.Dir(sessionID)
if err != nil {
    return errorResult(fmt.Sprintf("sandbox dir: %v", err)), nil
}
...
result, err := sandbox.AgentExec(sandbox.AgentExecOpts{
    SessionID: sessionID,
    Command:   interp.binary,
    Args:      []string{scriptPath},
    ...
})
```

`sandbox.Dir` joins `sessionID` into a filesystem path under `$HOME` and `MkdirAll`s it:

```go
// internal/sandbox/sandbox.go:18-29
func Dir(sessionID string) (string, error) {
    home, err := os.UserHomeDir()
    ...
    dir := filepath.Join(home, baseDirName, sessionID)
    subDir := filepath.Join(dir, sandboxSubDir)
    if err := os.MkdirAll(subDir, 0755); err != nil {
        return "", fmt.Errorf("sandbox: create dir: %w", err)
    }
    return dir, nil
}
```

`filepath.Join` does NOT reject `..` — it cleans the path, and `..` segments collapse out of the intended base. `sessionID = "../../../../tmp/evil"` yields `dir = /tmp/evil`, and `MkdirAll` creates it. `sessionID = "../../../../../etc/nanite"` attempts `/etc/nanite` (fails on EPERM, but a writable path under HOME's parent — or any world-writable directory — succeeds). The attacker picks an arbitrary target directory as the sandbox root.

The attacker-chosen directory is then passed to `applyOSSandbox` on macOS, where it is interpolated into a TinyScheme s-expression string literal:

```go
// internal/sandbox/os_darwin.go:17-42
func seatbeltProfile(sandboxDir string, networkAllow []string) string {
    absDir, err := filepath.Abs(sandboxDir)
    if err != nil {
        absDir = sandboxDir
    }

    var b strings.Builder
    b.WriteString("(version 1)\n")
    b.WriteString("(allow default)\n\n")

    b.WriteString("; Deny file writes outside sandbox\n")
    b.WriteString("(deny file-write*\n")
    b.WriteString("  (require-not\n")
    b.WriteString("    (require-any\n")
    fmt.Fprintf(&b, "      (subpath \"%s\")\n", absDir)
    ...
```

`absDir` is embedded inside a `"..."` literal with no escaping. Any `"` or `)` in the attacker-chosen path breaks out of the literal and lets the attacker inject arbitrary seatbelt rules.

A session_id of:

```
foo") (allow file-write*) (allow network*) ;"
```

is a valid Unix filename component. After `filepath.Abs`, it lands verbatim inside the profile, producing:

```
(subpath "/Users/victim/.nanite/sandboxes/foo") (allow file-write*) (allow network*) ;"
```

The closing `")` terminates the `subpath`, the injected `(allow file-write*)` grants host-wide writes, and `(allow network*)` restores full network access. The remaining `;"` is a line comment ending at EOL. The sandbox is now a no-op.

Note: the injection path requires the filename to survive `filepath.Clean` inside `Abs`. `Clean` preserves `"`, `)`, `(`, `;`, and spaces. Only `/` has special meaning, and it would split into multiple path components — but adding `/` to the payload just nests it deeper under the sandbox root, which does not defeat the injection.

A no-op variant using only the path-traversal primitive is also critical on its own: `session_id = "../../../../../../tmp/attacker"` runs the script in an attacker-chosen directory outside the sandbox root. If that directory already contains binaries or config the attacker wants the interpreter to load (e.g. `.envrc`, `package.json`, `__pycache__`), it's a starting point for further escalation even without seatbelt injection.

## Impact

- **Who:** any client that can cause a Nanite agent to invoke the `nanite_code_execute` MCP tool with an attacker-controlled `session_id`. In practice, prompt injection through any untrusted text channel (user message, MCP result, web_fetch, PTY output, read file) is enough. The agent does not need to be "compromised" — it just needs to be told.
- **What:** full sandbox escape on macOS via seatbelt rule injection; write-anywhere-the-user-can-write via path traversal on both platforms.
- **Blast radius:** host-wide. On a single-user dev machine (the beta target audience), this is equivalent to RCE-as-the-user. Nanite's beta docs acknowledge that the sandbox is the primary security boundary for "semi-trusted code"; this finding invalidates that claim for anyone who can influence a tool argument.
- **Reproducibility:** deterministic. No race, no timing.

Reproduction sketch (do NOT run):

1. Plant a crafted prompt in any source the agent reads (user message, `dev_read`ing a malicious file, `web_fetch` of an attacker page, an MCP result).
2. Prompt induces the agent to call `nanite_code_execute` with `session_id = "foo\") (allow file-write*) ;\""` and `code = <shell that touches /etc/nanite-pwned or reads ~/.aws/credentials>`.
3. On macOS, the seatbelt profile parses with the injected rule; the write or read succeeds. On Linux, the traversal places the sandbox root wherever the attacker wants but bwrap arg injection is not available (args go through slice, not shell), so Linux is "only" path traversal — still Critical.

## Recommendation

Treat `sessionID` as untrusted input everywhere it crosses into the filesystem or the sandbox profile. Two layers of defense; apply both:

1. **Strict whitelist at the MCP tool boundary.** In `CodeExecTransport.callCodeExecute`, reject any `session_id` that doesn't match `^[A-Za-z0-9_-]{1,64}$` before touching `sandbox.Dir`. This is the minimal safe character class for UUIDs, slugs, and test session names, and it eliminates every byte that matters for traversal or s-expression injection.

   ```go
   var sessionIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

   sessionID, _ := args["session_id"].(string)
   if sessionID == "" {
       sessionID = c.DefaultSessionID
   }
   if !sessionIDRe.MatchString(sessionID) {
       return errorResult("session_id must match [A-Za-z0-9_-]{1,64}"), nil
   }
   ```

2. **Defense in depth in `sandbox.Dir`.** The library function must not trust its caller. Validate the slug there too, and after building `dir`, verify that `filepath.Clean(dir)` is still lexically contained within `filepath.Join(home, baseDirName)`:

   ```go
   func Dir(sessionID string) (string, error) {
       if !sessionIDRe.MatchString(sessionID) {
           return "", fmt.Errorf("sandbox: invalid session id")
       }
       home, err := os.UserHomeDir()
       if err != nil { ... }
       base := filepath.Join(home, baseDirName)
       dir := filepath.Join(base, sessionID)
       if !strings.HasPrefix(dir+string(os.PathSeparator), base+string(os.PathSeparator)) {
           return "", fmt.Errorf("sandbox: session id escapes base dir")
       }
       ...
   }
   ```

3. **Seatbelt profile must not rely on the caller.** Even with 1 and 2 in place, escape the path before embedding it in the profile. TinyScheme string literals use `\"` and `\\` for escaping; escape both:

   ```go
   func escapeSeatbeltString(s string) string {
       s = strings.ReplaceAll(s, `\`, `\\`)
       s = strings.ReplaceAll(s, `"`, `\"`)
       return s
   }
   ...
   fmt.Fprintf(&b, "      (subpath \"%s\")\n", escapeSeatbeltString(absDir))
   ```

   Add a unit test that constructs a profile with a path containing `"` and `)` and asserts the generated profile parses cleanly (or at least doesn't contain an unescaped `"` after the subpath opener).

4. **Audit every other call site of `sandbox.Dir`.** `chat_generate.go:891` passes a session ID from the `Session` store record — that's normally a UUID, but the store's session ID write path should be sanity-checked to confirm no code path lets a user set a session ID via API. This is out of scope for the fix but in scope for the follow-up pass.

Recommended severity: Critical. Fix all of 1–3 before beta.

## References

- `internal/mcp/code_exec_tools.go:115-140`
- `internal/sandbox/sandbox.go:18-29`
- `internal/sandbox/os_darwin.go:17-57`
- `cmd/nanite/main.go:403` — registration of the transport
- Apple sandbox-exec / TinyScheme profile language — no published spec; escaping behavior confirmed by source inspection of TinyScheme
- OWASP Path Traversal (CWE-22)
- Related: finding `02-critical-proxy-ssrf-rfc1918-and-port.md` (separate sandbox boundary, compounding impact)
