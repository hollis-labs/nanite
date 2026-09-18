# Nanite

Nanite is a CLI agent framework and plugin host: a Go backend with an embedded
React + shadcn SPA, shipping the `nanite`, `nanite-agent` and `nanite-eval`
binaries. It boots agent sessions into a project, executes tools directly, and
extends through hot-loaded subprocess MCP plugins. It is a single-user desktop
application, not a multi-tenant service, and a peer of Torque rather than a
layer beneath it.

## Where Nanite is

Not released, not deployed, no consumers. The next milestone is a public repo
and building in the open. Chrispian decides when that happens — there are no
criteria to meet and no date.

So **release readiness is a direction, not a phase.** Security, testing and
release prep are ordinary work competing on merit with features, bug fixes and
everything else, sequenced by Chrispian's direction each session. A
`public-release` tag names the subject, never the urgency, and a board sorted
by it is not a plan.

The reasoning is `~/dev/projects/agent-setup/docs/what-a-check-may-assert.md`,
*Tighten at the first real consumer*: until someone outside the project can be
broken by a regression, the cost of a regression is one session noticing.

**Where this stops.** This is not licence to skip verification. Data integrity,
security boundaries, and anything that can silently lose work still get the
real treatment — what changes is what gets *scheduled*, not how carefully it is
done once it is.

## Start Here

- `docs/documentation-doctrine.md` governs what a file here may contain and who
  it is for. Audience follows location — read it before adding or moving one.
  It is the local counterpart to the portfolio documents in
  `~/dev/projects/agent-setup/docs/`, which carry how we approach recurring
  problems with the reasoning attached — `lenses.md` keyed on the situation you
  are in, `_owned-subjects-index.md` on the subject you are about to write.
  Those own the general shape; this file and the doctrine own what is true
  *here*.
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

The Context Broker's slot invariants live in
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

**Before a migration renames or removes a column, verify nothing already
selects it.** A rename or removal breaks every running process the moment the
migration applies — they hold compiled-in queries naming the old column, which
instantly fail with `no such column` against the altered schema. An `ADD
COLUMN` does not break running processes because queries name columns
explicitly and a column nobody selects is invisible to an already-open
connection. Check this before merge: `strings <deployed-artifact> | grep
<old-column-name>` — nonzero hits mean a live process selects it, and the
migration will break running instances on apply. Coordinate the deployment: new
binary deployed and service restarted *before* any post-migration process
applies the schema change. This is the generalizable lesson from migration 159's
`urn` → `legacy_urn` rename (`CW-20260912-0043`).

The `devmode` build tag disables plugin signature verification. `make build`
must never set it; `make build-dev` exists for that and must never ship.

The envelope catalog belongs to the released `go-envelopes` module, not to this
repo — `config/envelopes.yaml` no longer exists. New core types are released
there first; the GUI (the separate `flux` repo) adds the React component under
`src/components/chat/envelopes/` and regenerates.

`go build` produces `./nanite`, which is not the binary the running service
executes. Deployment goes through the Cerberus resource `nanite-api-service`:
`deploy` syncs the artifact, `reload` is the cutover, and deploy alone can
leave the previous process running on the old bytes.
