# [High] npm transitive dependencies have active CVEs (flatted, picomatch, brace-expansion)

**Scope:** ui/package-lock.json — frontend transitive dependencies
**Topic:** Security — CVEs
**Date:** 2026-04-11

## Problem

Three transitive npm dependencies have disclosed vulnerabilities: `flatted` (prototype pollution + DoS), `picomatch` (ReDoS + method injection), `brace-expansion` (DoS via zero-step sequences).

## Evidence

Command: `npm audit` in `ui/`

**flatted <=3.4.1** (High)
- GHSA-25h7-pfq9-p65f — Unbounded recursion DoS in `parse()` revive phase (CVSS 7.5)
- GHSA-rf6f-7fwh-wjgh — Prototype Pollution via `parse()`

**picomatch 4.0.0-4.0.3** (High)
- GHSA-3v7f-55p6-f55p — Method Injection in POSIX Character Classes (CVSS 5.3)
- GHSA-c2c7-rcm5-vvqj — ReDoS via extglob quantifiers (CVSS 7.5)

**brace-expansion <1.1.13 || >=4.0.0 <5.0.5** (Moderate)
- GHSA-f886-m6hf-6m8v — Zero-step sequence causes process hang and memory exhaustion (CVSS 6.5)

All are indirect dependencies pulled in by the devDependency tree (`eslint`, `typescript-eslint`). `flatted` is a dep of `eslint`'s file caching. `picomatch` is used by glob matching in the build toolchain. `brace-expansion` is used by `minimatch` in eslint and typescript-eslint.

## Impact

These are build-time/lint-time dependencies — they do not ship in the production frontend bundle. The prototype pollution in `flatted` and the ReDoS in `picomatch` could be triggered if a developer processes maliciously-crafted lint cache files or glob patterns during development, but the practical risk in this project is low.

The `flatted` prototype pollution (GHSA-rf6f-7fwh-wjgh) is the highest concern: if eslint's flat cache file is tampered with, `flatted.parse()` could pollute `Object.prototype`. In a CI/CD context where cache files might come from untrusted sources, this could be exploitable.

## Recommendation

Run `npm audit fix` in `ui/`. All four vulnerabilities report `fix available`. If audit fix bumps a major version, verify `npm run lint` and `npm run build` still pass.

## References

- https://github.com/advisories/GHSA-25h7-pfq9-p65f
- https://github.com/advisories/GHSA-rf6f-7fwh-wjgh
- https://github.com/advisories/GHSA-3v7f-55p6-f55p
- https://github.com/advisories/GHSA-c2c7-rcm5-vvqj
- https://github.com/advisories/GHSA-f886-m6hf-6m8v
