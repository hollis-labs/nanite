# API authentication

Every `/api/` route is authenticated (CW-20261011-0036). There is no
unauthenticated mode except the explicit `nanite serve --insecure-no-auth`
development flag, which warns at every start and never runs under the service
unit. Nanite binds loopback only and refuses every non-loopback bind; off-box access
is described in [remote-access.md](remote-access.md).

Code: `internal/apiauth` (credentials, store, guard, handlers, route rules),
wired in `internal/server` and `cmd/nanite/auth_wiring.go`.

## Credentials

A credential is a bearer secret `nan_<kind>_<43 base64url characters>` (256
random bits). Only its SHA-256 hash is stored, in `auth.db`, a separate SQLite
file in `<data>/auth/` (or beside an explicit `--db`) so restoring `main.db`
cannot resurrect a revoked credential. The secret is shown once, at creation.

| Kind | Holder | Scopes | Lifetime |
|---|---|---|---|
| `operator` | you | all | until rotated or revoked |
| `client` | an embedded client (Tangent backend) | explicit; never `admin` | 30 days by default |
| `agent` | one agent session | `tools` only | 1 h, sliding while the session is live, 24 h cap |

Scopes: `read` (GETs), `operate` (create sessions, send and cancel turns,
approvals, work items), `tools` (`/api/tools/call`), `maintain` (configuration
writes), `admin` (credentials, install, retire, secrets, shell, MCP servers).
Minting can only narrow.

## Routes and scopes

`routes.go` holds an ordered rule table mapping a mux pattern to its scope.
A write no rule names requires `admin`; `TestEveryRegisteredAPIRouteIsClassified`
parses the route registrations and fails until a new route is classified.
`GET /api/health`, `GET /api/agent/v1/initialize`, the agent card, the static UI
shell, `POST /api/auth/login` and `GET /api/auth/link` are public; plugin host
query and wake routes carry their own per-plugin bearer.

## Browser

`POST /api/auth/login` exchanges an operator or client credential for an
HttpOnly, SameSite=Strict `nanite_session` cookie (12 h sliding) and a CSRF
token. Unsafe cookie-authenticated requests need the `X-Nanite-CSRF` header and
a same-origin `Origin`. `nanite auth login-link` mints a one-time, 60 s link
(`GET /api/auth/link?code=…`) that signs a browser in. The SPA wraps `fetch` to
add the CSRF header and shows the login screen on a 401.

## Other rules

- Identity is the credential. `X-Nanite-Caller-*` headers are honored only for
  an agent credential bound to that session; `/api/tools/call` refuses an agent
  credential for another session.
- The `Host` header must name loopback (the bind address, or a name in `http.allowed_hosts`): DNS-rebinding
  defense.
- Revoking a credential or logging out cancels its in-flight requests, closing
  open SSE streams. Expiry bounds a request's context the same way.
- Host-minted agent credentials are planted as `NANITE_AUTH_TOKEN` in the
  launch's `.mcp.json` env for `nanite mcp`.
- The audit table records denials (rate-limited) and admin/maintain writes with
  the credential id, never a secret.

## Operating it

First start creates the operator credential and writes its secret to
`<data>/auth/operator.token` (mode 0600); the path is logged, the secret never.

```
nanite auth token-path            # where the operator token file is
nanite auth whoami
nanite auth create --kind client --label tangent --scope read,operate
nanite auth list [--all]
nanite auth revoke <id>
nanite auth rotate <id>           # operator rotation rewrites the token file
nanite auth login-link            # one-time browser sign-in URL
nanite auth reset-operator        # OFFLINE recovery; works without the server
```

### Show-once issuance to a file

```
nanite auth create --kind client --label tachyon --scope read,operate,agents:manage \
  --output ~/.config/tachyon/nanite.token
nanite auth rotate <id> --output ~/.config/tachyon/nanite.token --force
```

With `--output` the secret is written only to that file (mode 0600, created
whole or not at all) and is never printed, logged, placed in argv or put in an
error: stdout shows the credential's id, scopes and expiry plus `written_to`.
An existing file is refused before anything is issued unless `--force`. If the
file cannot be written after the server minted the credential, the new
credential is revoked, so no credential exists whose secret nobody holds. The
caller (for example Tachyon) is then given the file path, not the secret. The
server stores only a hash, logs and audits only the credential id, and the
secret is in the issuance response alone (tests pin each of these). Issuing a
credential still needs the operator token and, by policy, the owner's approval.

`NANITE_AUTH_TOKEN` (a `nan_operator_…` secret) overrides the token file for
headless runs; the server imports it on start.

## Limits

- Anything running as your OS user can read the operator token file; the
  database holds only hashes. The agent sandbox does not hide the auth
  directory from an agent CLI.
- `reset-operator` revokes in the database; a running server cancels streams
  for revocations made through its own API, not for an offline reset.
