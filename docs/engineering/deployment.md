# Deployment

## Build vs. deploy — these are separate binaries

`go build ./cmd/nanite/` is a **compile check only** — it outputs `./nanite` in the project root. The *running* service uses a separate artifact at `~/.cerberus/apps/nanite/nanite-api-service/bin/nanite-api-service`. Editing/building one does not affect the other. This has been a real source of confusion — always deploy through Cerberus, never assume a local `go build` changes what's actually running.

## The real cutover recipe

```bash
# Resource id is nanite-api-service (not nanite-api)
cerberus_resource_deploy nanite-api-service
cerberus_resource_reload nanite-api-service     # explicit cutover — deploy alone can leave
                                                 # the prior pid running on the old artifact
```

Restart without rebuilding (config change, MCP catalog refresh, etc.):

```bash
cerberus_resource_reload nanite-api-service
```

Verify:

```bash
cerberus_resource_status nanite-api-service
cerberus_resource_logs nanite-api-service --lines 50 --stream stderr
```

After `reload`, `cerberus_resource_status` should show a new `launchd_pid` and `last exit code = 0` for the prior process. If the pid hasn't changed, the cutover didn't happen — re-run `reload`.

## Frontend

```bash
cd ui && npm install && npm run build
```

## No dependencies, by design

Nanite must be able to exist and run using nothing but itself. Cerberus is a deployment convenience (equivalent to systemd/pm2), not an architectural dependency — Nanite has to work without it. Agent Mux (the optional MCP proxy) is the same category: purely optional, Nanite must work fully with it entirely absent.

## Not yet documented here

CI/CD pipeline details, environment/secrets management beyond what's in `internal/config`, rollback procedure if a deploy goes bad, and monitoring/alerting setup. Add as they're established.
