# [Low] Minor observations and small refactor opportunities in the sandbox package

**Scope:** sandbox — package-wide hygiene
**Topic:** Low-impact polish; error handling, tests, idioms
**Date:** 2026-04-10

Grouped set of small findings. Each is 1–2 paragraphs.

## L1. `limitedBuffer` drops silently instead of reporting truncation

`internal/sandbox/exec.go:271-290`. The `limitedBuffer` Write method discards bytes beyond `max` and returns `len(p)` as if they were written. Callers receive truncated output with no indication that truncation occurred. The `ExecResult` struct has no `StdoutTruncated` / `StderrTruncated` flag.

**Recommendation:** add a `truncated bool` field to `limitedBuffer` and expose it via getter methods. Surface `stdout_truncated` / `stderr_truncated` in `ExecResult`. Helps debugging and avoids user confusion when a script produces exactly 1MB of output.

## L2. `repr` in denylist is a pointlessly-named wrapper around quoted-string construction

`internal/sandbox/denylist.go:52-54`. `func repr(s string) string { return "\"" + s + "\"" }`. This is `strconv.Quote` without the escaping behavior. Either use `strconv.Quote` (which is correct) or use `fmt.Sprintf("%q", s)`. Remove the private helper.

## L3. `Proxy.Stop` returns the Shutdown error but `Proxy.Stop` is not checked at call sites

`internal/sandbox/exec.go:110` uses `defer proxy.Stop()`, ignoring the error. Probably fine — there's nothing actionable — but the defer-drops-error pattern is a code smell the linter will flag. Either wrap as `defer func() { _ = proxy.Stop() }()` for intentionality, or log at debug level.

## L4. `splitHostPort` silently swallows the error distinction between "no port" and "malformed"

`internal/sandbox/proxy.go:211-222`. If the input is `[::1` (malformed), the function returns the error branch. If the input is `example.com`, it returns `example.com, "", nil`. If the input is `example.com:` (trailing colon, weird but possible), `net.SplitHostPort` returns an error AND the string contains `:`, so the function returns `("", "", err)`. The callers then 400. Inconsistent handling. Just pass through `net.SplitHostPort`'s error and let the caller decide; special-casing "no colon" is a minor convenience that obscures real input bugs.

## L5. Tests do not run `go test -race` expectations on the proxy

The reviewer-context requires `-race` clean. The proxy tests exercise Start/Stop and concurrent CONNECT/HTTP paths, but there is no explicit concurrency test that exercises the lock-free `AllowedDomains` slice access under concurrent `domainAllowed` calls. If `AllowedDomains` is ever mutated after Start (not currently, but nothing enforces immutability), it's a race. Recommend either:

- Add a unit test that runs 100 parallel `domainAllowed` calls and asserts `-race` clean.
- Make the slice unexported after Start and document the construction-time-immutable contract.

## L6. `TestAgentExec_DenylistBlocked` in `exec_test.go:34-49` uses `rm -rf /` as the test input

`rm` is not in `essentialBinDirs` as a PATH — wait, actually `rm` in `/bin/rm` is on the PATH, so the test exercises the denylist before the exec would fail. But the test name implies the denylist is the gate; it happens to be. If `rm` were resolved to `/bin/rm` and the denylist were removed, the test would then attempt to run `/bin/rm -rf /` inside the seatbelt profile, which SHOULD fail the write-carve-out — but the test would actually start the exec, which is scary even in CI. Recommend: use a stub command name like `"nanite-test-denied-cmd"` and verify the denylist fires on pattern, never reaching exec. Defense-in-depth for the test suite.

## L7. `defaultAgentTimeout = 30 * time.Second` may be short for legitimate installs

`internal/sandbox/exec.go:17-26`. 30 seconds is fine for simple code execution but short for `pip install`, `npm install`, `go mod download` — the kinds of things a dev friend will want to try. `maxAgentTimeout = 5 * time.Minute` caps it. Recommend: bump default to 60s, make max configurable per-call (the code already allows this via `clampTimeout`, so this is just a doc change).

## L8. `sandbox.Dir` returns the session dir, not the `.sandbox` subdir

`internal/sandbox/sandbox.go:18-29`. The function creates `<dir>/.sandbox/` but returns `<dir>`. Callers then either `filepath.Join(dir, ".sandbox")` themselves or don't. Inconsistency leads to confusion. Consider a named return type (`SandboxPaths{Root, Sub string}`) or just returning both. Cosmetic.

## L9. `essentialBinDirs` hardcodes `/usr/local/bin` but not `/opt/homebrew/bin`

On Apple Silicon macs, Homebrew installs to `/opt/homebrew/bin`, not `/usr/local/bin`. Users with only Homebrew-installed `python3` / `node` will find those interpreters not on the sandboxed PATH. The code should probe at startup:

```go
var candidates = []string{"/usr/bin", "/bin", "/usr/local/bin", "/opt/homebrew/bin"}
var essentialBinDirs []string
for _, c := range candidates {
    if _, err := os.Stat(c); err == nil {
        essentialBinDirs = append(essentialBinDirs, c)
    }
}
```

Users will otherwise see "executable not found" errors from `nanite_code_execute` on `python3` / `node` on Apple Silicon.

## L10. `sandbox-exec` path hardcoded as `/usr/bin/sandbox-exec`

`internal/sandbox/os_darwin.go:84`. Use `exec.LookPath("sandbox-exec")` instead, for consistency with the Linux bwrap path and for systems where admin policy moved the binary. Minor.

## References

- Individual file:line citations above
- Related: `03-critical-linux-silent-sandbox-fallback.md`, `06-high-linux-bwrap-configuration-gaps.md` for the bigger issues. These L-findings are the cleanup tier.
