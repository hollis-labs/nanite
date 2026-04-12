# [Critical] Plugin install-local accepts arbitrary filesystem paths with no confinement

**Scope:** Plugin management API
**Topic:** Security
**Date:** 2026-04-11

## Problem

`POST /api/plugins/install-local` accepts a `path` field pointing to any directory on the filesystem. It reads `plugin.yaml` from that directory, then copies the entire directory tree into the plugins directory and hot-loads the plugin into the running host. There is no confinement of the source path.

## Evidence

`internal/api/plugins.go:L257-307`

```go
func (pms *pluginManagerState) handleInstallLocal(w http.ResponseWriter, r *http.Request) {
    var req InstallLocalRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Path == "" {
        pms.errorResp(w, http.StatusBadRequest, "path is required")
        return
    }

    // Resolve and validate source path.
    srcDir, err := filepath.Abs(req.Path)
    // ...
    srcManifest := filepath.Join(srcDir, "plugin.yaml")
    if !fileExists(srcManifest) {
        // ...
    }
    manifest, err := naniteplugin.ParseManifest(srcManifest)
    // ...
    target := filepath.Join(pms.pluginsDir, manifest.Name)
    // ...
    if err := copyDir(srcDir, target); err != nil {
        // ...
    }
    // Hot-load into running host.
    pms.runPluginLoadIntoHost(filepath.Join(target, "plugin.yaml"), target)
    // ...
}
```

Additionally, `handleInstall` (git clone) at L200-251 constructs a git URL from the user-provided `name` field. If the name contains path separator characters or shell metacharacters, the `git clone` command receives them. The fallback URL construction is:

```go
repoURL = fmt.Sprintf("git@github.com:hollis-labs/%s.git", req.Name)
```

While `exec.Command("git", "clone", ...)` passes arguments as a list (no shell injection), the `req.Name` value also determines the filesystem target path:

```go
target := filepath.Join(pms.pluginsDir, req.Name)
```

A `req.Name` of `../../etc/cron.d/evil` would write outside the plugins directory. The `handleInstall` does not validate `req.Name` against path traversal.

## Impact

- **Arbitrary code execution.** An attacker with API access can point `install-local` at any directory containing a crafted `plugin.yaml` + Go plugin binary (or subprocess entrypoint). The plugin runs in-process with the Nanite host.
- **File read via copyDir.** Even without a valid plugin, the `copyDir` operation copies all files from the source directory to `plugins/<manifest.Name>/`, exfiltrating file contents to a known location.
- **Path traversal in `handleInstall`.** A crafted `name` field writes cloned repo contents outside the plugins directory.

## Recommendation

1. **install-local:** Restrict `req.Path` to an allowlist of directories (e.g., the user's plugin development directory) or require the path to be under the plugins directory itself. At minimum, refuse absolute paths outside of `$HOME`.
2. **install (git clone):** Validate `req.Name` against a strict regex (`^[a-zA-Z0-9_-]+$`). Reject any name containing `/`, `..`, or path separators.
3. **install-archive:** Already has zip-slip guards but shares the `manifest.Name` as target directory name — apply the same regex validation to `manifest.Name` after parsing.
4. **Request body limits:** None of the three install endpoints use `http.MaxBytesReader`. Add limits.
5. **No signature verification on install-local.** This is explicitly documented ("local trust model") — acceptable for now, but worth noting.

## References

- `internal/api/plugins.go:L200-251` — `handleInstall` (git clone path)
- `internal/api/plugins.go:L310-417` — `handleInstallArchive` (has zip-slip guards, good)
- `internal/api/catalog.go:L248-421` — `handleCatalogInstall` (has checksum + signature verification, good)
- Cross-ref: `01-critical-cors-origin-reflection.md` — combined with CORS, a remote site can install plugins.
