# [High] Installer follows symlinks when reading/writing adapter target files and `.nanite/config.yaml`

**Scope:** installer / managed-section writers / adapter persist
**Topic:** Security — symlink TOCTOU / redirection
**Date:** 2026-04-10

## Problem

Every file the installer reads or writes in the target project — `CLAUDE.md`, `AGENTS.md`, `GEMINI.md`, `OPENCODE.md`, `.nanite/config.yaml`, `NANITE.md`, and the `pre-edit` snapshots in the archive directory — is accessed with `os.ReadFile` / `os.WriteFile`. Both functions follow symlinks all the way to their final target. There is no `os.Lstat` pre-check, no `O_NOFOLLOW`, and no enforcement that the destination must be a regular file inside the project tree.

Combined with the fact that the installer accepts an arbitrary `--project` path and the project tree is writable by any process running as the same user, this means:

1. If an attacker (or a buggy previous install, or a careless hand-edit) leaves a symlink at, say, `CLAUDE.md -> /etc/shadow`, the installer's `UpdateCLAUDEmd` will read `/etc/shadow`, write a "cleaned" version with a managed section appended, and `os.WriteFile` it back to `/etc/shadow`. This fails in most real attacker scenarios because of file ownership, but it is still a guaranteed information-disclosure / arbitrary-write primitive when the target happens to be user-owned (cron configs, shell rc files, dotfiles, private keys).
2. `.nanite/config.yaml` as a symlink to an arbitrary yaml-parseable file lets `persistAdapterList` rewrite that file with a mangled adapter key, potentially corrupting unrelated config.
3. Pre-edit snapshots under the archive directory (`archiveDir/CLAUDE.md.pre-edit`) are written with `os.WriteFile`, so if an attacker plants a symlink there before `snapshotAdapterTargets` runs, the victim's CLAUDE.md content is exfiltrated to the symlink target.

The install service does perform `filepath.EvalSymlinks` on `projectDir` itself (`internal/service/install/install.go:L112-114`) so the project root cannot be a symlink to elsewhere, but it does not recursively normalize or lstat-check any child path it touches.

## Evidence

### Managed-section writer (adapter target files)

```go
// internal/service/install/claudemd.go:L93-107
func UpdateCLAUDEmd(path string, managedContent string, snap *CLAUDESnapshotOpts) (*CLAUDEUpdateReport, error) {
    report := &CLAUDEUpdateReport{}

    existing, err := os.ReadFile(path)          // follows symlinks
    if os.IsNotExist(err) {
        ...
    }
    ...
    // later:
    if err := os.WriteFile(path, []byte(cleaned), 0o644); err != nil {
        return nil, fmt.Errorf("write cleaned CLAUDE.md: %w", err)
    }
    ...
    if err := agent.WriteManagedSection(path, managedContent); err != nil {
```

The `agent.WriteManagedSection` helper (`internal/agent/managed_section.go:L28-78`) also uses `os.ReadFile` / `os.WriteFile` with no Lstat:

```go
// internal/agent/managed_section.go:L28-48
func WriteManagedSection(path string, content string) error {
    block := buildManagedBlock(content)

    data, err := os.ReadFile(path)            // follows symlinks
    if os.IsNotExist(err) {
        return os.WriteFile(path, []byte(block), 0o644)
    }
    ...
```

### Pre-edit snapshot writer

```go
// internal/service/install/adapters.go:L113-129
func snapshotAdapterTargets(projectDir, archiveDir string) error {
    for _, name := range adapterTargetFiles {
        src := filepath.Join(projectDir, name)
        data, err := os.ReadFile(src)         // follows symlinks from project side
        if errors.Is(err, fs.ErrNotExist) {
            continue
        }
        if err != nil {
            return fmt.Errorf("read %s for snapshot: %w", src, err)
        }
        dst := filepath.Join(archiveDir, name+".pre-edit")
        if err := os.WriteFile(dst, data, 0o644); err != nil {   // follows symlinks from archive side
            return fmt.Errorf("write snapshot %s: %w", dst, err)
        }
    }
    return nil
}
```

### Adapter cleanup

```go
// internal/service/install/adapter_cleanup.go:L41-56
path := filepath.Join(projectDir, evidence.rootFile)
stripped, becameEmpty, err := agent.RemoveManagedSection(path)   // uses os.ReadFile / os.WriteFile
...
if becameEmpty {
    if err := os.Remove(path); err != nil {
        return reports, fmt.Errorf("delete empty %s: %w", path, err)
    }
    report.Action = "deleted"
}
```

