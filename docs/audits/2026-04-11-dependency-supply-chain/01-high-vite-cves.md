# [High] Vite dev server has 3 active CVEs (path traversal, fs.deny bypass, WebSocket file read)

**Scope:** ui/package.json — frontend build tooling
**Topic:** Security — CVEs
**Date:** 2026-04-11

## Problem

Vite v7.3.1 (direct devDependency) has three disclosed vulnerabilities, two rated High by the advisory source.

## Evidence

Command: `npm audit` in `ui/`

```
vite  7.0.0 - 7.3.1
Severity: high

GHSA-4w7w-66w2-5vf9  Path Traversal in Optimized Deps .map Handling  (CWE-22, CWE-200)
GHSA-v2wj-q39q-566r  server.fs.deny bypassed with queries            (CWE-180, CWE-284)
GHSA-p9ff-h696-f583  Arbitrary File Read via Dev Server WebSocket     (CWE-200, CWE-306)
```

All three affect the Vite **dev server** specifically:

- `ui/package.json:61` — `"vite": "^7.3.1"` (pinned spec allows semver patch bumps but the lockfile resolves to 7.3.1 which is in the vulnerable range)

Fix available via `npm audit fix`.

## Impact

These are dev-server vulnerabilities. They are exploitable when `vite dev` is running and the dev server port is reachable. In production the Go binary serves the embedded SPA — Vite is not involved. The risk is limited to developer machines during local development, but an attacker on the same network (or via DNS rebinding) could read arbitrary files from the developer's machine through the Vite WebSocket or `server.fs.deny` bypass.

Because nanite developers routinely run `vite dev` with `--dev` mode on their local machines, this is a realistic attack surface during development.

## Recommendation

Run `npm audit fix` in `ui/` to pull the patched Vite version. If no patched v7.x exists yet, pin to the latest available and track the upstream fix. The `^7.3.1` semver range will auto-resolve once a patch is published.

## References

- https://github.com/advisories/GHSA-4w7w-66w2-5vf9
- https://github.com/advisories/GHSA-v2wj-q39q-566r
- https://github.com/advisories/GHSA-p9ff-h696-f583
