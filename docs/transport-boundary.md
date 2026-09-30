# Transport boundary: transports call the service layer, not the store

Transports — the HTTP handlers, the MCP server and the agent self-tools —
decode a request, call the service layer and encode the answer. They do not call
store methods and do not write SQL. This repo enforces the mechanical half of
that with a second golangci-lint config, `.golangci.transport.yml`, and gates
only new code against it rather than walling the existing tree. The other half —
two transports deciding the same thing twice — lint cannot see; the parity
checklist below covers it.

| Input | Value |
|---|---|
| Module | `github.com/hollis-labs/nanite` |
| Store handle | `store.Store` (package name, not import path) in `internal/store` |
| Transport packages | `^internal/(api\|selftools\|mcpserver)/` |

The composition root — `cmd/nanite`, and the wiring that builds
`service.Container` — may open the store and is outside the scope regex by
construction. `internal/selftools` is in scope but is not fixed the same way as
`internal/api`: `internal/service` imports `internal/mcp`, so selftools cannot
import `internal/service` without a cycle, and it reaches the store through
narrow local interfaces instead (`self_tools_agent_profiles.go`).
`internal/mcp` is the MCP client manager and dev-tool adapters, not a
transport, and is deliberately outside the regex.

## Run it

```sh
GOWORK=off golangci-lint run --config .golangci.transport.yml ./...
# count only (v2 has no --out-format):
GOWORK=off golangci-lint run --config .golangci.transport.yml --issues-exit-code=0 \
  --output.json.path=stdout --output.text.path=/dev/null --show-stats=false ./... | jq '.Issues|length'
```

Exit code is 1 on findings unless `--issues-exit-code=0`. `golangci-lint`
refuses concurrent runs ("parallel golangci-lint is running"): serialize, or
pass `--allow-parallel-runners`.

A file that does not type-check produces a `typecheck` error for its package
and no forbidigo findings from it. A zero from a package that failed to build is
not a zero.

## The rules

- **Rule A (forbidigo):** `^store\.Store\.[A-Z].*$`. The form is
  `<package NAME>.<Type>.<Method>`; a fully-qualified import-path pattern
  matches nothing. Type-only use (`store.AgentProfile`,
  `store.ErrDurableAgentInstanceNotFound`) is not flagged.
- **Rule C (forbidigo):** `database/sql` `DB|Tx|Conn|Stmt` `Exec|Query|Prepare|Begin*`
  inside a transport. Kept here because transport packages already import
  `database/sql` for `sql.ErrNoRows` and the `sql.Null*` types.
- **Rule B (depguard, commented out in the config):** bans importing the store
  package from transports. Not affordable here: transports use store types as
  wire and domain types, so the import ban is a wall, not a ratchet.

### Requirements that make it work

Each of these is a silent-zero or silent-truncation trap.

1. `forbidigo.analyze-types: true`: without it the pattern sees source text,
   not types.
2. `run.relative-path-mode: gomod`: `path-except` is otherwise relative to the
   config file's directory; a config outside the module sees `../../..` paths
   and reports 0 issues.
3. `issues.max-issues-per-linter: 0`, `max-same-issues: 0`,
   `uniq-by-line: false`: the defaults cap at 50 and silently truncate.
4. `run.tests: false`, and the `_test\.go$` exclusion if the rules are ever
   merged into a config that lints tests — test files call the store directly
   by design and inflate the count.
5. Always `GOWORK=off` when the repo sits under a `go.work`; a portfolio
   `go.work` breaks type-checking.
6. **CI:** any job that runs this config must use `actions/setup-go` with
   `go-version-file: go.mod` (never `stable` or a literal), and golangci-lint
   `>= v2.11.4`. v2.1.6 is built with go1.24 and refuses go 1.26 modules.

## Where it is gated

| Rung | What | Gate | Here |
|---|---|---|---|
| R0 report-only | `--issues-exit-code=0`, print the count | none | yes |
| R1 new code only | `--new-from-rev` against the merge base `scripts/check.sh` already computes | blocking in `check.sh`, soft: sees touched lines only | yes |
| R2 count ceiling | CI fails when the total rises above a committed ceiling that only decreases | blocking | not adopted |
| R3 hard | ceiling 0, plain non-zero exit | blocking | not adopted |

R1 runs in `scripts/check.sh` only. It is not on a git hook (pre-commit is
formatting-only by design) and the nightly gate does not fail on it.

A revision passed to `--new-from-rev` that does not exist makes golangci-lint
warn ("could not read git repo") and report every finding; it does not silently
pass.

`service.Container.Store` stays exported while call sites still use it. It is
unexported last, behind one documented accessor, once the count is near zero;
unexporting it first would move every call site onto the accessor without
removing a single finding.

## Transport-parity checklist

1. One decision function per operation below the transports; transports
   decode, call, encode.
2. Authorization and scoping live below the transports, so the HTTP and MCP
   paths cannot disagree.
3. Not-found and conflict travel as typed sentinels checked with `errors.Is`,
   not `strings.Contains(err.Error(), ...)`.
4. Surface lists are enumerated at registration, not hand-maintained; waivers
   are explicit.
5. Composition roots (`cmd/`, serve wiring) may open the store and sit outside
   the scope regex.
6. The lint config is active on every transport package.
7. Wire types are API-owned DTOs, not storage records. The config polices
   calls, not type reuse, so this one is review's job.

## Limits and blind spots

- **Interface-mediated calls are invisible.** A transport calling through a
  consumer-defined interface over the store looks like a legitimate service
  interface. That is the shape `internal/selftools` uses, which is why its
  findings are only its direct calls.
- **Type-only coupling is not a handle leak.** Rule A does not flag a store
  type used as a wire type; checklist item 7 does.
- **Promotion through an embedded field is invisible.** With
  `type wrap struct{ *store.Store }`, a call `w.Get(2)` is not flagged; a direct
  call and a call through a type alias (`type alias = store.Store`) are. A
  wrapper type that hides the store this way defeats Rule A.
- **The regex scopes by path.** A transport package outside
  `^internal/(api|selftools|mcpserver)/` is not checked; a new transport
  directory needs the regex extended.
- **Not a parity check.** Two transports implementing the same decision
  differently pass this gate.
- `run.tests: false`: test files are never checked.
- `--new-from-rev` only sees touched lines; R1 alone lets an old violation be
  edited around without being fixed.
- Behaviour on golangci-lint releases other than v2.11.4 was not checked;
  re-measure after a bump.