`os.Remove` on a symlink removes the link (not the target), which is safe here. But the `RemoveManagedSection` call above still reads and writes the symlink target before the remove can run, so the write-to-target race happens first.

### Project config writer

```go
// internal/service/install/adapter_persist.go:L44-50
func persistAdapterList(path string, adapters []string) error {
    data, err := os.ReadFile(path)            // follows symlink
    if err != nil {
        return fmt.Errorf("read config %s: %w", path, err)
    }

    var root yaml.Node
    if err := yaml.Unmarshal(data, &root); err != nil {
```

`.nanite/config.yaml` is assumed to be a regular file. If `.nanite/` is a dir the user or a prior install placed, there is no guarantee the config file inside it is.

## Impact

- **Who:** any Nanite user with a project tree that has been manipulated by another user, a buggy previous tool, or a restored backup with preserved symlinks. In multi-user dev environments (shared CI workers, hosting boxes, containerized CI runners with persistent volumes) the threat is real.
- **What:** the installer can be coerced into (a) reading privileged file contents into memory and then back out to an attacker-reachable path via the archive pre-edit snapshot, and (b) overwriting arbitrary user-owned files with managed-section content. Severity of (a) is worse than (b) because it works even if the target is technically writable: the attacker gets to exfiltrate, not just trash.
- **Preconditions:** write access to either the project tree or the archive base dir by the attacker. Root is not required.
- **Blast radius:** limited to files the running user can read/write, but the managed-section format is a stable HTML-comment-bracketed block, so the attacker can distinguish injected content from untouched content.

## Recommendation

Treat every filesystem operation inside the installer as `O_NOFOLLOW`-equivalent. Concrete changes:

1. **Add a helper in the install package** that wraps reads and writes with `Lstat` + `FileMode().Type() == 0` (regular file) checks, refusing to proceed if the path is a symlink. Plumb it into `UpdateCLAUDEmd`, `snapshotAdapterTargets`, `persistAdapterList`, `RemoveManagedSection`, and the freshScaffold NANITE.md/config.yaml writes.

   ```go
   // internal/service/install/safefs.go (new file, sketch)
   func safeReadFile(path string) ([]byte, error) {
       info, err := os.Lstat(path)
       if err != nil {
           return nil, err
       }
       if info.Mode()&os.ModeSymlink != 0 {
           return nil, fmt.Errorf("refusing to follow symlink: %s", path)
       }
       return os.ReadFile(path)
   }

   func safeWriteFile(path string, data []byte, perm os.FileMode) error {
       info, err := os.Lstat(path)
       if err == nil && info.Mode()&os.ModeSymlink != 0 {
           return fmt.Errorf("refusing to overwrite symlink: %s", path)
       }
       return os.WriteFile(path, data, perm)
   }
   ```

2. **Validate `.nanite/` itself** during adopt-existing and fresh scaffold: `os.Lstat(filepath.Join(projectDir, ".nanite"))` must return a regular directory, not a symlink. Currently `dirExists` (`install.go:L265-268`) uses `os.Stat` which follows symlinks and returns true for a symlink-to-dir.

3. **Archive pre-edit writes** (`snapshotAdapterTargets`) need the same protection for `dst`. The archive base dir is user-writable by contract; there is no reason to let a pre-existing symlink in the archive folder redirect the snapshot.

4. **Document the trust model** in the install package comment: state explicitly that the installer assumes the project tree and archive base are not adversarially manipulated, and call out the specific files it guarantees are regular files.

Alternative (partial mitigation): use `os.OpenFile` with `O_NOFOLLOW` (on platforms that support it) instead of `os.WriteFile`. This catches writes but not reads, and it is not portable to Windows. Recommended as a belt-and-suspenders on top of the Lstat wrappers, not as a replacement.

## References

- `internal/agent/managed_section.go:L28-184` — the managed-section primitives used by every adapter.
- `internal/service/install/adapter_persist.go:L19-94` — YAML round-trip through `os.ReadFile` / `os.WriteFile`.
- `internal/service/install/install.go:L112-114` — project-root symlink resolution that does NOT recurse.
- Related: `03-high-managed-section-end-marker-confusion.md` (same parser, different vector).
- Go stdlib `os` package docs: `ReadFile` / `WriteFile` follow symlinks by design.
