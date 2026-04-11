# [Critical] Network proxy allowlist is bypassable via IP rebinding, RFC1918 targets, and arbitrary CONNECT ports

**Scope:** sandbox / network proxy
**Topic:** Security — SSRF, network allowlist bypass
**Date:** 2026-04-10

## Problem

The domain-allowlist proxy enforces its policy by matching against the hostname in the URL or `CONNECT` request line. It then hands the connection off to `net.DialTimeout` / `http.Client{}.Do`, both of which resolve the hostname **themselves** with the default resolver. The attacker controls the DNS for any domain they can get on the allowlist, and neither code path restricts the resulting IP or port.

This yields three distinct bypasses, each of which alone is Critical in the context of a sandbox that claims domain-level network isolation:

1. **DNS-based SSRF to RFC1918 / link-local / loopback targets** — any allowlisted domain whose DNS points at `127.0.0.1`, `169.254.169.254` (cloud metadata), `10.0.0.0/8`, or the host's own services.
2. **Arbitrary-port `CONNECT`** — the allowlist checks only the host, not the port. `CONNECT api.github.com:22` tunnels SSH, `:25` tunnels SMTP, `:2375` tunnels Docker, `:6379` tunnels Redis.
3. **IPv6 bypass via bracketed literals** — `splitHostPort` returns the host as the whole string if it contains no colon; for IPv6 literals like `[::1]:443`, the host passed to `domainAllowed` contains `[::1]` which no attacker-supplied allowlist pattern matches — but the underlying dial accepts the bracketed literal. More subtly, `[2001:db8::1]` *also* will not match any domain allowlist yet dial successfully; the check just fails closed — but combined with (1), an attacker whose allowed domain resolves to an IPv6 address that `splitHostPort` parses differently from `net.Dial` gets silently allowed.

Bypass 1 alone is enough to read cloud instance metadata, reach the user's local Redis/Postgres/Docker socket exposed on localhost, scan their LAN, or turn the proxy into a generic host-SSRF gadget. The "sandbox-first, denylist-second" design does not help here: the allowlist IS the first line for network, and it is porous.

## Evidence

`handleHTTP` and `handleConnect` both split the host and check the domain, then hand the raw string to the Go resolver:

```go
// internal/sandbox/proxy.go:82-101 (CONNECT)
func (p *Proxy) handleConnect(w http.ResponseWriter, r *http.Request) {
    host, _, err := splitHostPort(r.Host)
    if err != nil {
        http.Error(w, "bad host", http.StatusBadRequest)
        return
    }

    if !p.domainAllowed(host) {
        log.Printf("proxy: denied CONNECT to %s", r.Host)
        http.Error(w, "domain not allowed", http.StatusForbidden)
        return
    }

    // Dial the target.
    targetConn, err := net.DialTimeout("tcp", r.Host, 10*time.Second)
    ...
```

Note: `net.DialTimeout("tcp", r.Host, ...)` uses `r.Host`, not `host` — so if the client sends `CONNECT allowed.example.com:22`, the check passes (domain is allowed) and the dial targets port 22 without any port check.

```go
// internal/sandbox/proxy.go:134-186 (plain HTTP)
host, _, err := splitHostPort(r.URL.Host)
...
if !p.domainAllowed(host) {
    ...
}

outReq, err := http.NewRequestWithContext(r.Context(), r.Method, r.URL.String(), r.Body)
...
client := &http.Client{...}
resp, err := client.Do(outReq)
```

`client.Do(outReq)` resolves `outReq.URL.Host` via the default resolver. If `allowed.example.com` has an A record of `169.254.169.254`, the proxy forwards to EC2 metadata. If it has an A record of `127.0.0.1`, the proxy forwards to the host's localhost-bound services.

`splitHostPort` helper:

```go
// internal/sandbox/proxy.go:210-222
func splitHostPort(hostport string) (host, port string, err error) {
    host, port, err = net.SplitHostPort(hostport)
    if err != nil {
        if !strings.Contains(hostport, ":") {
            return hostport, "", nil
        }
        return "", "", err
    }
    return host, port, nil
}
```

For `[::1]:443`, `net.SplitHostPort` returns `host="::1"`, and `domainAllowed("::1")` will not match any domain pattern (fails closed, good). But for `CONNECT 127.0.0.1:22`, the attacker can register `localhost.example.com` in their DNS pointing at `127.0.0.1`, get the user to allowlist `*.example.com`, and then `CONNECT localhost.example.com:22` — the host check passes, the dial goes to port 22 on loopback.

Additionally, there are no circuits here that reject literal IP addresses at allowlist time — if the user (or a plugin yaml, or an agent mode config) puts `127.0.0.1` or `169.254.169.254` directly into `AllowedDomains`, the proxy will happily forward. That's arguably "user misconfiguration" but the docs say "domain" allowlist and there's no validation.

