# [Medium] Catalog install allows unsigned plugins from signed sources with only a log warning

**Scope:** Plugin catalog API
**Topic:** Security
**Date:** 2026-04-11

## Problem

When a catalog source has a trusted public key configured but the plugin entry in the catalog has no signature, the install proceeds with only a `log.Printf` warning. The user is not informed that signature verification was skipped.

## Evidence

`internal/api/catalog.go:L345-353`:

```go
if sourcePublicKey != "" && entry.Signature != "" {
    if err := naniteplugin.VerifySignature(tmpPath, sourcePublicKey, entry.Signature); err != nil {
        cs.errorResp(w, http.StatusBadRequest, fmt.Sprintf("signature verification failed: %v", err))
        return
    }
} else if sourcePublicKey != "" && entry.Signature == "" {
    // Source has a key but plugin is unsigned — warn but allow.
    log.Printf("catalog: WARNING plugin %q from %s is unsigned (source has a trusted key)", entry.Name, entry.SourceName)
}
```

## Impact

A compromised or malicious catalog source can serve unsigned plugins that bypass signature verification. The user configured a public key specifically to enforce integrity — silently allowing unsigned plugins defeats that intent. An attacker who can tamper with the catalog JSON (MITM, compromised CDN) can strip the signature field and deliver a modified archive.

## Recommendation

When a source has a public key configured and the plugin is unsigned, reject the install by default. Add an explicit override parameter (e.g., `"allow_unsigned": true` in the request body) that the user must opt into.

```go
} else if sourcePublicKey != "" && entry.Signature == "" {
    cs.errorResp(w, http.StatusBadRequest,
        fmt.Sprintf("plugin %q is unsigned but source %s has a trusted key; refusing install", entry.Name, entry.SourceName))
    return
}
```

## References

- The catalog system has good security infrastructure (checksum verification, Ed25519 signature verification, source key management). This is a policy gap in an otherwise well-designed system.
- `handleInstall` (git clone) and `handleInstallLocal` (local path) have no signature verification at all, which is a separate concern documented in `03-critical-plugin-install-local-path-traversal.md`.
