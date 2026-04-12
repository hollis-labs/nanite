# [High] Artifact download serves any file path stored in DB without confinement

**Scope:** Artifact API
**Topic:** Security
**Date:** 2026-04-11

## Problem

`handleDownloadArtifact` reads the `StoragePath` from the database and opens it directly with `os.Open` — no path validation, no confinement to a data directory. The `handlePlaceArtifact` endpoint accepts a user-provided `storage_path` and stores it in the database. Together, these form a two-step arbitrary file read.

## Evidence

`internal/api/artifacts.go:L28-47` — download serves any DB-stored path:

```go
func (a *API) handleDownloadArtifact(w http.ResponseWriter, r *http.Request) {
    id := r.PathValue("id")

    artifact, err := a.Services.Store.GetArtifact(id)
    if err != nil {
        a.errorResp(w, http.StatusNotFound, "artifact not found")
        return
    }

    f, err := os.Open(artifact.StoragePath)
    // ... serves the file
}
```

`internal/api/artifacts.go:L126-172` — place endpoint accepts arbitrary `storage_path`:

```go
func (a *API) handlePlaceArtifact(w http.ResponseWriter, r *http.Request) {
    var req PlaceArtifactRequest
    // ...
    if req.SessionID == "" || req.Name == "" || req.StoragePath == "" {
        // ...
    }
    // ...
    artifact := &store.Artifact{
        // ...
        StoragePath: req.StoragePath,  // User-controlled, stored directly
        // ...
    }
    if err := a.Services.Store.CreateArtifact(artifact); err != nil {
        // ...
    }
    // ...
}
```

**Attack chain:**
1. `POST /api/artifacts/place` with `storage_path: "/etc/passwd"` (or any sensitive file).
2. Response includes the new artifact's `id`.
3. `GET /api/artifacts/{id}/download` reads and serves `/etc/passwd`.

## Impact

Arbitrary file read on the host filesystem. An attacker can read:
- Environment files (`.env`, credentials)
- SSH keys, API key files
- The SQLite database itself
- Any file readable by the Nanite process

## Recommendation

1. **Validate `storage_path` in `handlePlaceArtifact`.** Require the path to be under a known data directory (e.g., `data/artifacts/`). Reject absolute paths that don't start with the expected prefix.
2. **Validate in `handleDownloadArtifact`.** Before `os.Open`, verify the `StoragePath` is under the artifact data directory.
3. Consider whether `handlePlaceArtifact` should accept a path at all, or whether it should only accept references to files that were previously uploaded.

```go
func (a *API) handlePlaceArtifact(w http.ResponseWriter, r *http.Request) {
    // ...
    cleanPath := filepath.Clean(req.StoragePath)
    if !strings.HasPrefix(cleanPath, filepath.Clean(a.artifactDataDir)+string(filepath.Separator)) {
        a.errorResp(w, http.StatusBadRequest, "storage_path must be under the artifact data directory")
        return
    }
    // ...
}
```

## References

- CWE-22: Improper Limitation of a Pathname to a Restricted Directory
- `handleUploadArtifact` (same file) correctly writes to `data/artifacts/{sessionID}/` — the upload path is safe, but the place + download path is not.
- Cross-ref: `dev-tools-input-validation` audit found path traversal in dev tools; this is the HTTP API-side equivalent.
