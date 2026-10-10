# Nanite

Nanite is a CLI agent framework and plugin host: a Go backend with an embedded
React + shadcn SPA, shipping the `nanite`, `nanite-agent` and `nanite-eval`
binaries. It boots agent sessions into a project, executes tools directly, and
extends through hot-loaded subprocess MCP plugins. It is a single-user desktop
application, not a multi-tenant service.

Verification should fit the change, but data integrity, security boundaries,
and anything that can silently lose work always get the real treatment.

## Start Here

- `docs/documentation-doctrine.md` governs what a file here may contain and who
  it is for. Audience follows location — read it before adding or moving one.
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
- `SECURITY.md` describes the deployment boundary and the known security
  limitations; keep a change consistent with it.

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
`make test` is the full `-race` suite and belongs to the nightly CI gate; a
change touching goroutines, channels, `context` cancellation, mutexes, atomics
or shutdown ordering needs `-race -count=20` on the package you touched as
well.

To land a change, open a pull request; a maintainer will review it.
`CONTRIBUTING.md` has the sequence.

## Boundaries

Ordinary agent readers use immutable agentdef v2 artifacts in
`agent_definitions` and mutable typed execution inputs in `agent_host_settings`.
Native `/api/agent/v1` creates cognitive views from a verified semantic pin;
optional host settings are selected at an explicit revision. Authored resources
are checked against exact byte digests. Nine pristine flat archetypes live under
`internal/agentdefs/`; the original General Chat definition remains unchanged.
Historical `agent_profiles` and their old relation graph are retained solely for
explicit history/export and audited retirement. They are never runtime fallback,
first-run seeds or a conversion source. Actor bindings and grants require real
issuer-owned authority; a definition pin, host UUID, slug or request metadata
does not enroll an actor or confer capabilities. Missing adopted issuer ports
refuse explicitly. See `docs/adding-an-agent.md` for authoring and host setup.

`TASKS/`, `adr/`, `docs/engineering/` and `docs/audits/` were archived out of
this repo at `b58db1fa`. Many Go comments, script headers and `Makefile`
targets still cite paths beneath them. Those paths do not resolve and are not
coming back; a claim is not verified because a comment cites one.

The Context Broker's slot invariants live in
the pinned substrate agent module’s `context/INVARIANTS.md`, enforced by
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
connection. Check this before merge: `strings <deployed-binary> | grep
<old-column-name>` — nonzero hits mean a live process selects it, and the
migration will break running instances on apply. Deploy the new binary and
restart the service *before* any post-migration process applies the schema
change.

Production builds use no build tags. `make build-dev` enables development-only
agent profiles and must never ship.

The envelope catalog belongs to the released `go-envelopes` module, not to this
repo — `config/envelopes.yaml` no longer exists. New core types are released
there first; this repo adds the React component under
`ui/src/components/chat/envelopes/` and regenerates.

`go build` writes `./nanite` in the working tree; it does not replace the
binary a running `nanite serve` is executing. Restart the server on the new
binary before treating a change as live.
