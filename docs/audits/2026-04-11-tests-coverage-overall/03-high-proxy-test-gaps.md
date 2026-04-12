# [High] Proxy tests missing DNS rebinding, Slowloris, and port restriction coverage

**Scope:** internal/sandbox/
**Topic:** Test Quality — security-critical proxy gaps
**Date:** 2026-04-11

## Problem

The sandbox proxy test suite (`internal/sandbox/proxy_test.go`, 245 lines) covers domain allowlisting, CONNECT tunneling, wildcard matching, and cleanup. It does not test three attack vectors identified by the `sandbox-hardening` audit as real findings.

## Evidence

`internal/sandbox/proxy_test.go` tests:
- `TestProxy_AllowedHTTP` — allowed domain passes through
- `TestProxy_DeniedHTTP` — denied domain returns 403
- `TestProxy_CONNECTAllowed` — TLS CONNECT for allowed domain
- `TestProxy_CONNECTDenied` — TLS CONNECT for denied domain returns 403
- `TestProxy_WildcardMatching` — 10-case table for domain pattern matching
- `TestProxy_StopCleansUp` — listener closed after Stop
- `TestProxy_MissingHost` — non-proxy request returns 400

Missing test coverage for findings from `2026-04-10-sandbox-hardening`:

1. **DNS rebinding:** No test verifies that a domain resolving to `127.0.0.1` after the allowlist check is blocked. The proxy checks the domain name against the allowlist but does not verify the resolved IP. A DNS rebinding attack could bypass the allowlist by having a domain resolve to an internal address.

2. **Slowloris / ReadHeaderTimeout:** No test verifies that the proxy's HTTP server has `ReadHeaderTimeout` set. The golangci-lint sweep found G112 (Slowloris) at the proxy's `http.Server`. Without `ReadHeaderTimeout`, a slow client can hold connections open indefinitely.

3. **Port restrictions:** No test verifies that the proxy restricts outbound ports. The sandbox-hardening audit noted missing port restrictions — the proxy allows CONNECT to any port on an allowed domain.

## Impact

Each of these is a known attack vector against HTTP proxies used as security boundaries. The sandbox proxy is the primary network control for sandboxed subprocesses. Without test coverage, regressions in these areas will not be detected.

## Recommendation

Add three test functions to `proxy_test.go`:

1. `TestProxy_DNSRebindingBlocked` — use a custom DNS resolver or httptest server that resolves an "allowed" domain to 127.0.0.1, verify the proxy blocks the request or at minimum doesn't forward it to the loopback.
2. `TestProxy_SlowClientTimesOut` — connect to the proxy and send headers very slowly, verify the connection is terminated within a reasonable timeout (requires `ReadHeaderTimeout` to be set on the proxy's server).
3. `TestProxy_PortRestriction` — if port restrictions are implemented, verify CONNECT to non-standard ports (e.g., 22, 6379) is blocked even for allowed domains.

## References

- `2026-04-10-sandbox-hardening` — proxy SSRF via DNS-resolved IPs, missing port restrictions
- `2026-04-11-whole-repo-tooling-and-tests-sweep` — G112 (Slowloris) on proxy `http.Server`
- `internal/sandbox/proxy_test.go` — current test suite
