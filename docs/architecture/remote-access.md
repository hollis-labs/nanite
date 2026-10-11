# Remote access

Nanite listens on loopback and does not terminate TLS. `validateBind` in
`internal/server/auth.go` refuses any other bind address, whatever the
configuration says. Every credential is a bearer secret, and a bearer secret
must not cross a network in the clear, so reaching Nanite from another machine
means putting something else in front of the loopback listener.

## Supported postures

| Posture | How it reaches Nanite | TLS comes from |
|---|---|---|
| Same machine | `http://127.0.0.1:<port>` | not needed |
| SSH forward | `ssh -L <local>:127.0.0.1:<port> host` | the SSH channel |
| Tailscale Serve | `tailscale serve` to `127.0.0.1:<port>` | Tailscale |
| TLS-terminating reverse proxy on the same host | proxy connects to `127.0.0.1:<port>` | the proxy |

All four present Nanite to the browser or CLI as if it were local. None needs a
change to Nanite beyond the settings below.

## Settings a proxy needs

Both live under `http:` in the configuration file.

- `allowed_hosts` lists the DNS names a proxy forwards in the `Host` header.
  The Host check is the DNS-rebinding defense: loopback names and the bind
  address pass, and any other name gets a 403 until it is listed. List only
  names you control.
- `cors_allowed_origins` must contain the proxy's external HTTPS origin for
  admin writes, which require a well-formed `Origin` that matches exactly.

Nanite does not read `X-Forwarded-*` or `Forwarded` headers. It sees plain HTTP
from the proxy, so it cannot tell when the browser is on HTTPS, and nothing it
decides depends on that.

## Credentials off-box

Mint a credential for the remote caller with `nanite auth create` and the
narrowest scopes it needs; see [api-auth.md](api-auth.md). Prefer an SSH forward
or Tailscale Serve for a single operator, a proxy for a name shared by several
tools. The operator token file stays on the machine running Nanite.

## Why not built-in TLS

Terminating TLS inside Nanite would add certificate sourcing and reload, HSTS
and a second remote path to test, for no case the postures above do not already
cover. If a remote use case appears that cannot use a forward or a proxy, built-in
TLS with operator-provided certificate and key is the shape to start from, and
the bind refusal would then become "refuse unless TLS is configured".
