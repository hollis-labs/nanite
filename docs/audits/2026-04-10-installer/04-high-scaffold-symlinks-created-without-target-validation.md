# [High] `ScaffoldNaniteDir` creates symlinks into `globalHome` without verifying the target exists

**Scope:** installer / fresh scaffold + adopt-existing flows
**Topic:** Correctness / resilience — broken symlinks ship silently
**Date:** 2026-04-10

## Problem

`ScaffoldNaniteDir` creates three symlinks from `{projectDir}/.nanite/{roles,skills,commands}` to `{globalHome}/{roles,skills,commands}`. The code does NOT verify that the link targets exist before creating them. `os.Symlink` does not care whether the destination exists — it just writes the link. If the user has never run `nanite install` without `--project`, or if they've moved / deleted `~/.nanite/`, or if their `globalHome` was set to a typo, all three symlinks will be dangling. Every subsequent tool that reads from `.nanite/roles/` or `.nanite/skills/` will get `fs.ErrNotExist` with no hint that the cause was a broken symlink into a missing home install.

This is especially problematic for the `adoptExisting` flow — the user is explicitly telling Nanite "this project already has `.nanite/`, just fill in missing pieces." If `.nanite/roles` is absent (because the user copied `.nanite/` from another project without the symlinks) the adopt flow will silently create a broken symlink rather than telling the user their home install is missing or incomplete.

## Evidence

```go
// internal/service/install/scaffold.go:L60-73
// Symlinks into globalHome. Anything already present at the link path is
// left alone to preserve the idempotency contract.
for _, sub := range []string{"roles", "skills", "commands"} {
    link := filepath.Join(naniteDir, sub)
    target := filepath.Join(globalHome, sub)
    if _, err := os.Lstat(link); err == nil {
        continue // already exists — don't replace
    } else if !errors.Is(err, fs.ErrNotExist) {
        return fmt.Errorf("lstat %s: %w", link, err)
    }
    if err := os.Symlink(target, link); err != nil {
        return fmt.Errorf("symlink %s -> %s: %w", link, target, err)
    }
}
```

There is no `os.Stat(target)` check. There is no guard that `globalHome` itself is a valid directory. The only evidence that the home install has been run is implicit — `InstallProject` defaults `globalHome` to `filepath.Join(home, ".nanite")` but never verifies it exists.

Symmetry check at the caller:

```go
// internal/service/install/install.go:L116-123
globalHome := opts.GlobalHome
if globalHome == "" {
    home, err := os.UserHomeDir()
    if err != nil {
        return nil, fmt.Errorf("resolve home dir: %w", err)
    }
    globalHome = filepath.Join(home, ".nanite")
}
```

Again, no `os.Stat(globalHome)` check before the scaffold step.

### Existing-but-dangling symlink trap

The scaffold loop's `os.Lstat(link); err == nil; continue` clause is specifically designed to preserve existing symlinks. That's idempotent when the target is live. When the link is broken — for example, from a previous install that succeeded against a `~/.nanite/` that has since been removed — the adopt-existing path will silently leave the broken link in place. The user now has a project `.nanite/` that looks complete but whose `roles`, `skills`, and `commands` all point into the void.

### Test gap

`scaffold_test.go` exercises the fresh creation path with a real tempdir-based fake home. It does not exercise the "target missing" case and does not assert that `os.Readlink(link)` + `os.Stat(target)` would succeed.

## Impact

- **Fresh install with missing or typo'd `globalHome`** — installer completes successfully, user gets a working-looking `.nanite/` that has no roles/skills content. The error surfaces only when an agent is booted and can't load its role file, with a generic "no such file or directory" message that does not point at the broken symlink.
- **Copied-from-another-project `.nanite/`** — a very common beta-user flow. User clones a project from a teammate who already configured Nanite, runs `nanite install --project .` to adopt it, and everything appears to work until the first agent boot fails.
- **Phase 4 Task 16** — the task this audit is gating for. `nanite install --project ~/Projects-apps/nanite` runs against a repo that already has `.nanite/`. If any of the existing links are stale (the user mentioned they have broken `.claude/commands` and `.claude/skills` symlinks in this repo in the reviewer-context), adopt-existing will leave them stale. That's the exact failure mode the task is trying to repair, and the installer helps perpetuate it.
- **Installer confidence hit** — the installer is the most visible trust artifact for first-time beta users. Silent broken-symlink output undermines trust even when the actual files are recoverable by re-running `nanite install` with no `--project`.

## Recommendation

Add two checks:

1. **Verify `globalHome` exists and is a directory** at the top of `ScaffoldNaniteDir`. If it doesn't, return a specific error telling the user to run `nanite install` without `--project` first.

   ```go
   func ScaffoldNaniteDir(projectDir, globalHome string, src ScaffoldSource) error {
       if info, err := os.Stat(globalHome); err != nil || !info.IsDir() {
           return fmt.Errorf("global nanite home %s is missing or not a directory — run `nanite install` (no --project) first: %w", globalHome, err)
       }
       ...
   }
   ```

2. **Verify each link target exists** before `os.Symlink`. If the target is missing, that's either a partially-populated global home (the user needs to re-run `nanite install --refresh`) or a globalHome typo. Either way, fail loud.

   ```go
   for _, sub := range []string{"roles", "skills", "commands"} {
       link := filepath.Join(naniteDir, sub)
       target := filepath.Join(globalHome, sub)
       if _, err := os.Stat(target); err != nil {
           return fmt.Errorf("symlink target %s missing: %w", target, err)
       }
       if info, err := os.Lstat(link); err == nil {
           // Existing link — verify it still resolves to a live target.
           if info.Mode()&os.ModeSymlink != 0 {
               if _, err := os.Stat(link); err != nil {
                   return fmt.Errorf("existing symlink %s is dangling: %w", link, err)
               }
           }
           continue
       } else if !errors.Is(err, fs.ErrNotExist) {
           return fmt.Errorf("lstat %s: %w", link, err)
       }
       if err := os.Symlink(target, link); err != nil {
           return fmt.Errorf("symlink %s -> %s: %w", link, target, err)
       }
   }
   ```

   The "existing-symlink dangling check" is the change most relevant to Phase 4 Task 16: it converts the silent-broken-link state into a hard failure that tells the user to re-install. For adopt-existing runs, the installer should probably also offer a `--repair-symlinks` or equivalent flag that replaces dangling links. At minimum it should not silently skip them.

3. **Test coverage.** Add a test that sets `globalHome` to a non-existent directory and asserts `ScaffoldNaniteDir` returns an error. Add another test that creates a broken symlink at `{project}/.nanite/roles` and asserts adoptExisting either repairs it or fails loud — currently it does neither.

## References

- `internal/service/install/scaffold.go:L31-76` — the `ScaffoldNaniteDir` implementation.
- `internal/service/install/install.go:L116-123` — `globalHome` defaulting with no existence check.
- `internal/service/install/adopt.go:L25-45` — adopt-existing flow calling the same scaffold helper.
- `internal/service/install/scaffold_test.go` — the test file that would need the new cases.
- Reviewer-context note: "Broken `.claude/commands` and `.claude/skills` symlinks in this repo — same fix" (reviewer-backend.md `Pre-existing known issues`, item 4). This finding is the technical mechanism behind that known issue.
