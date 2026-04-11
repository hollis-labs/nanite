# [Medium] `carryOverFromArchive` reads symlinks from `.agentrc/` and writes their targets into `.nanite/`

**Scope:** installer / migrate-from-agentrc flow
**Topic:** Security — trust boundary between user-editable `.agentrc/` and installer-managed `.nanite/`
**Date:** 2026-04-10

## Problem

During `--migrate-from-agentrc`, `carryOverFromArchive` copies `.agentrc/agents/*`, `.agentrc/boot-prompt.md`, and `.agentrc/config.yaml` into the newly-scaffolded `.nanite/` directory using `filepath.Walk` + `os.ReadFile`. `filepath.Walk` does not follow symlinked directories (it only walks the link itself), but `os.ReadFile` follows symlinked files. So if the user's `.agentrc/agents/foo.md` is actually a symlink to `/etc/passwd` or to any file outside the project, `copyDir` will read the target's contents and write them to `.nanite/agents/foo.md`.

The file is then:

1. Visible to every subsequent agent session (the file lives under the project's `.nanite/` and is loaded by adapters as context content).
2. Picked up by `adapter-nanite-native` in `Load()` via `readProjectConfigWithFallback` and composed into the system prompt for matching agents, landing inside LLM context.
3. Exposed to any adapter plugin that reads `.nanite/agents/` for discovery.

The threat model: if the `.agentrc/` directory was constructed by an adversary (e.g., the user cloned a git repo that contained `.agentrc/agents/backend.md -> /etc/shadow` as a tracked symlink), the migration silently exfiltrates the victim's file contents into their project's `.nanite/` and into every LLM prompt composed from it.

This matches the symlink-following pattern flagged in finding `02-high-symlink-following-on-adapter-target-files.md` but lives in a different code path (the `copyDir` walker) and has a slightly different impact because the exfiltration lands in LLM prompts, not just in local files.

## Evidence

```go
// internal/service/install/migrate.go:L228-253
func copyDir(src, dst string) error {
    return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
        if err != nil {
            return err
        }
        rel, err := filepath.Rel(src, path)
        if err != nil {
            return err
        }
        target := filepath.Join(dst, rel)
        if info.IsDir() {
            return os.MkdirAll(target, info.Mode())
        }
        data, err := os.ReadFile(path)     // follows symlinks — reads the target's content
        if err != nil {
            return fmt.Errorf("read %s: %w", path, err)
        }
        if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
            return fmt.Errorf("mkdir %s: %w", filepath.Dir(target), err)
        }
        if err := os.WriteFile(target, data, info.Mode()); err != nil {
            return fmt.Errorf("write %s: %w", target, err)
        }
        return nil
    })
}
```

Note `info.Mode()` is from `filepath.Walk`'s `info`, which is the result of `os.Lstat` — so for a symlink, `info.Mode()` reports `os.ModeSymlink`, and `info.IsDir()` returns false. The walker would therefore fall through to the `os.ReadFile` branch on a symlinked file. `os.ReadFile` follows the link to the target and returns the target's content. The `info.Mode()` passed to `os.WriteFile` still has the symlink bits set, which is harmless but technically wrong.

Callers:

```go
// internal/service/install/migrate.go:L188-225 (carryOverFromArchive)
srcAgents := filepath.Join(srcAgentrc, "agents")
if _, err := os.Stat(srcAgents); err == nil {
    if err := copyDir(srcAgents, filepath.Join(dstNanite, "agents")); err != nil {
        return fmt.Errorf("copy agents: %w", err)
    }
}
...
if data, err := os.ReadFile(filepath.Join(srcAgentrc, "boot-prompt.md")); err == nil {
    if err := os.WriteFile(filepath.Join(dstNanite, "boot-prompt.md"), data, 0o644); err != nil {
        return fmt.Errorf("copy boot-prompt: %w", err)
    }
}
...
if data, err := os.ReadFile(filepath.Join(srcAgentrc, "config.yaml")); err == nil {
    renamed := renameConfigFields(data)
    if err := os.WriteFile(filepath.Join(dstNanite, "config.yaml"), renamed, 0o644); err != nil {
        return fmt.Errorf("copy config: %w", err)
    }
}
```

Every one of these paths uses `os.ReadFile`, which follows symlinks.

### Walker caveat

`filepath.Walk` with a symlinked *directory* at the root will walk only the link, not its contents — the symlinked dir is reported once and then the walker moves on. So a symlinked `.agentrc/agents` would copy the single "file" (as a read of the symlink's content, which would fail because `os.ReadFile` on a symlink to a directory returns an error). That's a correctness issue but not an exfiltration vector.

Symlinked *files* inside `.agentrc/agents/` are the real issue. Those are visited as regular files, and their content is read from wherever the link points.

## Impact

- **Who:** any user migrating a project whose `.agentrc/` was constructed by another person (clone from a teammate, archive restore, checked-in symlinks in a fork).
- **What:** arbitrary file content owned by the running user is copied into `.nanite/` and subsequently loaded into LLM context. If the LLM is a hosted provider, the exfiltrated content is transmitted off-machine.
- **Preconditions:** attacker-controlled `.agentrc/` directory contents. Migration is a one-time operation, so the window is narrow, but the outcome is data exposure to a third party (the LLM provider), which is categorically worse than local file trashing.
- **Blast radius:** scoped to files the running user can read. `/etc/shadow` on Linux requires root, so a dev-user victim cannot be exploited for that specific file — but `~/.ssh/id_rsa`, `~/.aws/credentials`, `~/.config/gh/hosts.yml`, and every API-key-bearing dotfile are all fair game.

## Recommendation

Replace `os.ReadFile` in `copyDir` (and the two direct `os.ReadFile` calls in `carryOverFromArchive`) with an `os.Lstat`-gated read that refuses to follow symlinks. Same pattern as finding 02's `safeReadFile`:

```go
// Inside copyDir's walker callback:
if info.Mode()&os.ModeSymlink != 0 {
    // Log and skip. Or optionally: re-create as a symlink in the dst tree,
    // preserving user intent without reading target content.
    return nil
}
data, err := os.ReadFile(path)
```

For the two direct reads (`boot-prompt.md`, `config.yaml`), the same wrapper applies. The walker skip is the safer default because it doesn't silently hide the user's original content — but the installer should probably print a notice to the user that a symlink was skipped, so they can investigate.

Alternative: re-create symlinks in `.nanite/` by calling `os.Readlink` + `os.Symlink`, preserving the user's intent without reading the target content. This is probably what the user expected, but it introduces a new problem: the resulting `.nanite/agents/foo.md` symlink now points outside `.nanite/`, and every adapter that reads it will still follow the link and transmit the content to the LLM. So the same exfiltration vector exists, just moved one layer deeper.

Recommended: **skip-and-warn** is the right default. If a user needs a symlinked agent context file to survive migration, they can manually copy the target content after running migrate. That's a documented limitation, not a silent data exposure.

Test additions:

- Place a symlinked file in `.agentrc/agents/` pointing at a marker file outside the project.
- Run migrate.
- Assert the resulting `.nanite/agents/` does not contain the marker file's contents.

## References

- `internal/service/install/migrate.go:L183-253` — `carryOverFromArchive` + `copyDir`.
- Related finding: `02-high-symlink-following-on-adapter-target-files.md` — same class of issue in the `CLAUDE.md` write path.
- Go stdlib: `filepath.Walk` documentation explicitly warns that it does not follow symlinks; `os.ReadFile` documentation does not.
