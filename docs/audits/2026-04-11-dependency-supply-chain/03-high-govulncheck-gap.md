# [High] govulncheck is not installed — Go vulnerability scanning is absent

**Scope:** go.mod — Go dependency vulnerability scanning
**Topic:** Security — Tooling gap
**Date:** 2026-04-11

## Problem

`govulncheck` is not installed on this machine. There is no Go-side vulnerability scanner equivalent to `npm audit`. The Go module tree has 80+ transitive dependencies (including gRPC, protobuf, OTel, Badger, and SQLite CGo bindings) and none have been checked against the Go vulnerability database.

## Evidence

```
$ which govulncheck
govulncheck not found
```

The `reviewer-backend.md` context file lists `govulncheck ./...` as a required tool for security reviews. The `whole-repo-tooling-sweep` audit previously documented this gap as unresolved.

`go list -m -u all` (the fallback for checking updates) also fails because the phantom `github.com/hollis-labs/mcp-helpers` replace directive points to a module that doesn't exist on GitHub — the network lookup fails and aborts the entire command:

```
go: module github.com/hollis-labs/mcp-helpers: git ls-remote ... exit status 128:
    remote: Repository not found.
```

This means **neither** vulnerability scanning **nor** update checking works without manual intervention.

## Impact

Any CVE in a Go transitive dependency (e.g., gRPC, protobuf, or the SQLite C bindings) would go undetected. The Go vulnerability database is actively maintained and receives entries for stdlib and third-party modules.

The phantom `mcp-helpers` replace directive additionally blocks `go list -m -u all`, which is the standard way to check for available dependency updates.

## Recommendation

1. Install govulncheck: `go install golang.org/x/vuln/cmd/govulncheck@latest`
2. Run `govulncheck ./...` in the nanite repo and address any findings
3. Add govulncheck to the CI pipeline or pre-commit checks
4. Remove the phantom `mcp-helpers` replace directive (see finding 04)

## References

- https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck
- https://vuln.go.dev/
