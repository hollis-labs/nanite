# [Medium] Catalog install allows unsigned plugins from signed sources with only a log warning

**Scope:** Plugin system — trust boundary #5 (Plugin manifests)
**Topic:** Security
**Date:** 2026-04-11

## Problem

When a catalog source has a trusted public key configured but a plugin entry in that catalog has no signature, the catalog install handler logs a warning and proceeds with installation. This silently downgrades the security posture from "verified" to "unverified" without user consent.

## Evidence

`internal/api/catalog.go:L345-L353`:

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

The warning goes to server logs only. The API response does not indicate the downgrade. The user/caller has no way to know the plugin was unsigned unless they check server logs.

## Impact

An attacker who compromises a catalog server can replace a signed plugin archive with an unsigned one. The signature check passes (because there is no signature to verify) and the plugin is installed. The user sees "installed" with no indication that the security guarantee was weakened.

This is a supply-chain attack vector. The checksum verification at `L338-L341` provides MITM protection but not origin authentication — the checksum comes from the same catalog server that the attacker controls.

## Recommendation

1. When a source has a public key, reject unsigned plugins from that source by default. Add an explicit `--allow-unsigned` flag or API parameter for override.
2. Include the signature status in the API response so the frontend can display a warning to the user.
3. Consider adding the signature status to the `catalogBrowseEntry` returned by the browse API so users can see verification status before installing.

## References

- `internal/api/catalog.go:L345-L353` — signature verification logic
- `internal/api/catalog.go:L337-L341` — checksum verification
- `internal/plugin/signature.go` — Ed25519 signature verification implementation