## Impact

- **Who:** any sandboxed process whose `networkAllow` list is non-empty. That includes every agent-initiated tool call that requests network access, plus any workflow ShellStep configured with allowed domains.
- **What:** access to host-local services (Redis, Postgres, Docker daemon, Kubernetes API, internal dev servers), cloud metadata endpoints (IMDS v1, GCP metadata), LAN hosts, and arbitrary ports on otherwise-allowlisted hostnames.
- **Blast radius:** depends on what the host exposes. On a developer laptop this is generally "the user's local secrets and dev services"; on a CI runner it could be full cloud credential theft.
- **Reproducibility:** deterministic given a domain-with-a-record-the-attacker-wants.

Reproduction sketches (do NOT run):

- **Metadata:** attacker-controlled domain `loot.example.com` with A record `169.254.169.254`. User allowlists `*.example.com` for a research task. Agent fetches `http://loot.example.com/latest/meta-data/iam/security-credentials/`. Proxy forwards, metadata returned.
- **CONNECT to SSH:** user allowlists `github.com`. Agent `CONNECT github.com:22` — proxy allows because port is unchecked, tunnels raw TCP to port 22 on whatever github.com resolves to. (Not a huge win on GitHub specifically because SSH there wants a key, but the pattern is the issue.)
- **Loopback:** attacker domain resolves to `127.0.0.1`. User allowlists the domain for a legit purpose. Agent reads the host's local Ollama API at `http://<allowed>:11434` and exfiltrates whatever is there.

## Recommendation

Four layered fixes. Apply at least 1, 2, and 3 before beta; 4 is nice-to-have.

1. **Pin DNS resolution to the proxy-side check.** Resolve the hostname once, inside the proxy, BEFORE the domain-allowed check is even considered authoritative. Then dial the *IP* you resolved, not the hostname, and reject the resolved IP if it's in any of the forbidden ranges (loopback, link-local, RFC1918, RFC6598, multicast, IPv6 ULA / link-local, IPv6 loopback). Use a helper like:

   ```go
   func isBlockedIP(ip net.IP) bool {
       if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
          ip.IsMulticast() || ip.IsUnspecified() || ip.IsPrivate() {
           return true
       }
       // RFC 6598 shared address space
       _, cgnat, _ := net.ParseCIDR("100.64.0.0/10")
       if cgnat.Contains(ip) { return true }
       // IMDS
       _, imds, _ := net.ParseCIDR("169.254.169.254/32")
       if imds.Contains(ip) { return true }
       return false
   }
   ```

   For `handleHTTP`, construct a custom `http.Transport` with a `DialContext` that performs the resolution, applies `isBlockedIP`, and only then dials the resulting IP literal. For `handleConnect`, resolve `host`, pick the first allowed IP, and `net.DialTimeout("tcp", net.JoinHostPort(ip.String(), port), ...)`.

2. **Restrict CONNECT to TLS ports.** Unless the user explicitly configures otherwise, only allow `443` (and maybe `8443`) on CONNECT. Anything else returns 403. Do this before the dial:

   ```go
   _, portStr, _ := splitHostPort(r.Host)
   if portStr != "443" && portStr != "8443" {
       http.Error(w, "CONNECT only allowed to TLS ports", http.StatusForbidden)
       return
   }
   ```

3. **Validate allowlist entries at construction time.** In `NewProxy`, reject any entry that is a literal IP, a private/loopback hostname, or contains characters other than `[A-Za-z0-9.-*]`. This prevents someone from writing `127.0.0.1` into a plugin yaml and having it Just Work as a side door.

4. **Add a CONNECT byte-copy timeout and context.** The bidirectional `io.Copy` in `handleConnect` has no deadline; see finding `04-high-proxy-connect-goroutine-leak.md` for the lifetime issue. Related to this finding because the rebinding target can stall forever if you never time out.

Also add regression tests:

- Pattern-match an allowlisted hostname with `127.0.0.1` in its DNS → expect dial rejected.
- `CONNECT allowed.example.com:22` → expect 403 from the port check.
- IPv6 `[::1]:443` → expect rejection by IP classifier.

## References

- `internal/sandbox/proxy.go:82-131` (CONNECT)
- `internal/sandbox/proxy.go:134-186` (HTTP)
- `internal/sandbox/proxy.go:191-208` (domainAllowed)
- OWASP SSRF cheat sheet
- CWE-918 (SSRF), CWE-441 (unintended proxy)
- Go `net.IP.IsPrivate`, `IsLoopback`, etc.
- Related: `04-high-proxy-connect-goroutine-leak.md`, `06-high-linux-network-isolation-gap.md`
