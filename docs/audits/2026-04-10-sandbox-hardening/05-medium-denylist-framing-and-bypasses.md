# [Medium] Denylist relies on lowercased substring match; produces false positives and is mis-framed as a security control

**Scope:** sandbox / denylist / shell command execution
**Topic:** Security — ineffective control, defense-in-depth gap
**Date:** 2026-04-10

## Problem

`sandbox.CheckDenylist` blocks commands by lowercasing the full command string and checking for substring containment against a fixed list of literal patterns. The patterns include `"rm -rf /"`, `"mkfs"`, `"curl | sh"`, etc. This check runs on both `AgentExec` and `UserExec`.

The reviewer context explicitly frames this as "second line of defense; the OS sandbox is primary." That framing is fine for `AgentExec` and for `UserExec` when `Sandboxed: true`. But the denylist is the **only** control when:

- `UserExec` runs with `Sandboxed: false` — i.e. YOLO mode in the shell API (`internal/api/shell.go:89`).
- `AgentExec` runs on a platform without sandbox-exec or bwrap (`os_other.go` — the no-op fall-through).
- `bwrap` is not installed on Linux (`os_linux.go:20-25` — logs a warning and returns a no-op cleanup).

On those paths, the denylist is trivially defeated by common shell metaprogramming:

- Variable expansion: `r""m -rf /`, `r\m -rf /`, `"rm" -rf "/"`, `${x}m -rf /` where `x=r`.
- Command substitution: `` `echo rm` -rf / ``, `$(printf 'rm') -rf /`.
- Base64 + eval: `eval "$(echo cm0gLXJmIC8= | base64 -d)"`.
- IFS hacks: `r${IFS}m -rf /` — not blocked because the substring match requires `"rm -rf /"` verbatim (the space is literal).
- Absolute paths: `/bin/rm -rf /` — not blocked; pattern is `"rm -rf /"` so `/bin/rm -rf /` does contain it, BUT `rm -rf /home/` (legit-looking cleanup) is ALSO blocked as a false positive while `/usr/bin/mv /tmp/foo /tmp/bar` is not blocked even though `mv /` is listed as a pattern — `/usr/bin/mv /tmp/foo` contains `mv /` and is therefore falsely blocked too. The substring approach is both over- and under-inclusive.
- Tool choice: the list contains `rm -rf /` but not `find / -delete`, `perl -e 'unlink(...)'`, `python -c "import shutil; shutil.rmtree('/')"`, `rsync --delete`, `shred`, `truncate`, `> /etc/hosts`, `cat /dev/urandom > /dev/sda`, or any of the fork-bomb variants beyond the single hard-coded `:(){ :|:& };:`.

In short: the denylist blocks naive copy-paste from a scary tutorial, nothing else. Against an adversarial agent or a user who pastes any non-trivial command, it provides no defense.

## Evidence

```go
// internal/sandbox/denylist.go:42-50
func CheckDenylist(command string) (blocked bool, reason string) {
    lower := strings.ToLower(strings.TrimSpace(command))
    for _, p := range defaultDenyPatterns {
        if strings.Contains(lower, strings.ToLower(p.pattern)) {
            return true, "blocked by denylist: " + p.reason + " (matches " + repr(p.pattern) + ")"
        }
    }
    return false, ""
}
```

Denylist is called by `CheckDenylist(fullCmd)` where `fullCmd` is the literal `opts.Command + " " + strings.Join(opts.Args, " ")`:

```go
// internal/sandbox/exec.go:84-92
func AgentExec(opts AgentExecOpts) (*ExecResult, error) {
    fullCmd := opts.Command
    if len(opts.Args) > 0 {
        fullCmd += " " + strings.Join(opts.Args, " ")
    }
    if blocked, reason := CheckDenylist(fullCmd); blocked {
        return nil, fmt.Errorf("sandbox: agent-exec denied: %s", reason)
    }
    ...
}
```

YOLO path from the shell API, where the command is user-supplied and passed through `sh -c`:

```go
// internal/api/shell.go:83-90
shPath := resolveShell()
result, err := sandbox.UserExec(sandbox.UserExecOpts{
    Command:   shPath,
    Args:      []string{"-c", req.Command},
    Dir:       workDir,
    Sandboxed: mode != shell.ModeYOLO, // OS sandbox for ask+session, not yolo
})
```

When the user is in YOLO mode, `Sandboxed: false`, so `applyOSSandbox` is not invoked. The only remaining control is `CheckDenylist`. The check sees the string `sh -c <full user command>`. The user can trivially craft any bypass above.

False positive example: a user in `session` mode runs `git log --oneline` in a repo whose path contains `dd` or `rm` — the test in `exec_test.go:212-223` even exercises some of these intentionally, showing that `rm -rf ./build` is treated as allowed (the pattern is `rm -rf /` with a trailing slash, and `./build` doesn't contain that). Good for that case, but fragile: `rm -rf /tmp/scratch` is blocked because it contains `rm -rf /`, even though it's a perfectly legitimate local cleanup. Users will hit this and reach for YOLO.

## Impact

- **Who:** primarily the YOLO-mode user shell path and any AgentExec on Linux without bwrap installed.
- **What:** the denylist provides no meaningful protection against an attacker with any shell literacy. Against the prompt-injection threat model, it's easier to defeat than an XSS filter.
- **Blast radius:** in YOLO, full user-shell access (by design). In AgentExec without bwrap, full filesystem and network access to the Nanite process user, because the no-op cleanup from `os_linux.go:23-24` returns `func() {}` and `AgentExec` continues as if the sandbox succeeded. See finding `05-critical-linux-silent-sandbox-fallback.md` — bundled there because that silent fallback is the real bug and the denylist weakness is just what's left.
- **User-visible false positives:** yes. Users will hit spurious blocks and blame the denylist.

## Recommendation

Three separate fixes; none is enough on its own.

1. **Rename and reframe the denylist.** It is not a security boundary. Rename to `DangerousCommandHint` or similar, log the warning, and require explicit user confirmation before running. Do NOT advertise it as "blocked." Users and the audit trail should see it as an advisory.

2. **Replace substring match with tokenization.** Parse the command with a shell lexer (`github.com/mvdan/sh`) and match on the tokenized command name and argument shape. `rm` with an argument of `/` or a pattern matching `/-?[a-z]*r` is suspicious; `rm -rf /tmp/scratch` is not. This fixes both the false positives and the obvious bypasses.

   - Accept that this won't catch `perl -e ...` etc. — that's why it's a hint, not a control.

3. **Require the OS sandbox everywhere it's available and abort if it isn't.** On Linux without bwrap, `applyOSSandbox` currently prints one warning and returns. That is the wrong default for a beta "sandbox-first" product. Change the default to fail closed, with an explicit opt-in env var (`NANITE_ALLOW_UNSANDBOXED_AGENT_EXEC=1`) for CI/test use. See finding `05-critical-linux-silent-sandbox-fallback.md`.

4. **(Optional) Add fuzz tests** around `CheckDenylist` using the shell lexer so future regressions are caught. Table-drive both obvious bypasses and legitimate commands.

## References

- `internal/sandbox/denylist.go` (full file)
- `internal/sandbox/exec.go:84-92, 144-152`
- `internal/api/shell.go:83-90`
- `internal/sandbox/exec_test.go:194-225` — current test coverage
- Related: `05-critical-linux-silent-sandbox-fallback.md`
- CWE-185 (incorrect regular expression), CWE-693 (protection mechanism failure)
