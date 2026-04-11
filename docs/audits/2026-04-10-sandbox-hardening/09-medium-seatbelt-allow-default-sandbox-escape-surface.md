# [Medium] macOS seatbelt profile uses `(allow default)`, leaving most of the sandbox-exec attack surface open

**Scope:** sandbox / macOS platform
**Topic:** Security — weak isolation baseline
**Date:** 2026-04-10

## Problem

The seatbelt profile is written with `(allow default)` followed by a narrow `(deny file-write*)` carve-out and a network block. The comment explains the choice:

```
; Strategy: allow default, then deny file writes outside sandbox and network.
; This is more practical than deny-default because macOS processes need many
; mach ports, sysctls, and IPC operations that are hard to enumerate.
```

That's pragmatic, but it leaves all of the following allowed by default:

- **File reads** anywhere on the system — ~/.aws/credentials, SSH keys, browser cookies, user keychains (if they're on-disk), source repos.
- **Process inspection** via mach ports, `ps`, `sysctl`, process_info — sandboxed process can enumerate other processes on the host, read their env via `ps auxe`, etc.
- **Mach IPC** to arbitrary bootstrap-registered services. On macOS, mach bootstrap registration is a significant IPC surface — launchd, pasteboard, keychain, iconservices, etc. all expose mach interfaces.
- **Sending signals** to other processes owned by the same user.
- **Connecting to unix domain sockets** anywhere the user has filesystem reach. Docker, Postgres, Redis, any dev server listening on a socket.
- **Spawning subprocesses** via `posix_spawn` / `fork+exec`. No `process-exec*` restriction.

The existing deny rules address file WRITES (good, but see gap #1 below) and network outbound (good on darwin, enforced). Everything else on macOS's rich IPC and read surface is open.

This matches the reviewer-context honesty about sandbox strength, but the combination of "sandbox is the primary security boundary for untrusted/semi-trusted code" and `(allow default)` is a gap that should be closed or explicitly documented.

Gap #1 on the write deny: the carve-out allows writes to `/private/tmp`, `/tmp`, `/dev/null`, `/dev/tty`, `/dev/fd`. `/private/tmp` and `/tmp` are world-shared — two concurrent sandboxed processes can collide, or one can plant a file the other picks up. `/dev/tty` is the user's terminal, as also noted in the Linux finding (`06-high-linux-bwrap-configuration-gaps.md`).

## Evidence

```scheme
; internal/sandbox/os_darwin.go:17-56 (generated)
(version 1)
(allow default)

; Deny file writes outside sandbox
(deny file-write*
  (require-not
    (require-any
      (subpath "<sandboxDir>")
      (subpath "/private/tmp")
      (subpath "/tmp")
      (literal "/dev/null")
      (literal "/dev/tty")
      (subpath "/dev/fd")
    )
  )
)

; Deny network (or allow localhost only if proxy is active)
(deny network-outbound)
(deny network-inbound)
```

No restriction on `file-read*`, `process-fork`, `process-exec*`, `mach-lookup`, `mach-register`, `ipc-posix-*`, `signal`, `sysctl-read`, `sysctl-write`, `iokit-open`. All allowed via `(allow default)`.

The Apple System Configuration Framework sandbox profiles (in `/System/Library/Sandbox/Profiles/`) are examples of how a proper macOS sandbox profile is constructed — deny-default with explicit allows, which is much more work. The decision to go allow-default is defensible for a beta but should be tracked as a known limitation.

## Impact

- **Who:** all macOS users.
- **What:** sandboxed tool execution can read any file the Nanite user can read, enumerate processes, send mach IPC, spawn further processes. File writes outside the sandbox dir are blocked (good).
- **Blast radius:** comparable to Linux gap #1 (host filesystem reads). Combined with the proxy SSRF (finding 02), a compromised tool can exfiltrate anything it can read to attacker-allowlisted domains.

## Recommendation

Near term (for beta):

1. **Document the allow-default stance in user-facing security docs.** "The sandbox denies filesystem writes outside the sandbox dir and denies non-localhost network. It does NOT deny reads of files outside the sandbox dir. Do not keep secrets on disk under the Nanite user that the agent should not see." This is the cheap fix and matches the current design.

2. **Move `/tmp` and `/private/tmp` out of the write allowlist.** Replace with a per-session tmp under the sandbox dir: `cmd.Env = append(cmd.Env, "TMPDIR="+filepath.Join(sandboxDir, "tmp"))`. Create that dir before exec. This prevents cross-session collisions.

3. **Deny `(deny file-read* (subpath "/Users/..."))` for user config dirs** — `.aws`, `.ssh`, `.gnupg`, `.config/gcloud`, `.anthropic`, `.netrc`, `.npmrc`, `.pypirc`, `.docker`. This is surgical and high-value. The pattern:

   ```scheme
   (deny file-read*
     (require-any
       (subpath "${HOME}/.aws")
       (subpath "${HOME}/.ssh")
       (subpath "${HOME}/.gnupg")
       (subpath "${HOME}/.config/gcloud")
       (subpath "${HOME}/.anthropic")
       (literal "${HOME}/.netrc")
       ...
     )
   )
   ```

   Note that seatbelt does not expand `$HOME`; substitute at profile-generation time.

4. **Add `(deny process-exec*)` or at least restrict it to interpreters** once the sandboxed process is running. This prevents an injected agent from spawning `curl`, `ssh`, `nc`, etc. as secondary tools. Hard to get exactly right — e.g. Python's `subprocess` module will break. Document as a known trade-off; keep the gate open for beta.

Medium term:

5. **Switch to deny-default** after cataloguing the exact mach services, ports, and sysctls needed by `sh`, `python3`, and `node`. This is a multi-day task per interpreter. Good candidate for a post-beta hardening pass.

6. **Add per-interpreter profiles.** `python3` needs very different allows than `sh`. A single profile is lowest-common-denominator. Generated profiles per interpreter can tighten each.

## References

- `internal/sandbox/os_darwin.go:13-57`
- Apple: `man sandbox_init`, `man sandbox-exec`, `/System/Library/Sandbox/Profiles/*.sb` for examples
- TinyScheme profile reference: unofficial, collected in `/usr/share/sandbox/*.sb`
- Related: `06-high-linux-bwrap-configuration-gaps.md` (same design gap on the other platform)
