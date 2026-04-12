# [High] Artifact upload uses client-provided filename without sanitization

**Scope:** Artifact API
**Topic:** Security
**Date:** 2026-04-11

## Problem

`handleUploadArtifact` uses `header.Filename` (from the multipart form) directly as the storage filename and in the `Content-Disposition` header on download. The filename is not sanitized against path traversal characters or special characters.

## Evidence

`internal/api/artifacts.go:L49-121` — upload handler:

```go
file, header, err := r.FormFile("file")
// ...
storageDir := filepath.Join("data", "artifacts", sessionID)
if err := os.MkdirAll(storageDir, 0o755); err != nil {
    // ...
}
storagePath := filepath.Join(storageDir, header.Filename)  // User-controlled filename
dst, err := os.Create(storagePath)
// ...
```

`internal/api/artifacts.go:L44-46` — download handler:

```go
w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, artifact.Name))
```

**Attack vectors:**

1. **Path traversal via filename.** A multipart upload with `filename="../../../etc/cron.d/evil"` writes outside `data/artifacts/`. `filepath.Join` does NOT prevent this — `filepath.Join("data", "artifacts", "sess1", "../../../etc/cron.d/evil")` resolves to `etc/cron.d/evil`.
2. **Content-Disposition header injection.** A filename containing `"` or newlines can break out of the `Content-Disposition` header value, potentially injecting additional HTTP headers (response splitting).

## Impact

1. **Arbitrary file write** on the server filesystem via path-traversed filenames. An attacker can overwrite configuration files, cron entries, or inject code into the plugins directory.
2. **Response header injection** via crafted filenames in the Content-Disposition header.

## Recommendation

1. Sanitize the filename before using it as a storage path:

```go
safeName := filepath.Base(header.Filename)
if safeName == "." || safeName == "/" || safeName == "" {
    safeName = "upload"
}
storagePath := filepath.Join(storageDir, safeName)
// Verify the resolved path stays under storageDir
if !strings.HasPrefix(filepath.Clean(storagePath), filepath.Clean(storageDir)+string(filepath.Separator)) {
    // reject
}
```

2. Sanitize the filename in Content-Disposition by stripping or escaping `"`, `\n`, `\r`.

## References

- CWE-22: Improper Limitation of a Pathname to a Restricted Directory
- CWE-113: Improper Neutralization of CRLF Sequences in HTTP Headers
- Related: `06-high-artifact-download-path-traversal.md` (download side of the same attack surface)
