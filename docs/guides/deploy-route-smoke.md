# Post-deploy route smoke

`deploy/deploy.yaml` lists, after `/api/health` (200, version must match), four more health entries per target:
`/api/agents`, `/api/start-surface/capabilities`, `/api/providers` and `/api/sessions`, each expecting **401**.
`fleet-install` runs them after the restart, and a failure rolls the deploy back automatically (same path as a
failed `/api/health`).

## What it proves, and what it does not

An unauthenticated request to a protected route answers 401 from the auth layer before any handler runs. So this
smoke proves the new binary is up, routing and enforcing auth. It does **not** exercise the handler: the
`GET /api/agents` 500 fixed in #594 (a refused definition failing the whole list) would have passed it.

## Closing that gap needs a credential decision

An authenticated GET of those routes would catch handler failures. It needs a read-scoped Nanite credential, and
none exists on the hosts (only the admin operator token, which a deploy tool should not hold). When one is issued:

1. Store it 0600 on the host (never in the repo or deploy.yaml).
2. `fleet-install` needs an optional per-check `bearer_token_file` (not built yet; one small change in
   `tools/fleet-install`, read at call time, never logged).
3. Switch the four entries to `expect_status: [200]` with `bearer_token_file`.

## Periodic checks

Deploy-time checks do not catch a route that breaks later. That belongs to Cerberus health monitoring; see the
proposal in Tesseract `project/cerberus/knowledge/handoffs`.
