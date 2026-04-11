# [High] Installer does not validate sandbox prerequisites on any platform

**Scope:** installer / bonus question for sandbox audit Finding 03
**Topic:** Security — fail-closed vs silent degradation of the primary security boundary
**Date:** 2026-04-10

## Problem

The Nanite installer does not check for the presence of any OS sandbox binary (`bwrap` on Linux, `sandbox-exec` on macOS) at install time, and does not refuse to complete or warn the user when the prerequisite is missing. This directly resolves the open question left by the prior sandbox audit's Finding 03: **the installer does not gate on sandbox availability**, so that finding's "downgrade if installer hard-fails" path does not apply. Finding 03 stays Critical, and this installer finding files the missing-check as its own High.

The reviewer-context calls the OS sandbox "the primary security boundary." The installer is the only place where Nanite has a chance to notice that the boundary is absent before a user runs an agent. It misses that chance entirely. There is no preflight, no `exec.LookPath("bwrap")`, no `runtime.GOOS` branching, no config knob that would fail closed. The first time a missing sandbox is observed is at `AgentExec` time, via a single `sync.Once` `log.Println` warning (`internal/sandbox/os_linux.go:L13-25`) that the user is very unlikely to see in normal CLI output.

## Evidence

I searched the entire installer surface and the install command entry point for any sandbox-related check. There are zero hits.

```
$ grep -rni 'bwrap\|bubblewrap\|sandbox-exec\|LookPath\|preflight\|prerequisite' \
    internal/service/install/ cmd/nanite/install_cmd.go
(no output)
```

Files examined (all read in full or confirmed absent of the relevant code):

- `cmd/nanite/install_cmd.go:L1-306` — flag parsing + dispatch into `install.Service`. No preflight phase.
- `internal/service/install/install.go:L1-268` — `InstallHome`, `InstallProject`, `freshScaffold`, `archiveOnly`. No `runtime.GOOS` branching. No binary lookup.
- `internal/service/install/adopt.go`, `migrate.go`, `scaffold.go`, `resume.go`, `rollback.go` — none reference sandbox, bwrap, or exec.LookPath.
- `internal/assets/framework.go` — extracts embedded assets to `~/.nanite/` with no platform awareness beyond `os.MkdirAll` / `os.WriteFile`.

For comparison, the sandbox package does attempt to detect bwrap but only at `AgentExec` time and only logs once:

```go
// internal/sandbox/os_linux.go:L13-25
var bwrapWarnOnce sync.Once

func applyOSSandbox(cmd *exec.Cmd, sandboxDir string, networkAllow []string) (cleanup func(), err error) {
    bwrapPath, lookErr := exec.LookPath("bwrap")
    if lookErr != nil {
        bwrapWarnOnce.Do(func() {
            log.Println("sandbox: bwrap not found — install bubblewrap for OS-level isolation (using Tier 1 only)")
        })
        return func() {}, nil
    }
    ...
```

The installer has no hook into this detection path. `install.Service` never imports `internal/sandbox`.

## Impact

- **Linux users without bubblewrap installed** will complete `nanite install` successfully, have the CLI report no errors, and then run agents with zero OS-level isolation. Their only "sandbox" is the convention-level `sandboxDir` + restricted env, which is advisory. Every other sandbox finding in `docs/audits/2026-04-10-sandbox-hardening/` (seatbelt injection, proxy SSRF, process group orphans) lands on an unsandboxed host.
- **macOS users with `sandbox-exec` disabled or removed** (corporate-managed machines, hardened builds, Homebrew pkgmgr edge cases) will also install successfully. Detection fails later at `exec.Cmd.Run` time with a cryptic "fork/exec: no such file" error.
- **Cross-platform install script flows** — the installer is the only component a provisioning script would run to validate a host. A CI or dotfiles flow that runs `nanite install` and gates on exit code will always get success, regardless of sandbox state.
- **No recovery path.** There is no `nanite install --check` or `nanite doctor` style subcommand; even a user who learns about the issue has no tooling to verify their own machine.

## Recommendation

Add a preflight check to `InstallHome` (and optionally `InstallProject`) that calls `exec.LookPath` for the platform's sandbox binary and fails closed when missing. The check should be skippable with an explicit opt-out flag so CI environments and intentionally unsandboxed setups can proceed with eyes open.

Minimal sketch:

```go
// internal/service/install/preflight.go (new file)
package install

import (
    "fmt"
    "os/exec"
    "runtime"
)

// CheckSandboxPrerequisite verifies the OS sandbox binary is available.
// Returns nil on success, an error with actionable install instructions
// on failure. Callers may wrap in an --allow-missing-sandbox override.
func CheckSandboxPrerequisite() error {
    switch runtime.GOOS {
    case "linux":
        if _, err := exec.LookPath("bwrap"); err != nil {
            return fmt.Errorf("sandbox prerequisite missing: bubblewrap (bwrap) not found on PATH. " +
                "Install with: apt install bubblewrap | dnf install bubblewrap | pacman -S bubblewrap. " +
                "Pass --allow-missing-sandbox to proceed without OS-level isolation (NOT recommended).")
        }
    case "darwin":
        if _, err := exec.LookPath("sandbox-exec"); err != nil {
            return fmt.Errorf("sandbox prerequisite missing: sandbox-exec not found on PATH. " +
                "This is unusual on macOS; corporate management profiles may have removed it. " +
                "Pass --allow-missing-sandbox to proceed without OS-level isolation (NOT recommended).")
        }
    default:
        // Windows/BSD: no supported sandbox. The install service should
        // still warn loudly during scaffold, but we don't fail the install.
    }
    return nil
}
```

Wire it at the top of `cmdInstall` before the branch dispatch (so both `--project` and the home-install path hit it), controlled by a new `--allow-missing-sandbox` flag:

```go
// cmd/nanite/install_cmd.go:cmdInstall prelude
if !*allowMissingSandbox {
    if err := install.CheckSandboxPrerequisite(); err != nil {
        installDie("sandbox prerequisite", err)
    }
}
```

Trade-offs:

- **Fail loud on Linux/macOS** — matches the "sandbox-first" invariant stated in the reviewer context. Breaks existing Linux test runners that don't have bwrap; those need to pass `--allow-missing-sandbox` explicitly. That's the intended behavior.
- **Windows/BSD warn only** — the project has not promised OS sandbox support on those platforms. A loud warning at install time is appropriate; a hard failure is not.
- **Backwards compatibility** — anyone already running `nanite install` on Linux without bwrap will start getting install failures. That is the desired outcome — Finding 03 says this configuration is a release blocker. Document the new flag in release notes so existing users have an escape hatch.

Alternative (not recommended): make the installer warn but not fail. This keeps the current silent-degradation pattern and defeats the purpose of the check.

## References

- `docs/audits/2026-04-10-sandbox-hardening/03-critical-linux-silent-sandbox-fallback.md` — the open question this finding answers.
- `internal/sandbox/os_linux.go:L13-25` — the existing runtime-level LookPath that the installer should front-run.
- `cmd/nanite/install_cmd.go:L20-197` — the dispatch point where the preflight would live.
- Cross-audit resolution note in this audit's `index.md` under "Cross-audit resolutions".
