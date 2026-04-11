# [Critical] `web_fetch` SSRF: no scheme restriction, no IP filter, no proxy, blind redirects

**Scope:** `internal/mcp/general_tools.go` — `web_fetch` MCP tool
**Topic:** Security — SSRF, trust boundary, outbound network
**Date:** 2026-04-10

## Problem

The `web_fetch` built-in tool accepts an arbitrary URL from LLM-generated tool arguments, hands it to `http.Client{Timeout: 10s}.Get(url)`, and returns up to 8000 characters of the response body to the LLM. The handler applies zero validation:

- No scheme allowlist (though Go's default transport limits to http/https in practice).
- No host allowlist or IP denylist — RFC1918 (`10/8`, `172.16/12`, `192.168/16`), loopback (`127/8`, `::1`), link-local (`169.254/16` — including AWS/GCP instance metadata at `169.254.169.254`), and IPv6 ULA (`fc00::/7`) are all reachable.
- No port restriction — `http://127.0.0.1:11434` (local Ollama), `http://127.0.0.1:5432` (local Postgres HTTP abuses), `http://localhost:6379` (local Redis via HTTP smuggling), all reachable.
- No redirect validation — the default `http.Client` follows up to 10 redirects to arbitrary hosts; an attacker-controlled public endpoint can 302 into `http://169.254.169.254/latest/meta-data/iam/security-credentials/...`.
- No DNS rebinding protection — the client resolves hostnames on each request and has no pinning.
- **No use of the sandbox network proxy.** `web_fetch` runs in the nanite host process, not inside a sandboxed child process, so `HTTP_PROXY` / `HTTPS_PROXY` are not set. The domain allowlist implemented in `internal/sandbox/proxy.go` is bypassed entirely. This is worse than the proxy bug the sandbox audit flagged as Critical finding 02 — at least that one *tried* to go through the proxy.

The 10-second client timeout is the only bound on the operation. The 8000-byte body limit controls only what returns to the LLM, not what reaches the target — all side effects (hitting an IMDS endpoint, triggering a webhook, exhausting an API quota) happen regardless of the byte limit.

## Evidence

Tool handler in full:

```go
// internal/mcp/general_tools.go:175-197
func (g *GeneralToolsTransport) callWebFetch(args map[string]any) (*ToolResult, error) {
    url, _ := args["url"].(string)
    if url == "" {
        return errorResult("url is required"), nil
    }

    client := &http.Client{Timeout: 10 * time.Second}
    resp, err := client.Get(url)
    if err != nil {
        return errorResult(fmt.Sprintf("fetch error: %v", err)), nil
    }
    defer resp.Body.Close()

    // Read up to 8000 chars.
    limited := io.LimitReader(resp.Body, 8000)
    body, err := io.ReadAll(limited)
    if err != nil {
        return errorResult(fmt.Sprintf("read error: %v", err)), nil
    }

    result := fmt.Sprintf("Status: %d %s\n\n%s", resp.StatusCode, resp.Status, string(body))
    return textResult(result), nil
}
```

Registration makes the tool available by default in every chat, with no per-project gating:

```go
// cmd/nanite/main.go:402
mcpManager.AddServer("general", mcp.NewGeneralToolsTransport())
```

The sandbox's outbound proxy (`internal/sandbox/proxy.go`) is only wired into subprocess children via `HTTP_PROXY` env injection (`internal/sandbox/exec.go:L116`). The nanite host process itself has no such injection. Therefore `http.Client{}.Get(url)` from inside `callWebFetch` bypasses the allowlist entirely. Grep confirms: `HTTP_PROXY` does not appear in `internal/mcp/` anywhere.

### What the default `http.Client` allows

Go's `http.DefaultTransport` (which `http.Client{}` inherits when `Transport` is nil) does the following:
- `Proxy: ProxyFromEnvironment` — honors `HTTP_PROXY`/`HTTPS_PROXY` if set in the process environment, but nanite does not set them on its own process.
- `DialContext`: plain `net.Dialer` with no IP filter.
- `TLSClientConfig`: nil, so default cert verification applies (this part is correct).
- Follows up to 10 redirects automatically (`http.Client.CheckRedirect` defaults to `defaultCheckRedirect`).

In an AWS/GCP/Azure-hosted test environment this is immediate creds exfiltration against instance metadata. On a developer laptop it's a reach into every localhost service the user has running — Ollama, Redis, PostgreSQL's HTTP admin endpoints, Docker daemon if exposed, devcontainers, MCP HTTP transports, nanite's own server, etc.

### Cross-audit comparison with sandbox finding 02

The sandbox audit's Critical 02 (`02-critical-proxy-ssrf-rfc1918-and-port.md`) flagged:
1. Proxy honors hostname-based allowlist but resolves DNS inside the CONNECT handler, so a domain on the allowlist resolving to RFC1918 was accepted.
2. No port restriction.
3. No DNS rebinding pin.

`web_fetch` is *worse* on three axes:
1. There is no allowlist at all — not domain, not IP, nothing.
2. There is no proxy involved, so all the proxy's (broken) controls are irrelevant — they'd never run.
3. Redirects are followed blindly; the sandbox proxy at least has one checkpoint per CONNECT; `web_fetch` re-checks nothing on 302.

And it is *better* on exactly one axis:
1. The 8000-byte body limit caps what the LLM sees. An IMDS response is typically under 8000 bytes, so this does not help for the headline exploit.

## Impact

- **Who:** any caller who can deliver a URL to a `web_fetch` call via prompt injection. Same trust boundary as findings 01 and 02.
- **What:**
  - **Cloud:** read IMDS creds (`http://169.254.169.254/...`), then pivot to the cloud provider API.
  - **Localhost:** enumerate and read from every HTTP-speaking service on the developer's machine. Trigger state-changing endpoints on services that use GET for state changes (many REST services do).
  - **Internal network:** if the developer is on a VPN, reach internal services (`intranet.corp`, `git.internal`, `jenkins.internal`).
  - **Exfiltration:** 302 redirects let an attacker-controlled public site send nanite to any arbitrary URL, bypassing any client-side URL filter a naive reviewer might add.
  - **Abuse:** ping an attacker-controlled endpoint repeatedly (no rate limit, no per-tool counter) to amplify DDoS or to trigger billing on a third party.
- **Reproducibility:** deterministic. One tool call.
- **Blast radius:** higher on cloud-hosted nanite deployments than on the current "first beta for developer friends" use case. Still critical on the dev-workstation case because of localhost-service reachability.

## Recommendation

Four layers; apply 1–3 as the hard fix, 4 as defense in depth.

**1. Scheme allowlist.** Reject non-http/https upfront:

```go
parsed, err := url.Parse(rawURL)
if err != nil {
    return errorResult(fmt.Sprintf("invalid url: %v", err)), nil
}
if parsed.Scheme != "http" && parsed.Scheme != "https" {
    return errorResult(fmt.Sprintf("unsupported scheme %q (http/https only)", parsed.Scheme)), nil
}
```

**2. Custom dialer with IP filter.** This is the real fix. Build an `http.Transport` with a `DialContext` that resolves the hostname itself, checks every returned IP against a denylist, and uses the pinned IP for the dial (so DNS rebinding cannot swap the destination after the check):

```go
var denylist = []*net.IPNet{
    mustParseCIDR("127.0.0.0/8"),
    mustParseCIDR("169.254.0.0/16"),
    mustParseCIDR("10.0.0.0/8"),
    mustParseCIDR("172.16.0.0/12"),
    mustParseCIDR("192.168.0.0/16"),
    mustParseCIDR("0.0.0.0/8"),
    mustParseCIDR("100.64.0.0/10"), // CGNAT
    mustParseCIDR("::1/128"),
    mustParseCIDR("fc00::/7"),
    mustParseCIDR("fe80::/10"),
}

func safeDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
    host, port, err := net.SplitHostPort(addr)
    if err != nil {
        return nil, err
    }
    ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
    if err != nil {
        return nil, err
    }
    for _, ip := range ips {
        for _, block := range denylist {
            if block.Contains(ip) {
                return nil, fmt.Errorf("blocked destination: %s", ip)
            }
        }
    }
    // Pin to first allowed IP to defeat DNS rebinding during the dial.
    pinned := ips[0].String()
    return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, net.JoinHostPort(pinned, port))
}
```

Unit-test this with the full IPv4 and IPv6 CIDR list.

**3. Redirect handling.** Install a `CheckRedirect` hook that re-runs the scheme + IP checks on every hop, or caps redirects at zero and forces the caller to handle redirects explicitly:

```go
client := &http.Client{
    Timeout: 10 * time.Second,
    Transport: transport,
    CheckRedirect: func(req *http.Request, via []*http.Request) error {
        if len(via) >= 5 {
            return fmt.Errorf("too many redirects")
        }
        return validateURL(req.URL) // scheme + IP checks again
    },
}
```

**4. Optional — route through the sandbox proxy.** The cleanest architectural fix is to make `web_fetch` use the same domain-allowlisted proxy that sandboxed subprocesses use. The reviewer-backend context lists this as an explicit design goal. Today that proxy has its own bugs (see sandbox finding 02) that must be fixed first, but once fixed, every outbound HTTP from nanite — host and sandbox — should go through one pinned chokepoint. Until then, 1–3 are the minimum.

**5. Body size and header policy.** Two small additions to the same change:
- Cap the body read at a byte count (say 256KB) before the 8000-char truncation that goes to the LLM, so large responses don't tie up kernel buffers for 10s.
- Do not return response headers to the LLM (`web_fetch` currently only returns status code + body — **correct** on this axis, logging as Info/praise; keep it that way).

Recommended severity: Critical. The tool is enabled by default, reaches any callable URL in the world, and has no defense against any of the standard SSRF attack classes.

## References

- `internal/mcp/general_tools.go:L175-L197` — the entire handler
- `cmd/nanite/main.go:L402` — default registration
- `internal/sandbox/proxy.go` — the proxy `web_fetch` should be using
- `internal/sandbox/exec.go:L116` — where `HTTP_PROXY` is injected (subprocess only)
- `docs/audits/2026-04-10-sandbox-hardening/02-critical-proxy-ssrf-rfc1918-and-port.md` — sibling finding in the proxy layer
- OWASP CWE-918 — SSRF
- AWS IMDSv1 (`169.254.169.254`) — the headline exploit target
- Go stdlib: `net.Dialer.DialContext`, `http.Transport.DialContext`, `http.Client.CheckRedirect`
- Related: finding `07-high-web-fetch-envelope-injection-from-response.md` — the same handler has a separate, compounding issue with returning unfiltered response text to the LLM
