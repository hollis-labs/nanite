# Nanite

Nanite is a CLI agent framework and plugin host: a Go backend with an embedded
React + shadcn SPA, shipping the `nanite`, `nanite-agent` and `nanite-eval`
binaries. It boots agent sessions into a project, executes tools directly, and
extends through hot-loaded subprocess MCP plugins. It is a single-user desktop
application, not a multi-tenant service, and a peer of Torque rather than a
layer beneath it.

## Start Here

- `README.md` is the only outward-facing overview; everything else is internal.
- `cmd/nanite/` is the server and CLI, `cmd/nanite-agent/` installs the agent
  framework, `cmd/nanite-eval/` is the eval harness.
- `internal/chat/` orchestrates a turn; `internal/runtime/agent/` builds an
  agent's boot content and resolves its dynamic context at launch.
- `internal/plugin/` is the plugin host: install state machine, catalog fetch,
  Ed25519 signature verification.
- `internal/workspace/walkup.go` decides which instruction files Nanite reads
  from a project, and in what order.
- `docs/architecture/` holds one current document per subsystem;
  `ls docs/architecture/` is the index.
- `ui/src/generated/` is generator output — regenerate it, never hand-edit it.

## Commands

```bash
lefthook install                  # once per clone — a tracked lefthook.yml installs nothing
go build ./cmd/nanite/            # compile check; writes ./nanite
./nanite serve -dev               # DB resolves via go-apppaths; `nanite path` prints it
go test ./...                     # no -race — the suite pre-push runs
./scripts/check.sh                # the landing check: format, vet, scoped lint, tests
make build                        # generate envelopes + build UI + build binary
make check-envelopes              # generated envelope artifacts vs. the go-envelopes module
```

Run `./scripts/check.sh` when a feature lands, not on every commit — pre-commit
is formatting only, scoped to the staged diff. Its header explains the test
tiers and why its lint stage measures from the merge base with `origin/main`.
`make test` is the full `-race` suite and belongs to the nightly gate; a change
touching goroutines, channels, `context` cancellation, mutexes, atomics or
shutdown ordering needs `-race -count=20` on the package you touched as well.

## Boundaries

Agent profiles and durable instances are database-backed. There is no project
agent catalog — no `.nanite/`, no `config/agents/`, no file that defines a
runtime agent, and `Boot <agent>` is not a resolution mechanism here. The
profiles under `internal/agent/builtin/profiles/` are first-run seeds.

`TASKS/`, `adr/`, `docs/engineering/` and `docs/audits/` were archived out of
this repo at `b58db1fa` for public release. Hundreds of Go comments, script
headers and `Makefile` targets still cite paths beneath them. Those paths do
not resolve and are not coming back; a claim is not verified because a comment
cites one.

The Context Broker's six slot invariants live in
`internal/context/INVARIANTS.md`, enforced by
`internal/service/slot_invariants_test.go`. Change both together or neither.

Migrations carry schema, not application data: no `VALUES` clause in
`internal/store/migrations/*.sql` for rows the application owns — those belong
in `internal/store/seed.go`. `UPDATE`/`DELETE` backfills and the
`INSERT ... SELECT` rebuild idiom are allowed, and so is seeding a closed
vocabulary that is a foreign-key target: `reflex_action_kinds`,
`reflex_action_categories`, `reflex_provenance_tiers`,
`selftool_reaction_kinds`. Those rows are the schema's own enum, which SQLite
has no type for, and they must exist in the same transaction as the constraint
that references them — `store.New` runs every migration before `main.go` calls
`Seed`, so a rebuild inserting `action_kind = 'resume_loop_run'` would fail its
foreign key against a vocabulary row `seed.go` has not written yet.

A duplicate migration number is this repo's one unrecoverable failure, so
`migration-number` runs on every push to `main` with no file filter. Do not
add one — a filter here fails open and prints as a benign skip.

The `devmode` build tag disables plugin signature verification. `make build`
must never set it; `make build-dev` exists for that and must never ship.

The envelope catalog belongs to the released `go-envelopes` module, not to this
repo — `config/envelopes.yaml` no longer exists. New core types are released
there first; this repo adds the React component under
`ui/src/components/chat/envelopes/` and regenerates.

`go build` produces `./nanite`, which is not the binary the running service
executes. Deployment goes through the Cerberus resource `nanite-api-service`:
`deploy` syncs the artifact, `reload` is the cutover, and deploy alone can
leave the previous process running on the old bytes.
